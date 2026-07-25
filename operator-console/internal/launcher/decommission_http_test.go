package launcher_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/decommission"
	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
	"github.com/stephan271/smallworlds/operator-console/internal/state"
)

type preserveInspector struct {
	mu        sync.Mutex
	calls     int
	unknown   bool
	resources []decommission.Resource
}

func (inspector *preserveInspector) Inspect(_ context.Context, profile state.Profile) (decommission.Inspection, error) {
	inspector.mu.Lock()
	defer inspector.mu.Unlock()
	inspector.calls++
	resources := append([]decommission.Resource(nil), inspector.resources...)
	if inspector.unknown {
		resources = append(resources, decommission.Resource{Kind: "firewall", ProviderID: "unknown-firewall", Name: "smallworlds-ish"})
	}
	classified, err := decommission.Classify(profile.ID, resources)
	if err != nil {
		return decommission.Inspection{}, err
	}
	return decommission.Inspection{ProfileID: profile.ID, DeploymentMode: profile.DeploymentMode, ProfileRevision: profile.Revision, ObservedAt: time.Now().UTC(), Resources: classified, RetainedData: []string{"volume-data"}, RecoveryPath: "Use the retained volume or Recovery Bundle.", Protection: []string{"offsite recovery point current"}}, nil
}

type preserveExecutor struct {
	mu       sync.Mutex
	removed  []string
	failOnce bool
}

func (executor *preserveExecutor) Remove(_ context.Context, _ string, item decommission.Item) error {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.failOnce {
		executor.failOnce = false
		return errors.New("simulated interruption")
	}
	executor.removed = append(executor.removed, item.ProviderID)
	return nil
}

func TestPreserveDataDecommissionInspectsPlansExecutesAndResumesNarrowly(t *testing.T) {
	inspector := &preserveInspector{resources: []decommission.Resource{
		{Kind: "server", ProviderID: "server-1", Tags: map[string]string{"smallworlds-profile": "placeholder"}},
		{Kind: "volume", ProviderID: "volume-data", Persistent: true, MonthlyEUR: 4.4},
		{Kind: "primary-ip", ProviderID: "ip-1", Tags: map[string]string{"smallworlds-profile": "placeholder"}, MonthlyEUR: .60},
		{Kind: "dns-zone", ProviderID: "zone-1", Shared: true},
		{Kind: "gitops-overlay", ProviderID: "overlay-1"},
	}}
	executor := &preserveExecutor{failOnce: true}
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "preserve-data", PreserveDecommissionInspector: inspector, PreserveDecommissionExecutor: executor})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "preserve-data")
	profile := createProfile(t, handler, cookie, csrf, "Retire", "en", "hetzner")
	// The test adapter learns the real opaque profile identity only after it is created.
	inspector.mu.Lock()
	for index := range inspector.resources {
		if inspector.resources[index].Kind == "server" || inspector.resources[index].Kind == "primary-ip" {
			inspector.resources[index].Tags = map[string]string{"smallworlds-profile": profile.ID}
			inspector.resources[index].ClusterIdentity = profile.ID
		}
	}
	inspector.mu.Unlock()

	body, _ := json.Marshal(map[string]string{"profileId": profile.ID})
	response := request(t, handler, http.MethodPost, "/api/v1/decommission/plan", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("plan = %d: %s", response.StatusCode, readAll(t, response))
	}
	var planned struct {
		Plan struct {
			ID string `json:"id"`
		} `json:"plan"`
		Decommission decommission.Plan `json:"decommission"`
		Approvable   bool              `json:"approvable"`
	}
	if err := json.NewDecoder(response.Body).Decode(&planned); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if !planned.Approvable || planned.Plan.ID == "" || len(planned.Decommission.ContinuingCosts) != 2 {
		t.Fatalf("plan = %+v", planned)
	}
	for _, item := range planned.Decommission.Items {
		if item.ProviderID == "volume-data" || item.ProviderID == "ip-1" || item.ProviderID == "zone-1" || item.ProviderID == "overlay-1" {
			if item.Action != decommission.Retain {
				t.Fatalf("%s action = %s, want retain", item.ProviderID, item.Action)
			}
		}
	}
	response = request(t, handler, http.MethodPost, "/api/v1/plans/"+planned.Plan.ID+"/approve", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("approve = %d: %s", response.StatusCode, readAll(t, response))
	}
	var run struct {
		ID string `json:"id"`
	}
	json.NewDecoder(response.Body).Decode(&run)
	response.Body.Close()
	waitFor(t, 2*time.Second, func() bool {
		response := request(t, handler, http.MethodGet, "/api/v1/runs/"+run.ID, nil, cookie, nil)
		defer response.Body.Close()
		var current struct {
			CurrentCheckpoint string `json:"currentCheckpoint"`
		}
		json.NewDecoder(response.Body).Decode(&current)
		return current.CurrentCheckpoint == "interrupted"
	})
	response = request(t, handler, http.MethodPost, "/api/v1/decommission/runs/"+run.ID+"/resume", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("resume = %d: %s", response.StatusCode, readAll(t, response))
	}
	waitFor(t, 2*time.Second, func() bool {
		response := request(t, handler, http.MethodGet, "/api/v1/runs/"+run.ID, nil, cookie, nil)
		defer response.Body.Close()
		var current struct {
			State string `json:"state"`
		}
		json.NewDecoder(response.Body).Decode(&current)
		return current.State == "verified"
	})
	executor.mu.Lock()
	removed := append([]string(nil), executor.removed...)
	executor.mu.Unlock()
	if len(removed) != 1 || removed[0] != "server-1" {
		t.Fatalf("removed = %v, want only profile-owned compute", removed)
	}
	inspector.mu.Lock()
	calls := inspector.calls
	inspector.mu.Unlock()
	if calls < 3 {
		t.Fatalf("inspection calls = %d, want plan + execution + resume", calls)
	}
}

func TestPreserveDataUnknownBlocksApprovalAndForgetIsLocalOnly(t *testing.T) {
	inspector := &preserveInspector{unknown: true, resources: []decommission.Resource{{Kind: "server", ProviderID: "server-1"}}}
	executor := &preserveExecutor{}
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "preserve-unknown", PreserveDecommissionInspector: inspector, PreserveDecommissionExecutor: executor})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "preserve-unknown")
	profile := createProfile(t, handler, cookie, csrf, "Unknown", "en", "local-lan")
	body, _ := json.Marshal(map[string]string{"profileId": profile.ID})
	response := request(t, handler, http.MethodPost, "/api/v1/decommission/plan", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	var planned struct {
		Plan struct {
			ID string `json:"id"`
		} `json:"plan"`
		Approvable bool `json:"approvable"`
	}
	json.NewDecoder(response.Body).Decode(&planned)
	response.Body.Close()
	if planned.Approvable {
		t.Fatal("unknown resource made plan approvable")
	}
	response = request(t, handler, http.MethodPost, "/api/v1/plans/"+planned.Plan.ID+"/approve", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("unknown approval = %d", response.StatusCode)
	}
	response.Body.Close()
	executor.mu.Lock()
	calls := len(executor.removed)
	executor.mu.Unlock()
	if calls != 0 {
		t.Fatalf("unknown plan executed: %v", calls)
	}
	forget, _ := json.Marshal(map[string]string{"confirmProfileId": profile.ID})
	response = request(t, handler, http.MethodPost, "/api/v1/profiles/"+profile.ID+"/forget", forget, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("forget = %d: %s", response.StatusCode, readAll(t, response))
	}
	response.Body.Close()
	response = request(t, handler, http.MethodGet, "/api/v1/profiles", nil, cookie, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatal(response.StatusCode)
	}
	if strings.Contains(string(readAll(t, response)), profile.ID) {
		t.Fatal("forgotten profile still listed")
	}
}

func waitFor(t *testing.T, timeout time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for lifecycle run")
}
