package observers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/assessment"
	"github.com/stephan271/smallworlds/operator-console/internal/kubeclient"
)

// fakeAPI is a stand-in Kubernetes API server: a path-to-object map plus a
// per-path failure switch and a read counter, which is all the live observers
// touch.
type fakeAPI struct {
	objects map[string]any
	fail    map[string]error
	reads   map[string]int
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{objects: map[string]any{}, fail: map[string]error{}, reads: map[string]int{}}
}

func (api *fakeAPI) Get(_ context.Context, path string, target any) error {
	api.reads[path]++
	if err, failing := api.fail[path]; failing {
		return err
	}
	object, ok := api.objects[path]
	if !ok {
		return kubeclient.ErrNotFound
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}

func argoPath(name string) string {
	return kubeclient.NamespacedPath(kubeclient.ArgoAPI, "argocd", "applications", name)
}

// healthyCluster wires one capability the way a converged cluster looks: a
// synced and healthy Argo Application over a ready Deployment, a bound claim, a
// Service with endpoints, and a publicly routed Ingress with an issued
// certificate.
func healthyCluster() *fakeAPI {
	api := newFakeAPI()
	api.objects[argoPath("nextcloud")] = map[string]any{
		"status": map[string]any{
			"sync":         map[string]any{"status": "Synced"},
			"health":       map[string]any{"status": "Healthy"},
			"reconciledAt": "2026-07-28T10:00:00Z",
			"resources": []map[string]any{
				{"kind": "Deployment", "namespace": "nextcloud", "name": "nextcloud"},
				{"kind": "PersistentVolumeClaim", "namespace": "nextcloud", "name": "nextcloud-data"},
				{"kind": "Service", "namespace": "nextcloud", "name": "nextcloud"},
				{"kind": "Ingress", "namespace": "nextcloud", "name": "nextcloud"},
			},
		},
	}
	api.objects[kubeclient.NamespacedPath(kubeclient.AppsAPI, "nextcloud", "deployments", "nextcloud")] = map[string]any{
		"metadata": map[string]any{"generation": 4},
		"spec":     map[string]any{"replicas": 2},
		"status":   map[string]any{"observedGeneration": 4, "readyReplicas": 2, "updatedReplicas": 2},
	}
	api.objects[kubeclient.NamespacedPath(kubeclient.CoreAPI, "nextcloud", "persistentvolumeclaims", "nextcloud-data")] = map[string]any{
		"status": map[string]any{"phase": "Bound"},
	}
	api.objects[kubeclient.NamespacedPath(kubeclient.CoreAPI, "nextcloud", "endpoints", "nextcloud")] = map[string]any{
		"subsets": []map[string]any{{"addresses": []map[string]any{{"ip": "10.42.0.11"}}}},
	}
	api.objects[kubeclient.NamespacedPath(kubeclient.NetworkingAPI, "nextcloud", "ingresses", "nextcloud")] = map[string]any{
		"metadata": map[string]any{"annotations": map[string]string{entrypointsAnnotation: "websecure"}},
		"spec": map[string]any{
			"rules": []map[string]any{{"host": "cloud.example.test"}},
			"tls":   []map[string]any{{"secretName": "nextcloud-tls"}},
		},
	}
	api.objects[kubeclient.NamespacedPath(kubeclient.CertManagerAPI, "nextcloud", "certificates", "")] = map[string]any{
		"items": []map[string]any{{
			"spec":   map[string]any{"secretName": "nextcloud-tls"},
			"status": map[string]any{"conditions": []map[string]any{{"type": "Ready", "status": "True"}}},
		}},
	}
	return api
}

func liveSources(api *fakeAPI) *LiveSources {
	return &LiveSources{
		Reader:        api,
		ArgoNamespace: "argocd",
		LookupHost: func(_ context.Context, host string) ([]string, error) {
			if host == "cloud.example.test" {
				return []string{"203.0.113.10"}, nil
			}
			return nil, errors.New("no such host")
		},
	}
}

func TestLiveSourcesObserveConvergedCapability(t *testing.T) {
	sources := liveSources(healthyCluster())

	delivery, at, err := sources.ObserveDelivery(context.Background(), "nextcloud")
	if err != nil {
		t.Fatalf("observe delivery: %v", err)
	}
	if at.IsZero() {
		t.Fatal("expected an observation timestamp")
	}
	if !delivery.ApplicationFound || delivery.SyncStatus != SyncSynced || delivery.HealthStatus != HealthHealthy {
		t.Fatalf("delivery = %+v", delivery)
	}
	if delivery.LastReconciledAt.IsZero() {
		t.Fatal("expected the Argo reconciliation time to be carried through")
	}

	runtime, _, err := sources.ObserveRuntime(context.Background(), "nextcloud")
	if err != nil {
		t.Fatalf("observe runtime: %v", err)
	}
	if !runtime.WorkloadsFound || !runtime.AllWorkloadsReady || !runtime.AllPVCsBound || !runtime.ProbesPassing || runtime.FailedJobs != 0 {
		t.Fatalf("runtime = %+v", runtime)
	}

	access, _, err := sources.ObserveAccess(context.Background(), "nextcloud")
	if err != nil {
		t.Fatalf("observe access: %v", err)
	}
	if !access.DNSResolves || !access.CertificateReady || !access.PublicReachable || access.GatewayReachable {
		t.Fatalf("access = %+v", access)
	}
}

// The engine's central invariant is that an Argo-Healthy delivery over a
// not-yet-ready workload never reads healthy. That only holds if runtime
// evidence is read from Kubernetes rather than from Argo's own roll-up, so this
// is the test that keeps the two sources genuinely independent.
func TestLiveSourcesReadRuntimeIndependentlyOfArgoHealth(t *testing.T) {
	api := healthyCluster()
	api.objects[kubeclient.NamespacedPath(kubeclient.AppsAPI, "nextcloud", "deployments", "nextcloud")] = map[string]any{
		"metadata": map[string]any{"generation": 5},
		"spec":     map[string]any{"replicas": 2},
		"status":   map[string]any{"observedGeneration": 5, "readyReplicas": 1, "updatedReplicas": 2},
	}
	sources := liveSources(api)

	delivery, _, _ := sources.ObserveDelivery(context.Background(), "nextcloud")
	runtime, _, _ := sources.ObserveRuntime(context.Background(), "nextcloud")
	if delivery.HealthStatus != HealthHealthy {
		t.Fatalf("expected Argo to still report Healthy, got %q", delivery.HealthStatus)
	}
	if runtime.AllWorkloadsReady {
		t.Fatal("a Deployment with one of two replicas ready must not read as ready")
	}
	if !runtime.AnyWorkloadStarting {
		t.Fatal("a partially ready Deployment is starting")
	}

	result := assessment.Assess(Gatherer{Delivery: sources, Runtime: sources, Configuration: sources, Access: sources}.Gather(context.Background(), assessment.CapabilityRef{ID: "nextcloud", Exposure: assessment.ExposurePublic}))
	if result.State == assessment.StateHealthy {
		t.Fatal("an Argo-Healthy application over an unready workload must not assess healthy")
	}
}

// A status the controller has not observed yet describes the previous
// generation, so it cannot vouch for the current one.
func TestLiveSourcesTreatUnobservedGenerationAsNotReady(t *testing.T) {
	api := healthyCluster()
	api.objects[kubeclient.NamespacedPath(kubeclient.AppsAPI, "nextcloud", "deployments", "nextcloud")] = map[string]any{
		"metadata": map[string]any{"generation": 9},
		"spec":     map[string]any{"replicas": 1},
		"status":   map[string]any{"observedGeneration": 8, "readyReplicas": 1, "updatedReplicas": 1},
	}
	runtime, _, err := liveSources(api).ObserveRuntime(context.Background(), "nextcloud")
	if err != nil {
		t.Fatalf("observe runtime: %v", err)
	}
	if runtime.AllWorkloadsReady {
		t.Fatal("a stale controller status must not read as ready")
	}
}

// An Application that does not exist yet is evidence — declared, awaiting
// delivery — and must not be reported as a read failure.
func TestLiveSourcesReportAbsentApplicationAsPending(t *testing.T) {
	sources := liveSources(newFakeAPI())
	delivery, _, err := sources.ObserveDelivery(context.Background(), "immich")
	if err != nil {
		t.Fatalf("a missing Application must not be an error: %v", err)
	}
	if delivery.ApplicationFound {
		t.Fatal("expected ApplicationFound to be false")
	}
}

// A read that fails is the absence of evidence, which the translators turn into
// Missing — never into a healthy-looking answer.
func TestLiveSourcesReportReadFailureAsError(t *testing.T) {
	api := healthyCluster()
	api.fail[argoPath("nextcloud")] = kubeclient.ErrForbidden
	sources := liveSources(api)

	if _, _, err := sources.ObserveDelivery(context.Background(), "nextcloud"); !errors.Is(err, kubeclient.ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
	evidence := Gatherer{Delivery: sources}.Gather(context.Background(), assessment.CapabilityRef{ID: "nextcloud"}).Delivery
	if !evidence.Missing {
		t.Fatal("a failed read must produce Missing evidence")
	}
}

func TestLiveSourcesReadIngressEntrypointExposure(t *testing.T) {
	tests := []struct {
		name           string
		annotations    map[string]string
		wantPublic     bool
		wantViaGateway bool
	}{
		{name: "public entrypoint", annotations: map[string]string{entrypointsAnnotation: "websecure"}, wantPublic: true},
		{name: "private gateway entrypoint", annotations: map[string]string{entrypointsAnnotation: "private-gateway"}, wantViaGateway: true},
		{name: "both entrypoints", annotations: map[string]string{entrypointsAnnotation: "websecure,private-gateway"}, wantPublic: true, wantViaGateway: true},
		// No annotation means Traefik serves the route on every entrypoint it
		// has, which includes the public ones. That is exposure, not silence.
		{name: "no annotation", annotations: map[string]string{}, wantPublic: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := healthyCluster()
			ingress := api.objects[kubeclient.NamespacedPath(kubeclient.NetworkingAPI, "nextcloud", "ingresses", "nextcloud")].(map[string]any)
			ingress["metadata"] = map[string]any{"annotations": test.annotations}
			access, _, err := liveSources(api).ObserveAccess(context.Background(), "nextcloud")
			if err != nil {
				t.Fatalf("observe access: %v", err)
			}
			if access.PublicReachable != test.wantPublic {
				t.Fatalf("PublicReachable = %v, want %v", access.PublicReachable, test.wantPublic)
			}
			if access.GatewayReachable != test.wantViaGateway {
				t.Fatalf("GatewayReachable = %v, want %v", access.GatewayReachable, test.wantViaGateway)
			}
		})
	}
}

// A route on the public entrypoint is only actually public if nothing in front
// of it turns public traffic away. An operator interface restricted to the
// Private Network must not be reported as exposed to the internet.
func TestLiveSourcesReadSourceRestrictionFromMiddleware(t *testing.T) {
	tests := []struct {
		name           string
		sourceRanges   []string
		wantPublic     bool
		wantViaGateway bool
	}{
		{
			name:           "restricted to the Private Network",
			sourceRanges:   []string{"100.64.0.0/10"},
			wantPublic:     false,
			wantViaGateway: true,
		},
		{
			name:         "restricted to the local network only",
			sourceRanges: []string{"192.168.0.0/16", "10.0.0.0/8"},
			wantPublic:   false,
		},
		{
			// One public range in the allow-list and the route is public again.
			// It stays reachable on the Private Network too — both are true, and
			// it is the public exposure that makes it a finding.
			name:           "allows a public range",
			sourceRanges:   []string{"100.64.0.0/10", "203.0.113.0/24"},
			wantPublic:     true,
			wantViaGateway: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := healthyCluster()
			ingress := api.objects[kubeclient.NamespacedPath(kubeclient.NetworkingAPI, "nextcloud", "ingresses", "nextcloud")].(map[string]any)
			ingress["metadata"] = map[string]any{"annotations": map[string]string{
				entrypointsAnnotation: "websecure",
				middlewaresAnnotation: "nextcloud-operator-only@kubernetescrd",
			}}
			api.objects[kubeclient.NamespacedPath(kubeclient.TraefikAPI, "nextcloud", "middlewares", "operator-only")] = map[string]any{
				"spec": map[string]any{"ipAllowList": map[string]any{"sourceRange": test.sourceRanges}},
			}
			access, _, err := liveSources(api).ObserveAccess(context.Background(), "nextcloud")
			if err != nil {
				t.Fatalf("observe access: %v", err)
			}
			if access.PublicReachable != test.wantPublic {
				t.Fatalf("PublicReachable = %v, want %v", access.PublicReachable, test.wantPublic)
			}
			if access.GatewayReachable != test.wantViaGateway {
				t.Fatalf("GatewayReachable = %v, want %v", access.GatewayReachable, test.wantViaGateway)
			}
		})
	}
}

// A middleware that cannot be read or understood leaves the route counted as
// public: the conservative answer to "is this exposed?" is yes.
func TestLiveSourcesTreatUnreadableMiddlewareAsNoRestriction(t *testing.T) {
	api := healthyCluster()
	ingress := api.objects[kubeclient.NamespacedPath(kubeclient.NetworkingAPI, "nextcloud", "ingresses", "nextcloud")].(map[string]any)
	ingress["metadata"] = map[string]any{"annotations": map[string]string{
		entrypointsAnnotation: "websecure",
		middlewaresAnnotation: "nextcloud-absent@kubernetescrd",
	}}
	access, _, err := liveSources(api).ObserveAccess(context.Background(), "nextcloud")
	if err != nil {
		t.Fatalf("observe access: %v", err)
	}
	if !access.PublicReachable {
		t.Fatal("an unreadable middleware must not be taken as protection")
	}
}

// Certificate readiness is read from the cert-manager Certificate, never from
// the TLS Secret — so the console's ServiceAccount never needs to read secret
// material to answer whether TLS is ready.
func TestLiveSourcesReadCertificateReadinessWithoutSecrets(t *testing.T) {
	api := healthyCluster()
	api.objects[kubeclient.NamespacedPath(kubeclient.CertManagerAPI, "nextcloud", "certificates", "")] = map[string]any{
		"items": []map[string]any{{
			"spec":   map[string]any{"secretName": "nextcloud-tls"},
			"status": map[string]any{"conditions": []map[string]any{{"type": "Ready", "status": "False"}}},
		}},
	}
	access, _, err := liveSources(api).ObserveAccess(context.Background(), "nextcloud")
	if err != nil {
		t.Fatalf("observe access: %v", err)
	}
	if access.CertificateReady {
		t.Fatal("a Certificate that is not Ready must not read as ready")
	}
	for path := range api.reads {
		if strings.Contains(path, "/secrets") {
			t.Fatalf("the observers must not read Secrets, but read %q", path)
		}
	}
}

// A Service whose endpoints are empty is precisely a readiness probe that is not
// passing: an unready pod is removed from the endpoints.
func TestLiveSourcesReadProbeEvidenceFromEndpoints(t *testing.T) {
	api := healthyCluster()
	api.objects[kubeclient.NamespacedPath(kubeclient.CoreAPI, "nextcloud", "endpoints", "nextcloud")] = map[string]any{"subsets": []map[string]any{}}
	runtime, _, err := liveSources(api).ObserveRuntime(context.Background(), "nextcloud")
	if err != nil {
		t.Fatalf("observe runtime: %v", err)
	}
	if runtime.ProbesPassing {
		t.Fatal("a Service without ready endpoints must not report passing probes")
	}
}

func TestLiveSourcesCountFailedJobs(t *testing.T) {
	api := healthyCluster()
	application := api.objects[argoPath("nextcloud")].(map[string]any)
	status := application["status"].(map[string]any)
	status["resources"] = append(status["resources"].([]map[string]any), map[string]any{"kind": "Job", "namespace": "nextcloud", "name": "nextcloud-init"})
	api.objects[kubeclient.NamespacedPath(kubeclient.BatchAPI, "nextcloud", "jobs", "nextcloud-init")] = map[string]any{
		"status": map[string]any{"conditions": []map[string]any{{"type": "Failed", "status": "True"}}},
	}
	runtime, _, err := liveSources(api).ObserveRuntime(context.Background(), "nextcloud")
	if err != nil {
		t.Fatalf("observe runtime: %v", err)
	}
	if runtime.FailedJobs != 1 {
		t.Fatalf("FailedJobs = %d, want 1", runtime.FailedJobs)
	}
}

func TestLiveSourcesObserveConfigurationFromRootApplication(t *testing.T) {
	api := healthyCluster()
	api.objects[argoPath("smallworlds-root")] = map[string]any{
		"status": map[string]any{"resources": []map[string]any{
			{"kind": "Application", "namespace": "argocd", "name": "nextcloud"},
			{"kind": "Application", "namespace": "argocd", "name": "keycloak"},
		}},
	}
	sources := liveSources(api)
	sources.Dependencies = map[string][]string{"nextcloud": {"keycloak"}}

	// keycloak is declared but its Application is degraded, so the dependency is
	// not satisfied.
	api.objects[argoPath("keycloak")] = map[string]any{
		"status": map[string]any{"sync": map[string]any{"status": "Synced"}, "health": map[string]any{"status": "Degraded"}},
	}

	facts, _, err := sources.ObserveConfiguration(context.Background(), "nextcloud")
	if err != nil {
		t.Fatalf("observe configuration: %v", err)
	}
	if !facts.Selected || !facts.DeclaredInGit {
		t.Fatalf("expected nextcloud to read as declared: %+v", facts)
	}
	if len(facts.UnmetDependencies) != 1 || facts.UnmetDependencies[0] != "keycloak" {
		t.Fatalf("UnmetDependencies = %v, want [keycloak]", facts.UnmetDependencies)
	}

	// A capability the root Application does not manage is not declared.
	other, _, err := sources.ObserveConfiguration(context.Background(), "immich")
	if err != nil {
		t.Fatalf("observe configuration: %v", err)
	}
	if other.Selected || other.DeclaredInGit {
		t.Fatalf("expected immich to read as not declared: %+v", other)
	}
}

// An Argo condition naming a specification or comparison error is the only
// in-cluster evidence that the overlay is missing a required value.
func TestLiveSourcesReadRequiredValuesFromArgoConditions(t *testing.T) {
	api := healthyCluster()
	api.objects[argoPath("smallworlds-root")] = map[string]any{
		"status": map[string]any{"resources": []map[string]any{{"kind": "Application", "name": "nextcloud"}}},
	}
	application := api.objects[argoPath("nextcloud")].(map[string]any)
	application["status"].(map[string]any)["conditions"] = []map[string]any{
		{"type": "ComparisonError", "message": "helm values missing"},
	}
	facts, _, err := liveSources(api).ObserveConfiguration(context.Background(), "nextcloud")
	if err != nil {
		t.Fatalf("observe configuration: %v", err)
	}
	if facts.RequiredValuesMet {
		t.Fatal("an Argo ComparisonError must read as required values missing")
	}
}

// A root Application that cannot be read is not "nothing is declared"; it is a
// cluster whose delivery cannot be seen, and the facet must be unknown.
func TestLiveSourcesReportUnreadableRootApplicationAsError(t *testing.T) {
	api := healthyCluster()
	api.fail[argoPath("smallworlds-root")] = errors.New("connection refused")
	if _, _, err := liveSources(api).ObserveConfiguration(context.Background(), "nextcloud"); err == nil {
		t.Fatal("expected an unreadable root Application to be an error")
	}
}

// The overview assesses every capability on every request. Without a cache one
// page view would multiply into hundreds of API calls.
func TestLiveSourcesCacheOneObservationCycle(t *testing.T) {
	api := healthyCluster()
	sources := liveSources(api)
	clock := time.Now().UTC()
	sources.Clock = func() time.Time { return clock }

	for range 5 {
		if _, _, err := sources.ObserveDelivery(context.Background(), "nextcloud"); err != nil {
			t.Fatalf("observe: %v", err)
		}
		if _, _, err := sources.ObserveRuntime(context.Background(), "nextcloud"); err != nil {
			t.Fatalf("observe: %v", err)
		}
	}
	if reads := api.reads[argoPath("nextcloud")]; reads != 1 {
		t.Fatalf("argo reads = %d, want 1 within the cache window", reads)
	}

	clock = clock.Add(2 * defaultCacheTTL)
	if _, _, err := sources.ObserveDelivery(context.Background(), "nextcloud"); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if reads := api.reads[argoPath("nextcloud")]; reads != 2 {
		t.Fatalf("argo reads = %d, want a refresh after the cache window", reads)
	}
}

// A capability with no Ingress has no hostname or certificate to wait for, and
// must not be held degraded for the absence of either.
func TestLiveSourcesTreatIngresslessCapabilityAsAccessible(t *testing.T) {
	api := newFakeAPI()
	api.objects[argoPath("cloudnative-pg")] = map[string]any{
		"status": map[string]any{
			"sync":   map[string]any{"status": "Synced"},
			"health": map[string]any{"status": "Healthy"},
			"resources": []map[string]any{
				{"kind": "Deployment", "namespace": "cnpg-system", "name": "cnpg-controller"},
			},
		},
	}
	api.objects[kubeclient.NamespacedPath(kubeclient.AppsAPI, "cnpg-system", "deployments", "cnpg-controller")] = map[string]any{
		"metadata": map[string]any{"generation": 1},
		"spec":     map[string]any{"replicas": 1},
		"status":   map[string]any{"observedGeneration": 1, "readyReplicas": 1, "updatedReplicas": 1},
	}
	access, _, err := liveSources(api).ObserveAccess(context.Background(), "cloudnative-pg")
	if err != nil {
		t.Fatalf("observe access: %v", err)
	}
	if !access.DNSResolves || !access.CertificateReady {
		t.Fatalf("access = %+v", access)
	}
	if access.PublicReachable {
		t.Fatal("a capability without an Ingress is not publicly reachable")
	}
}
