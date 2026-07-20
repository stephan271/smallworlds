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
	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
	"github.com/stephan271/smallworlds/operator-console/internal/localbootstrap"
	"github.com/stephan271/smallworlds/operator-console/internal/nodeinspect"
)

type readyNodeInspector struct{ calls int }

func (inspector *readyNodeInspector) InspectSameHost(profileID string, requirements nodeinspect.Requirements) (nodeinspect.Report, nodeinspect.Assessment, error) {
	inspector.calls++
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

func TestLocalBootstrapPlanReinspectsBindsAndExecutesWithoutSecretLeakage(t *testing.T) {
	contents := []byte("verified bootstrap archive")
	digest := sha256.Sum256(contents)
	digestText := fmt.Sprintf("%x", digest[:])
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := bootstrapassets.Descriptor{ID: "bootstrap-linux-amd64", Release: "v1.2.26", URL: "https://assets.example.invalid/bootstrap.tar.gz", SHA256: digestText, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(digestText))), PublicKey: publicKey, Destination: "assets.example.invalid"}
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
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "bootstrap-plan", BootstrapAssets: assets, GenericGitClient: git, NodeInspector: inspector, LocalBootstrapRunner: runner})
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
	planBody, _ := json.Marshal(map[string]any{"profileId": profile.ID, "target": map[string]any{"kind": "same-host"}, "authentication": map[string]any{"kind": "agent"}, "release": descriptor.Release, "configuration": map[string]any{"domain": "home.example", "environmentExtension": ".dev", "dataDirectory": "/var/lib/smallworlds-data", "nodeName": "home-node", "manageDns": false}, "secretsManifest": "apiVersion: v1\nkind: Secret\ndata:\n  token: cluster-secret-value\n"})
	response = request(t, handler, http.MethodPost, "/api/v1/local-bootstrap/plan", planBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap plan status = %d: %s", response.StatusCode, readAll(t, response))
	}
	planResponse := readAll(t, response)
	if bytes.Contains(planResponse, []byte("cluster-secret-value")) || !bytes.Contains(planResponse, []byte(`"bootstrapRelease":"v1.2.26"`)) || !bytes.Contains(planResponse, []byte(`"overlayCommit":"`+strings.Repeat("c", 40)+`"`)) || !bytes.Contains(planResponse, []byte(`"dataDirectory":"/var/lib/smallworlds-data"`)) || !bytes.Contains(planResponse, []byte(`"code":"node.services.may_restart"`)) {
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
	response = request(t, handler, http.MethodGet, "/api/v1/events?profileId="+profile.ID, nil, cookie, nil)
	if body := readAll(t, response); bytes.Contains(body, []byte("cluster-secret-value")) || bytes.Contains(body, []byte("git-secret")) {
		t.Fatalf("activity leaked secrets: %s", body)
	}
}
