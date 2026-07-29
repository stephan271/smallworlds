package observers

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/kubeclient"
)

// This file is the production half of the seam the rest of this package defines:
// readers that observe a live cluster through the Kubernetes API. The split
// stays strict — nothing here decides a Capability State. Each reader returns
// raw facts plus the time they were gathered, and the pure translators above
// turn those into evidence for the assessment engine.
//
// Two properties are deliberate. First, the runtime facet is read from
// Kubernetes directly rather than from Argo CD's own health roll-up: if runtime
// evidence were derived from the same Argo status as delivery evidence, the
// engine's central invariant — an Argo-Healthy delivery with a not-yet-ready
// workload never reads healthy — would be vacuous. Second, a read that fails
// returns an error, which becomes Missing (unknown) evidence, while an object
// that does not exist yet returns facts saying so. A console that cannot read
// must never look like a healthy one.

const (
	defaultArgoNamespace   = "argocd"
	defaultRootApplication = "smallworlds-root"
	// defaultGatewayEntrypoint is the Traefik entrypoint the Private Gateway
	// serves operator interfaces on (ADR 0012). An Ingress routed there is
	// reachable through the gateway; one routed on a public entrypoint is not
	// private, whatever DNS says.
	defaultGatewayEntrypoint = "private-gateway"
	// defaultCacheTTL bounds how often the cluster is re-read for one capability.
	// The overview assesses every capability on each request, so an uncached
	// reader would multiply one page view into hundreds of API calls; the cache
	// timestamp is also what the staleness rules judge.
	defaultCacheTTL = 20 * time.Second

	entrypointsAnnotation = "traefik.ingress.kubernetes.io/router.entrypoints"
	middlewaresAnnotation = "traefik.ingress.kubernetes.io/router.middlewares"
)

var defaultPublicEntrypoints = []string{"web", "websecure"}

// Reader is the subset of the Kubernetes client the live observers need.
// kubeclient.Client satisfies it; tests inject a fake API server.
type Reader interface {
	Get(ctx context.Context, path string, target any) error
}

// LiveSources observes a running SmallWorlds cluster. One value satisfies
// ConfigurationSource, DeliverySource, RuntimeSource, and AccessSource: all four
// facets come from the same snapshot of a capability's Argo CD Application and
// the Kubernetes objects it manages, so the four facets of one assessment
// describe the same moment rather than four drifting ones.
//
// Protection is not observed here — it has its own inventory
// (internal/protection), which the console composes into the assessment input.
type LiveSources struct {
	Reader Reader
	// ArgoNamespace holds the Argo CD Applications. Defaults to "argocd".
	ArgoNamespace string
	// RootApplication is the app-of-apps whose managed resources are the
	// authoritative list of what the GitOps Overlay declares.
	RootApplication string
	// Dependencies are the catalog's declared dependencies per capability. A
	// dependency whose own Application is absent or unhealthy is reported unmet.
	Dependencies map[string][]string
	// PublicEntrypoints name the Traefik entrypoints reachable from the internet.
	PublicEntrypoints []string
	// GatewayEntrypoint names the Private Gateway's entrypoint.
	GatewayEntrypoint string
	// LookupHost resolves a hostname. Defaults to the process resolver.
	LookupHost func(ctx context.Context, host string) ([]string, error)
	// CacheTTL bounds how long one capability's snapshot is reused.
	CacheTTL time.Duration
	Clock    func() time.Time

	mu        sync.Mutex
	snapshots map[string]*snapshot
	rootApps  map[string]bool
	rootAt    time.Time
}

// snapshot is one capability's observation cycle: everything read from the
// cluster for it, at one moment.
type snapshot struct {
	at            time.Time
	err           error
	applicationOK bool
	delivery      DeliveryFacts
	runtime       RuntimeFacts
	access        AccessFacts
	healthy       bool
}

func (sources *LiveSources) now() time.Time {
	if sources.Clock != nil {
		return sources.Clock()
	}
	return time.Now().UTC()
}

func (sources *LiveSources) argoNamespace() string {
	if sources.ArgoNamespace != "" {
		return sources.ArgoNamespace
	}
	return defaultArgoNamespace
}

func (sources *LiveSources) cacheTTL() time.Duration {
	if sources.CacheTTL > 0 {
		return sources.CacheTTL
	}
	return defaultCacheTTL
}

func (sources *LiveSources) lookupHost(ctx context.Context, host string) ([]string, error) {
	if sources.LookupHost != nil {
		return sources.LookupHost(ctx, host)
	}
	return net.DefaultResolver.LookupHost(ctx, host)
}

// ObserveDelivery reports the capability's Argo CD Application state.
func (sources *LiveSources) ObserveDelivery(ctx context.Context, capabilityID string) (DeliveryFacts, time.Time, error) {
	current := sources.snapshot(ctx, capabilityID)
	if current.err != nil {
		return DeliveryFacts{}, current.at, current.err
	}
	return current.delivery, current.at, nil
}

// ObserveRuntime reports the readiness of the Kubernetes objects the
// capability's Application manages.
func (sources *LiveSources) ObserveRuntime(ctx context.Context, capabilityID string) (RuntimeFacts, time.Time, error) {
	current := sources.snapshot(ctx, capabilityID)
	if current.err != nil {
		return RuntimeFacts{}, current.at, current.err
	}
	return current.runtime, current.at, nil
}

// ObserveAccess reports how the capability's Ingresses can actually be reached.
func (sources *LiveSources) ObserveAccess(ctx context.Context, capabilityID string) (AccessFacts, time.Time, error) {
	current := sources.snapshot(ctx, capabilityID)
	if current.err != nil {
		return AccessFacts{}, current.at, current.err
	}
	return current.access, current.at, nil
}

// ObserveConfiguration reports what the cluster can see of a capability's
// configuration: whether the GitOps Overlay declares it (the root Application's
// managed resources are the authority), whether its declared dependencies are
// satisfied, and whether Argo CD reports a specification or comparison error —
// the only in-cluster evidence that a required value is missing, since the
// overlay's values are never visible from inside the cluster.
func (sources *LiveSources) ObserveConfiguration(ctx context.Context, capabilityID string) (ConfigurationFacts, time.Time, error) {
	declared, at, err := sources.declaredApplications(ctx)
	if err != nil {
		return ConfigurationFacts{}, at, err
	}
	facts := ConfigurationFacts{
		Selected:          declared[capabilityID],
		DeclaredInGit:     declared[capabilityID],
		RequiredValuesMet: true,
	}
	current := sources.snapshot(ctx, capabilityID)
	if current.err == nil && current.applicationOK {
		facts.RequiredValuesMet = !current.delivery.specificationError
	}
	for _, dependency := range sources.Dependencies[capabilityID] {
		if !sources.dependencySatisfied(ctx, dependency) {
			facts.UnmetDependencies = append(facts.UnmetDependencies, dependency)
		}
	}
	sort.Strings(facts.UnmetDependencies)
	return facts, at, nil
}

// dependencySatisfied reports whether a declared dependency is actually serving.
// An unreadable dependency counts as unmet: the honest answer to "is what this
// needs in place?" when it cannot be seen is no, not yes.
func (sources *LiveSources) dependencySatisfied(ctx context.Context, dependency string) bool {
	current := sources.snapshot(ctx, dependency)
	return current.err == nil && current.applicationOK && current.healthy
}

// declaredApplications reads the root Application's managed resources — the
// child Applications the GitOps Overlay declares.
func (sources *LiveSources) declaredApplications(ctx context.Context) (map[string]bool, time.Time, error) {
	sources.mu.Lock()
	cached, at := sources.rootApps, sources.rootAt
	sources.mu.Unlock()
	if cached != nil && sources.now().Sub(at) < sources.cacheTTL() {
		return cached, at, nil
	}

	root := sources.RootApplication
	if root == "" {
		root = defaultRootApplication
	}
	var application argoApplication
	err := sources.Reader.Get(ctx, kubeclient.NamespacedPath(kubeclient.ArgoAPI, sources.argoNamespace(), "applications", root), &application)
	observedAt := sources.now()
	if err != nil {
		// A missing root Application is not "nothing is declared" — it is a
		// cluster whose delivery has not been established. Reporting an error
		// makes the configuration facet unknown rather than falsely empty.
		return nil, observedAt, err
	}
	declared := make(map[string]bool, len(application.Status.Resources))
	for _, resource := range application.Status.Resources {
		if resource.Kind == "Application" {
			declared[resource.Name] = true
		}
	}
	sources.mu.Lock()
	sources.rootApps, sources.rootAt = declared, observedAt
	sources.mu.Unlock()
	return declared, observedAt, nil
}

// snapshot returns a capability's cached observation, refreshing it when stale.
func (sources *LiveSources) snapshot(ctx context.Context, capabilityID string) *snapshot {
	sources.mu.Lock()
	if sources.snapshots == nil {
		sources.snapshots = map[string]*snapshot{}
	}
	cached, ok := sources.snapshots[capabilityID]
	sources.mu.Unlock()
	if ok && sources.now().Sub(cached.at) < sources.cacheTTL() {
		return cached
	}
	fresh := sources.observe(ctx, capabilityID)
	sources.mu.Lock()
	sources.snapshots[capabilityID] = fresh
	sources.mu.Unlock()
	return fresh
}

// observe performs one capability's read cycle: its Argo CD Application, then
// every Kubernetes object that Application manages.
func (sources *LiveSources) observe(ctx context.Context, capabilityID string) *snapshot {
	current := &snapshot{at: sources.now()}
	var application argoApplication
	err := sources.Reader.Get(ctx, kubeclient.NamespacedPath(kubeclient.ArgoAPI, sources.argoNamespace(), "applications", capabilityID), &application)
	switch {
	case errors.Is(err, kubeclient.ErrNotFound):
		// Declared but not reconciled yet. The translators read this as Pending.
		return current
	case err != nil:
		current.err = err
		return current
	}
	current.applicationOK = true
	current.delivery = application.deliveryFacts()
	current.healthy = current.delivery.SyncStatus == SyncSynced && current.delivery.HealthStatus == HealthHealthy
	current.runtime, current.access = sources.observeManagedResources(ctx, application.Status.Resources)
	return current
}

// observeManagedResources reads the workloads, claims, endpoints, and Ingresses
// of one Application straight from the Kubernetes API.
func (sources *LiveSources) observeManagedResources(ctx context.Context, resources []argoResource) (RuntimeFacts, AccessFacts) {
	runtime := RuntimeFacts{AllWorkloadsReady: true, AllPVCsBound: true, ProbesPassing: true}
	access := AccessFacts{}
	sawIngress, sawService := false, false

	for _, resource := range resources {
		switch resource.Kind {
		case "Deployment", "StatefulSet":
			runtime.WorkloadsFound = true
			ready, starting, err := sources.observeReplicatedWorkload(ctx, kubeclient.AppsAPI, strings.ToLower(resource.Kind)+"s", resource)
			if err != nil || !ready {
				runtime.AllWorkloadsReady = false
			}
			if starting {
				runtime.AnyWorkloadStarting = true
			}
		case "DaemonSet":
			runtime.WorkloadsFound = true
			ready, starting, err := sources.observeDaemonSet(ctx, resource)
			if err != nil || !ready {
				runtime.AllWorkloadsReady = false
			}
			if starting {
				runtime.AnyWorkloadStarting = true
			}
		case "Job":
			if failed := sources.observeJob(ctx, resource); failed {
				runtime.FailedJobs++
			}
		case "PersistentVolumeClaim":
			if !sources.observeClaimBound(ctx, resource) {
				runtime.AllPVCsBound = false
			}
		case "Service":
			sawService = true
			if !sources.observeEndpointsReady(ctx, resource) {
				runtime.ProbesPassing = false
			}
		case "Ingress":
			sawIngress = true
			sources.observeIngress(ctx, resource, &access)
		}
	}
	// Probe evidence comes from Service endpoints; with no Service there is
	// nothing probing, so the facet must not claim a passing probe it never saw.
	if !sawService {
		runtime.ProbesPassing = runtime.WorkloadsFound && runtime.AllWorkloadsReady
	}
	// A capability without an Ingress has no DNS name or certificate to be ready.
	// Reporting both as satisfied is correct for an internal capability, whose
	// access facet only fails when an Ingress appears where none should be.
	if !sawIngress {
		access.DNSResolves = true
		access.CertificateReady = true
	}
	return runtime, access
}

func (sources *LiveSources) observeReplicatedWorkload(ctx context.Context, apiRoot, plural string, resource argoResource) (ready, starting bool, err error) {
	var workload workloadObject
	if err := sources.Reader.Get(ctx, kubeclient.NamespacedPath(apiRoot, resource.Namespace, plural, resource.Name), &workload); err != nil {
		return false, false, err
	}
	desired := int32(1)
	if workload.Spec.Replicas != nil {
		desired = *workload.Spec.Replicas
	}
	// A scaled-to-zero workload is ready by its own declaration; there is nothing
	// left to become ready.
	if desired == 0 {
		return true, false, nil
	}
	// A status the controller has not caught up with describes the previous
	// generation, so it cannot vouch for the current one.
	settled := workload.Status.ObservedGeneration >= workload.Metadata.Generation
	ready = settled && workload.Status.ReadyReplicas >= desired && workload.Status.UpdatedReplicas >= desired
	starting = !ready
	return ready, starting, nil
}

func (sources *LiveSources) observeDaemonSet(ctx context.Context, resource argoResource) (ready, starting bool, err error) {
	var workload workloadObject
	if err := sources.Reader.Get(ctx, kubeclient.NamespacedPath(kubeclient.AppsAPI, resource.Namespace, "daemonsets", resource.Name), &workload); err != nil {
		return false, false, err
	}
	desired := workload.Status.DesiredNumberScheduled
	if desired == 0 {
		return true, false, nil
	}
	ready = workload.Status.NumberReady >= desired
	return ready, !ready, nil
}

// observeJob reports whether a Job has failed outright. A Job that is still
// running, or that failed a pod but has retries left, is not a failure yet —
// only an exhausted Job is, and that is what the Failed condition records.
func (sources *LiveSources) observeJob(ctx context.Context, resource argoResource) bool {
	var job workloadObject
	if err := sources.Reader.Get(ctx, kubeclient.NamespacedPath(kubeclient.BatchAPI, resource.Namespace, "jobs", resource.Name), &job); err != nil {
		return false
	}
	for _, condition := range job.Status.Conditions {
		if condition.Type == "Failed" && condition.Status == "True" {
			return true
		}
	}
	return false
}

func (sources *LiveSources) observeClaimBound(ctx context.Context, resource argoResource) bool {
	var claim claimObject
	if err := sources.Reader.Get(ctx, kubeclient.NamespacedPath(kubeclient.CoreAPI, resource.Namespace, "persistentvolumeclaims", resource.Name), &claim); err != nil {
		return false
	}
	return claim.Status.Phase == "Bound"
}

// observeEndpointsReady reports whether a Service has at least one ready
// endpoint. This is the console's probe evidence: a pod that starts but fails
// its readiness probe is removed from the endpoints, so an empty endpoint list
// is exactly "the probes are not passing".
func (sources *LiveSources) observeEndpointsReady(ctx context.Context, resource argoResource) bool {
	var endpoints endpointsObject
	if err := sources.Reader.Get(ctx, kubeclient.NamespacedPath(kubeclient.CoreAPI, resource.Namespace, "endpoints", resource.Name), &endpoints); err != nil {
		return false
	}
	for _, subset := range endpoints.Subsets {
		if len(subset.Addresses) > 0 {
			return true
		}
	}
	return false
}

// observeIngress folds one Ingress into the access facts: which entrypoint
// actually carries it, whether its host resolves, and whether cert-manager has
// issued its certificate.
func (sources *LiveSources) observeIngress(ctx context.Context, resource argoResource, access *AccessFacts) {
	var ingress ingressObject
	if err := sources.Reader.Get(ctx, kubeclient.NamespacedPath(kubeclient.NetworkingAPI, resource.Namespace, "ingresses", resource.Name), &ingress); err != nil {
		return
	}
	if sources.routesOnGateway(ingress) {
		access.GatewayReachable = true
	}
	if sources.routesOnPublicEntrypoint(ingress) {
		// A route on a public entrypoint is only actually public if nothing in
		// front of it turns public traffic away. Reading the router's own
		// middlewares is what tells the two apart — otherwise an operator
		// interface restricted to the Private Network would be reported as
		// exposed to the internet, which is the opposite of the truth.
		restriction := sources.sourceRestriction(ctx, resource.Namespace, ingress)
		access.PublicReachable = !restriction.deniesPublic
		if restriction.allowsPrivateNetwork {
			access.GatewayReachable = true
		}
	}
	for _, rule := range ingress.Spec.Rules {
		if rule.Host == "" {
			continue
		}
		if addresses, err := sources.lookupHost(ctx, rule.Host); err == nil && len(addresses) > 0 {
			access.DNSResolves = true
		}
	}
	for _, tls := range ingress.Spec.TLS {
		if tls.SecretName == "" {
			continue
		}
		if sources.certificateReady(ctx, resource.Namespace, tls.SecretName) {
			access.CertificateReady = true
		}
	}
	// An Ingress serving plain HTTP has no certificate to wait for.
	if len(ingress.Spec.TLS) == 0 {
		access.CertificateReady = true
	}
}

// routesOnGateway reports whether the Ingress is pinned to the Private Gateway's
// entrypoint.
func (sources *LiveSources) routesOnGateway(ingress ingressObject) bool {
	gateway := sources.GatewayEntrypoint
	if gateway == "" {
		gateway = defaultGatewayEntrypoint
	}
	return containsEntrypoint(ingress.Metadata.Annotations[entrypointsAnnotation], gateway)
}

// routesOnPublicEntrypoint reports whether the Ingress is carried by an
// entrypoint reachable from the internet. An Ingress with no entrypoint
// annotation is served on every entrypoint the router has, which includes the
// public ones — so the absence of the annotation is itself public exposure, not
// the absence of evidence.
func (sources *LiveSources) routesOnPublicEntrypoint(ingress ingressObject) bool {
	declared := ingress.Metadata.Annotations[entrypointsAnnotation]
	if strings.TrimSpace(declared) == "" {
		return true
	}
	entrypoints := sources.PublicEntrypoints
	if len(entrypoints) == 0 {
		entrypoints = defaultPublicEntrypoints
	}
	for _, entrypoint := range entrypoints {
		if containsEntrypoint(declared, entrypoint) {
			return true
		}
	}
	return false
}

// restriction summarizes what a router's middlewares do to source addresses.
type restriction struct {
	// deniesPublic is true when every allowed source range is off the public
	// internet, so traffic from it cannot reach the service at all.
	deniesPublic bool
	// allowsPrivateNetwork is true when the Private Network's own address range
	// is among the allowed sources — the route is reachable by an enrolled
	// Operator Device.
	allowsPrivateNetwork bool
}

// sourceRestriction reads the Traefik middlewares a router references and
// reports what they do to source addresses. Only an allow-list of source ranges
// is interpreted; any middleware this cannot resolve leaves the route counted as
// public, because the conservative answer to "is this exposed?" is yes.
func (sources *LiveSources) sourceRestriction(ctx context.Context, namespace string, ingress ingressObject) restriction {
	names := ingress.Metadata.Annotations[middlewaresAnnotation]
	if strings.TrimSpace(names) == "" {
		return restriction{}
	}
	result := restriction{}
	for _, reference := range strings.Split(names, ",") {
		name, ok := middlewareName(namespace, reference)
		if !ok {
			continue
		}
		var middleware middlewareObject
		if err := sources.Reader.Get(ctx, kubeclient.NamespacedPath(kubeclient.TraefikAPI, namespace, "middlewares", name), &middleware); err != nil {
			continue
		}
		ranges := middleware.Spec.IPAllowList.SourceRange
		if len(ranges) == 0 {
			// Traefik v2 spelled the same middleware ipWhiteList. A cluster part
			// way through an upgrade can carry either.
			ranges = middleware.Spec.IPWhiteList.SourceRange
		}
		if len(ranges) == 0 {
			continue
		}
		offPublicInternet := true
		for _, sourceRange := range ranges {
			prefix, err := netip.ParsePrefix(strings.TrimSpace(sourceRange))
			if err != nil {
				offPublicInternet = false
				continue
			}
			if privateNetworkRange.Overlaps(prefix) {
				result.allowsPrivateNetwork = true
			}
			if !isOffPublicInternet(prefix) {
				offPublicInternet = false
			}
		}
		if offPublicInternet {
			result.deniesPublic = true
		}
	}
	return result
}

// middlewareName resolves one entry of the router.middlewares annotation to a
// Middleware name in the given namespace. Traefik writes them as
// "<namespace>-<name>@kubernetescrd"; an entry that does not belong to this
// namespace is left unresolved rather than guessed at.
func middlewareName(namespace, reference string) (string, bool) {
	reference = strings.TrimSpace(reference)
	if at := strings.IndexByte(reference, '@'); at >= 0 {
		reference = reference[:at]
	}
	if reference == "" {
		return "", false
	}
	if name, found := strings.CutPrefix(reference, namespace+"-"); found && name != "" {
		return name, true
	}
	if !strings.Contains(reference, "-") {
		return reference, true
	}
	return "", false
}

// privateNetworkRange is the carrier-grade NAT range Headscale assigns to
// enrolled Operator Devices. A route allowing it is reachable on the Private
// Network.
var privateNetworkRange = netip.MustParsePrefix("100.64.0.0/10")

// offPublicInternetRanges are the address ranges that carry no traffic from the
// public internet.
var offPublicInternetRanges = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	privateNetworkRange,
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
}

func isOffPublicInternet(prefix netip.Prefix) bool {
	for _, known := range offPublicInternetRanges {
		if known.Contains(prefix.Addr()) && prefix.Bits() >= known.Bits() {
			return true
		}
	}
	return false
}

func containsEntrypoint(declared, wanted string) bool {
	for _, entry := range strings.Split(declared, ",") {
		if strings.EqualFold(strings.TrimSpace(entry), wanted) {
			return true
		}
	}
	return false
}

// certificateReady reads the cert-manager Certificate that issues a TLS secret
// and reports its Ready condition. The console reads the Certificate rather than
// the Secret so its ServiceAccount never needs permission to read secret
// material to answer "is TLS ready?".
func (sources *LiveSources) certificateReady(ctx context.Context, namespace, secretName string) bool {
	var list certificateList
	if err := sources.Reader.Get(ctx, kubeclient.NamespacedPath(kubeclient.CertManagerAPI, namespace, "certificates", ""), &list); err != nil {
		return false
	}
	for _, certificate := range list.Items {
		if certificate.Spec.SecretName != secretName {
			continue
		}
		for _, condition := range certificate.Status.Conditions {
			if condition.Type == "Ready" && condition.Status == "True" {
				return true
			}
		}
	}
	return false
}

// --- Kubernetes wire shapes ---
//
// Only the fields the console reads are declared. Keeping them minimal is what
// lets this package track the API without a generated client.

type argoResource struct {
	Group     string `json:"group"`
	Version   string `json:"version"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Health    struct {
		Status string `json:"status"`
	} `json:"health"`
}

type argoApplication struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Status struct {
		Sync struct {
			Status string `json:"status"`
		} `json:"sync"`
		Health struct {
			Status string `json:"status"`
		} `json:"health"`
		OperationState struct {
			Phase      string    `json:"phase"`
			FinishedAt time.Time `json:"finishedAt"`
		} `json:"operationState"`
		ReconciledAt time.Time `json:"reconciledAt"`
		Conditions   []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"conditions"`
		Resources []argoResource `json:"resources"`
	} `json:"status"`
}

func (application argoApplication) deliveryFacts() DeliveryFacts {
	facts := DeliveryFacts{
		ApplicationFound: true,
		SyncStatus:       application.Status.Sync.Status,
		HealthStatus:     application.Status.Health.Status,
		OperationPhase:   application.Status.OperationState.Phase,
		LastReconciledAt: application.Status.ReconciledAt,
	}
	for _, condition := range application.Status.Conditions {
		// Argo raises these when the Application's source cannot be rendered —
		// a missing value, an unreachable ref, an invalid spec. That is the only
		// evidence inside the cluster that the overlay is incomplete.
		if strings.Contains(condition.Type, "Error") {
			facts.specificationError = true
		}
	}
	return facts
}

type workloadObject struct {
	Metadata struct {
		Generation int64 `json:"generation"`
	} `json:"metadata"`
	Spec struct {
		Replicas *int32 `json:"replicas"`
	} `json:"spec"`
	Status struct {
		ObservedGeneration     int64 `json:"observedGeneration"`
		ReadyReplicas          int32 `json:"readyReplicas"`
		UpdatedReplicas        int32 `json:"updatedReplicas"`
		DesiredNumberScheduled int32 `json:"desiredNumberScheduled"`
		NumberReady            int32 `json:"numberReady"`
		Conditions             []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
	} `json:"status"`
}

type claimObject struct {
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
}

type endpointsObject struct {
	Subsets []struct {
		Addresses []struct {
			IP string `json:"ip"`
		} `json:"addresses"`
	} `json:"subsets"`
}

type ingressObject struct {
	Metadata struct {
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		IngressClassName string `json:"ingressClassName"`
		Rules            []struct {
			Host string `json:"host"`
		} `json:"rules"`
		TLS []struct {
			SecretName string `json:"secretName"`
		} `json:"tls"`
	} `json:"spec"`
}

// middlewareObject is the subset of a Traefik Middleware that decides whether
// public traffic can reach a router at all.
type middlewareObject struct {
	Spec struct {
		IPAllowList struct {
			SourceRange []string `json:"sourceRange"`
		} `json:"ipAllowList"`
		IPWhiteList struct {
			SourceRange []string `json:"sourceRange"`
		} `json:"ipWhiteList"`
	} `json:"spec"`
}

type certificateList struct {
	Items []struct {
		Spec struct {
			SecretName string `json:"secretName"`
		} `json:"spec"`
		Status struct {
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	} `json:"items"`
}
