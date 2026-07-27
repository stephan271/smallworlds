package launcher_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/bootstrapassets"
	"github.com/stephan271/smallworlds/operator-console/internal/hetzner"
	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
	"github.com/stephan271/smallworlds/operator-console/internal/localbootstrap"
	"github.com/stephan271/smallworlds/operator-console/internal/nodeinspect"
)

type readyNodeInspector struct {
	calls         int
	dataDirectory string
}

func (inspector *readyNodeInspector) InspectSameHost(profileID string, requirements nodeinspect.Requirements) (nodeinspect.Report, nodeinspect.Assessment, error) {
	inspector.calls++
	inspector.dataDirectory = requirements.DataDirectory
	report := nodeinspect.Report{NodeIdentity: nodeinspect.HashNodeIdentity("test-machine"), OperatingSystem: "linux", Architecture: "amd64", Systemd: true, Capacity: nodeinspect.Capacity{CPUCores: 8, MemoryMi: requirements.MemoryMi + 1024, DiskGi: requirements.DiskGi + 100}, KernelReady: true, Privilege: "sudo", Installation: nodeinspect.Installation{Kubernetes: nodeinspect.Absent, SmallWorldsData: nodeinspect.Absent}}
	return report, nodeinspect.Assess(report, requirements), nil
}

func (inspector *readyNodeInspector) InspectRemote(context.Context, nodeinspect.Target, nodeinspect.Credentials, string, string, nodeinspect.Requirements) (nodeinspect.Report, nodeinspect.Assessment, error) {
	return nodeinspect.Report{}, nodeinspect.Assessment{}, fmt.Errorf("unexpected remote inspection")
}

type successfulBootstrapRunner struct{ calls int }

func (runner *successfulBootstrapRunner) Run(_ context.Context, request localbootstrap.RunRequest) (localbootstrap.Observation, error) {
	runner.calls++
	if _, err := io.ReadAll(request.Archive); err != nil {
		return localbootstrap.Observation{}, err
	}
	if !strings.Contains(request.Secrets, "cluster-secret-value") {
		return localbootstrap.Observation{}, fmt.Errorf("missing cluster secrets")
	}
	return localbootstrap.Observation{CommandCompleted: true, K3SReady: true, ArgoCDReady: true, OverlaySynced: true, ObservedAt: time.Now().UTC()}, nil
}

func (runner *successfulBootstrapRunner) Observe(_ context.Context, _ localbootstrap.RunRequest) (localbootstrap.Observation, error) {
	return localbootstrap.Observation{CommandCompleted: true, K3SReady: true, ArgoCDReady: true, OverlaySynced: true, ObservedAt: time.Now().UTC()}, nil
}

func (runner *successfulBootstrapRunner) Detail(_ context.Context, _ localbootstrap.RunRequest) (localbootstrap.Detail, error) {
	return localbootstrap.Detail{
		Nodes:        []localbootstrap.NodeCondition{{Name: "smallworlds-local-node", Ready: true, Version: "v1.36.2+k3s1"}},
		Applications: []localbootstrap.ApplicationCondition{{Name: "smallworlds-root", Sync: "Synced", Health: "Degraded"}},
		Workloads:    []localbootstrap.WorkloadCondition{{Namespace: "keycloak", Name: "keycloak-keycloakx-0", Phase: "Pending", Ready: "0/1", Reason: "CreateContainerConfigError", Message: `secret "keycloak-admin-creds" not found`}},
	}, nil
}

func TestLocalBootstrapPlanReinspectsBindsAndExecutesWithoutSecretLeakage(t *testing.T) {
	contents := []byte("verified bootstrap archive")
	digest := sha256.Sum256(contents)
	digestText := fmt.Sprintf("%x", digest[:])
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := bootstrapassets.Descriptor{ID: "bootstrap-linux-amd64", Release: "v1.2.27", URL: "https://assets.example.invalid/bootstrap.tar.gz", SHA256: digestText, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(digestText))), PublicKey: publicKey, Destination: "assets.example.invalid"}
	assets, err := bootstrapassets.NewManager(t.TempDir(), bootstrapassets.Catalog{Descriptors: []bootstrapassets.Descriptor{descriptor}}, assetFetcherStub{contents: contents})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := assets.Acquire(context.Background(), descriptor.Release); err != nil {
		t.Fatal(err)
	}
	git := &genericGitStub{commit: strings.Repeat("c", 40)}
	inspector := &readyNodeInspector{}
	runner := &successfulBootstrapRunner{}
	dnsProvider := &fakeHetznerProvider{nameservers: append([]string(nil), hetzner.HetznerNameservers...)}
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "bootstrap-plan", BootstrapAssets: assets, GenericGitClient: git, NodeInspector: inspector, LocalBootstrapRunner: runner, HetznerProvider: dnsProvider})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "bootstrap-plan")
	profile := createProfile(t, handler, cookie, csrf, "Home", "en", "local-lan")
	unlockVaultForRecoveryTest(t, handler, cookie, csrf)
	credentials, _ := json.Marshal(map[string]string{"profileId": profile.ID, "repositoryUrl": "https://git.example/overlay.git", "username": "operator", "token": "git-secret"})
	response := request(t, handler, http.MethodPost, "/api/v1/generic-git/token/validate", credentials, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("git credential status = %d", response.StatusCode)
	}
	response.Body.Close()
	capabilityPlanBody, _ := json.Marshal(map[string]any{"profileId": profile.ID, "mode": "minimal", "communityIds": []string{}, "release": descriptor.Release, "repositoryUrl": "https://git.example/overlay.git", "domain": "home.example"})
	response = request(t, handler, http.MethodPost, "/api/v1/capabilities/plan", capabilityPlanBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	var capabilityPlan struct {
		Plan struct {
			ID string `json:"id"`
		} `json:"plan"`
	}
	if err := json.NewDecoder(response.Body).Decode(&capabilityPlan); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	response = request(t, handler, http.MethodPost, "/api/v1/plans/"+capabilityPlan.Plan.ID+"/approve", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
	response.Body.Close()
	establishBody, _ := json.Marshal(map[string]any{"profileId": profile.ID, "planId": capabilityPlan.Plan.ID, "repositoryUrl": "https://git.example/overlay.git", "mode": "minimal", "communityIds": []string{}, "release": descriptor.Release, "domain": "home.example"})
	response = request(t, handler, http.MethodPost, "/api/v1/generic-git/overlay/establish", establishBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("overlay status = %d: %s", response.StatusCode, readAll(t, response))
	}
	response.Body.Close()
	planBody, _ := json.Marshal(map[string]any{"profileId": profile.ID, "target": map[string]any{"kind": "same-host"}, "authentication": map[string]any{"kind": "agent"}, "release": descriptor.Release, "configuration": map[string]any{"domain": "home.example", "environmentExtension": ".dev", "dataDirectory": "/data/smallworlds-acceptance", "nodeName": "home-node", "manageDns": false}, "secretsManifest": "apiVersion: v1\nkind: Secret\ndata:\n  token: cluster-secret-value\n"})
	response = request(t, handler, http.MethodPost, "/api/v1/local-bootstrap/plan", planBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap plan status = %d: %s", response.StatusCode, readAll(t, response))
	}
	planResponse := readAll(t, response)
	if bytes.Contains(planResponse, []byte("cluster-secret-value")) || !bytes.Contains(planResponse, []byte(`"bootstrapRelease":"v1.2.27"`)) || !bytes.Contains(planResponse, []byte(`"overlayCommit":"`+strings.Repeat("c", 40)+`"`)) || !bytes.Contains(planResponse, []byte(`"dataDirectory":"/data/smallworlds-acceptance"`)) || !bytes.Contains(planResponse, []byte(`"code":"node.services.may_restart"`)) {
		t.Fatalf("unsafe or incomplete plan: %s", planResponse)
	}
	var planned struct {
		Plan struct {
			ID string `json:"id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(planResponse, &planned); err != nil {
		t.Fatal(err)
	}
	if inspector.calls != 1 {
		t.Fatalf("fresh inspection calls = %d", inspector.calls)
	}
	if inspector.dataDirectory != "/data/smallworlds-acceptance" {
		t.Fatalf("inspected data directory = %q", inspector.dataDirectory)
	}
	response = request(t, handler, http.MethodPost, "/api/v1/plans/"+planned.Plan.ID+"/approve", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("approval status = %d", response.StatusCode)
	}
	var run struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&run); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		response = request(t, handler, http.MethodGet, "/api/v1/runs/"+run.ID, nil, cookie, nil)
		body := readAll(t, response)
		if bytes.Contains(body, []byte(`"state":"verified"`)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not verify: %s", body)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if runner.calls != 1 {
		t.Fatalf("runner calls = %d", runner.calls)
	}

	publicProfile := createProfile(t, handler, cookie, csrf, "Public home", "en", "local-public")
	publicGitCredentials, _ := json.Marshal(map[string]string{"profileId": publicProfile.ID, "repositoryUrl": "https://git.example/public-overlay.git", "username": "operator", "token": "public-git-secret"})
	response = request(t, handler, http.MethodPost, "/api/v1/generic-git/token/validate", publicGitCredentials, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("public git credential status = %d", response.StatusCode)
	}
	response.Body.Close()
	publicCapabilityBody, _ := json.Marshal(map[string]any{"profileId": publicProfile.ID, "mode": "minimal", "communityIds": []string{}, "release": descriptor.Release, "repositoryUrl": "https://git.example/public-overlay.git", "domain": "public.example"})
	response = request(t, handler, http.MethodPost, "/api/v1/capabilities/plan", publicCapabilityBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	var publicCapabilityPlan struct {
		Plan struct {
			ID string `json:"id"`
		} `json:"plan"`
	}
	if err := json.NewDecoder(response.Body).Decode(&publicCapabilityPlan); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	response = request(t, handler, http.MethodPost, "/api/v1/plans/"+publicCapabilityPlan.Plan.ID+"/approve", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
	response.Body.Close()
	publicEstablishBody, _ := json.Marshal(map[string]any{"profileId": publicProfile.ID, "planId": publicCapabilityPlan.Plan.ID, "repositoryUrl": "https://git.example/public-overlay.git", "mode": "minimal", "communityIds": []string{}, "release": descriptor.Release, "domain": "public.example"})
	response = request(t, handler, http.MethodPost, "/api/v1/generic-git/overlay/establish", publicEstablishBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("public overlay status = %d: %s", response.StatusCode, readAll(t, response))
	}
	response.Body.Close()
	publicDNSSecret := "dns-token-secret-value"
	// A cluster whose Secrets were never supplied installs perfectly and then
	// sits Degraded forever, because Keycloak, Garage and Grafana each mount one
	// this manifest is the only source of. The plan is refused instead.
	secretlessBody, _ := json.Marshal(map[string]any{
		"profileId": publicProfile.ID, "target": map[string]any{"kind": "same-host"}, "authentication": map[string]any{"kind": "agent"}, "release": descriptor.Release,
		"configuration":  map[string]any{"domain": "public.example", "dataDirectory": "/data/public", "nodeName": "public-node", "acmeEmail": "operator@public.example", "manageDns": false},
		"publicExposure": map[string]any{"dns01Provider": "hetzner", "dnsZone": "public.example", "dnsToken": "secretless-token", "publicIpBehavior": "dynamic-ddns", "routerAcknowledged": true},
	})
	response = request(t, handler, http.MethodPost, "/api/v1/local-bootstrap/plan", secretlessBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusConflict || !bytes.Contains(readAll(t, response), []byte("cluster_secrets_required")) {
		t.Fatal("a bootstrap plan without Cluster Secrets was not refused")
	}
	unacknowledgedBody, _ := json.Marshal(map[string]any{
		"profileId": publicProfile.ID, "target": map[string]any{"kind": "same-host"}, "authentication": map[string]any{"kind": "agent"}, "release": descriptor.Release,
		"configuration":   map[string]any{"domain": "public.example", "dataDirectory": "/data/public", "nodeName": "public-node", "acmeEmail": "operator@public.example", "manageDns": false},
		"publicExposure":  map[string]any{"dns01Provider": "hetzner", "dnsZone": "public.example", "dnsToken": "unacknowledged-secret", "publicIpBehavior": "dynamic-ddns", "routerAcknowledged": false},
		"secretsManifest": "apiVersion: v1\nkind: Secret\ndata:\n  token: cluster-secret-value\n",
	})
	response = request(t, handler, http.MethodPost, "/api/v1/local-bootstrap/plan", unacknowledgedBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusConflict || !bytes.Contains(readAll(t, response), []byte("router_forwarding_acknowledgement_required")) || dnsProvider.seenToken == "unacknowledged-secret" {
		t.Fatal("unacknowledged router rules were not rejected before provider access")
	}
	publicPlanBody, _ := json.Marshal(map[string]any{
		"profileId": publicProfile.ID, "target": map[string]any{"kind": "same-host"}, "authentication": map[string]any{"kind": "agent"}, "release": descriptor.Release,
		"configuration":   map[string]any{"domain": "public.example", "dataDirectory": "/data/public", "nodeName": "public-node", "acmeEmail": "operator@public.example", "manageDns": false},
		"publicExposure":  map[string]any{"dns01Provider": "hetzner", "dnsZone": "public.example", "dnsToken": publicDNSSecret, "publicIpBehavior": "dynamic-ddns", "routerAcknowledged": true},
		"secretsManifest": "apiVersion: v1\nkind: Secret\ndata:\n  token: cluster-secret-value\n",
	})
	response = request(t, handler, http.MethodPost, "/api/v1/local-bootstrap/plan", publicPlanBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("public bootstrap plan status = %d: %s", response.StatusCode, readAll(t, response))
	}
	publicPlanResponse := readAll(t, response)
	for _, expected := range []string{`"externalPort":80`, `"externalPort":443`, `"externalPort":10000`, `"automaticConfiguration":false`, `"dedicatedVerification":false`, `"code":"headscale.public_coordination.enabled"`} {
		if !bytes.Contains(publicPlanResponse, []byte(expected)) {
			t.Fatalf("public plan missing %s: %s", expected, publicPlanResponse)
		}
	}
	if bytes.Contains(publicPlanResponse, []byte(publicDNSSecret)) {
		t.Fatalf("public plan leaked DNS token: %s", publicPlanResponse)
	}
	if dnsProvider.seenToken != publicDNSSecret {
		t.Fatal("DNS provider did not receive the write-only token")
	}

	// What the cluster is doing, in the cluster's own words. A run parked at
	// awaiting-convergence says something is unfinished and nothing about what,
	// so this has to be readable while that run is still in flight.
	response = request(t, handler, http.MethodGet, "/api/v1/cluster-detail?profileId="+publicProfile.ID, nil, cookie, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("cluster detail status = %d: %s", response.StatusCode, readAll(t, response))
	}
	detailBody := readAll(t, response)
	for _, expected := range []string{`"smallworlds-root"`, `"Degraded"`, `"keycloak-keycloakx-0"`, `secret \"keycloak-admin-creds\" not found`} {
		if !bytes.Contains(detailBody, []byte(expected)) {
			t.Fatalf("cluster detail missing %s: %s", expected, detailBody)
		}
	}
	// A profile that never installed anything has no cluster to describe, and
	// says so rather than reporting an empty healthy one.
	freshProfile := createProfile(t, handler, cookie, csrf, "Never installed", "en", "local-lan")
	response = request(t, handler, http.MethodGet, "/api/v1/cluster-detail?profileId="+freshProfile.ID, nil, cookie, nil)
	if response.StatusCode != http.StatusConflict || !bytes.Contains(readAll(t, response), []byte("cluster_not_installed")) {
		t.Fatal("cluster detail for an uninstalled profile was not refused")
	}

	response = request(t, handler, http.MethodGet, "/api/v1/events?profileId="+profile.ID, nil, cookie, nil)
	if body := readAll(t, response); bytes.Contains(body, []byte("cluster-secret-value")) || bytes.Contains(body, []byte("git-secret")) {
		t.Fatalf("activity leaked secrets: %s", body)
	}
}
