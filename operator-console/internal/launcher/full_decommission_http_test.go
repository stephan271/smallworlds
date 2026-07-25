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

type fullInspector struct {
	mu             sync.Mutex
	calls          int
	weakProtection bool
	resources      []decommission.Resource
}

func (inspector *fullInspector) InspectFull(_ context.Context, profile state.Profile) (decommission.FullInspection, error) {
	inspector.mu.Lock()
	defer inspector.mu.Unlock()
	inspector.calls++
	classified, err := decommission.ClassifyFull(profile.ID, inspector.resources)
	if err != nil {
		return decommission.FullInspection{}, err
	}
	protection := decommission.ProtectionEvidence{BackupFreshness: "current", OffsiteRecoveryPoints: []string{"2026-07-25T00:00:00Z"}, RecoveryBundleStatus: "exported", Sufficient: true}
	if inspector.weakProtection {
		protection = decommission.ProtectionEvidence{BackupFreshness: "stale", RecoveryBundleStatus: "missing", Sufficient: false, Warnings: []string{"offsite Recovery Point is stale"}}
	}
	return decommission.FullInspection{ProfileID: profile.ID, DeploymentMode: profile.DeploymentMode, ProfileRevision: profile.Revision, ObservedAt: time.Now().UTC(), Resources: classified, Protection: protection}, nil
}

type fullExecutor struct {
	mu        sync.Mutex
	calls     int
	failStage decommission.Stage
	failed    bool
	removed   []string
}

func (executor *fullExecutor) RemoveFull(_ context.Context, _ string, item decommission.FullItem) error {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.calls++
	executor.removed = append(executor.removed, item.ProviderID)
	return nil
}

func (executor *fullExecutor) AfterFullDecommissionStage(_ context.Context, _ string, stage decommission.Stage) error {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.failStage == stage && !executor.failed {
		executor.failed = true
		return errors.New("injected post-stage interruption")
	}
	return nil
}

func fullResources() []decommission.Resource {
	return []decommission.Resource{
		{Kind: "server", ProviderID: "compute-1"},
		{Kind: "volume", ProviderID: "storage-1", Persistent: true},
		{Kind: "firewall", ProviderID: "network-1"},
		{Kind: "dns-record", ProviderID: "dns-1"},
		{Kind: "dns-zone", ProviderID: "zone-1", Shared: true},
		{Kind: "gitops-overlay", ProviderID: "overlay-1"},
		{Kind: "server", ProviderID: "unknown-1"},
	}
}

func TestFullDecommissionAllModesResumeEveryDeletionStage(t *testing.T) {
	for _, mode := range []string{"hetzner", "local-lan", "local-public"} {
		for stage, failStage := range map[string]decommission.Stage{"compute": decommission.ComputeStage, "storage": decommission.StorageStage, "networking": decommission.NetworkingStage, "dns": decommission.DNSStage} {
			t.Run(mode+"/"+stage, func(t *testing.T) {
				inspector := &fullInspector{resources: fullResources()}
				executor := &fullExecutor{failStage: failStage}
				handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "full-" + mode + stage, FullDecommissionInspector: inspector, FullDecommissionExecutor: executor})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = handler.Close() })
				cookie, csrf := exchange(t, handler, "full-"+mode+stage)
				profile := createProfile(t, handler, cookie, csrf, "Full "+mode, "en", mode)
				inspector.mu.Lock()
				for index := range inspector.resources {
					if inspector.resources[index].Kind != "dns-zone" && inspector.resources[index].Kind != "gitops-overlay" && inspector.resources[index].ProviderID != "unknown-1" {
						inspector.resources[index].Tags = map[string]string{"smallworlds-profile": profile.ID}
						inspector.resources[index].ClusterIdentity = profile.ID
					}
				}
				inspector.mu.Unlock()
				planned := fullPlanForTest(t, handler, cookie, csrf, profile.ID)
				// The ordinary approval path must never be enough for full deletion.
				response := request(t, handler, http.MethodPost, "/api/v1/plans/"+planned.Plan.ID+"/approve", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
				if response.StatusCode != http.StatusConflict {
					t.Fatalf("ordinary approval = %d", response.StatusCode)
				}
				response.Body.Close()
				run := approveFullPlanForTest(t, handler, cookie, csrf, planned, false, "")
				waitFor(t, 2*time.Second, func() bool {
					response := request(t, handler, http.MethodGet, "/api/v1/runs/"+run.ID, nil, cookie, nil)
					defer response.Body.Close()
					var current struct {
						CurrentCheckpoint string `json:"currentCheckpoint"`
					}
					_ = json.NewDecoder(response.Body).Decode(&current)
					return strings.HasPrefix(current.CurrentCheckpoint, "full-decommission-interrupted-")
				})
				response = request(t, handler, http.MethodPost, "/api/v1/full-decommission/runs/"+run.ID+"/resume", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
				if response.StatusCode != http.StatusAccepted {
					t.Fatalf("resume = %d: %s", response.StatusCode, readAll(t, response))
				}
				response.Body.Close()
				waitFor(t, 2*time.Second, func() bool {
					response := request(t, handler, http.MethodGet, "/api/v1/runs/"+run.ID, nil, cookie, nil)
					defer response.Body.Close()
					var current struct {
						State string `json:"state"`
					}
					_ = json.NewDecoder(response.Body).Decode(&current)
					return current.State == "verified"
				})
				executor.mu.Lock()
				removed := append([]string(nil), executor.removed...)
				executor.mu.Unlock()
				if len(removed) != 4 {
					t.Fatalf("removed = %v, want four proven resources", removed)
				}
				response = request(t, handler, http.MethodGet, "/api/v1/full-decommission/activity?profileId="+profile.ID, nil, cookie, nil)
				if response.StatusCode != http.StatusOK {
					t.Fatalf("activity export = %d: %s", response.StatusCode, readAll(t, response))
				}
				if body := readAll(t, response); !contains(body, "full-decommission.completed") || contains(body, "unknown-1") {
					t.Fatalf("activity export was not redacted/final: %s", body)
				}
				inspector.mu.Lock()
				calls := inspector.calls
				inspector.mu.Unlock()
				if calls < 6 {
					t.Fatalf("fresh inspection calls = %d, want plan + execution + resume", calls)
				}
			})
		}
	}
}

func TestFullDecommissionWeakProtectionRequiresOwnerOverride(t *testing.T) {
	inspector := &fullInspector{resources: fullResources(), weakProtection: true}
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "full-override", FullDecommissionInspector: inspector, FullDecommissionExecutor: &fullExecutor{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "full-override")
	profile := createProfile(t, handler, cookie, csrf, "Override", "en", "hetzner")
	inspector.mu.Lock()
	for index := range inspector.resources {
		if inspector.resources[index].Kind != "dns-zone" && inspector.resources[index].Kind != "gitops-overlay" && inspector.resources[index].ProviderID != "unknown-1" {
			inspector.resources[index].Tags = map[string]string{"smallworlds-profile": profile.ID}
			inspector.resources[index].ClusterIdentity = profile.ID
		}
	}
	inspector.mu.Unlock()
	planned := fullPlanForTest(t, handler, cookie, csrf, profile.ID)
	if !planned.Decommission.RequiresOwnerOverride {
		t.Fatal("weak protection did not produce prominent override gate")
	}
	responseBody, _ := json.Marshal(map[string]any{"planId": planned.Plan.ID, "profileId": profile.ID, "planDigest": planned.Decommission.Digest, "confirmation": planned.Decommission.TypedConfirmation})
	response := request(t, handler, http.MethodPost, "/api/v1/full-decommission/approve", responseBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("missing override = %d", response.StatusCode)
	}
	response.Body.Close()
	_ = approveFullPlanForTest(t, handler, cookie, csrf, planned, true, "I accept deletion despite stale recovery evidence")
}

type fullPlanned struct {
	Plan struct {
		ID string `json:"id"`
	} `json:"plan"`
	Decommission decommission.FullPlan `json:"decommission"`
}

func fullPlanForTest(t *testing.T, handler *launcher.Server, cookie *http.Cookie, csrf, profileID string) fullPlanned {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"profileId": profileID})
	response := request(t, handler, http.MethodPost, "/api/v1/full-decommission/plan", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("plan = %d: %s", response.StatusCode, readAll(t, response))
	}
	var planned fullPlanned
	if err := json.NewDecoder(response.Body).Decode(&planned); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if planned.Plan.ID == "" || planned.Decommission.Digest == "" {
		t.Fatalf("missing plan binding: %#v", planned)
	}
	return planned
}
func approveFullPlanForTest(t *testing.T, handler *launcher.Server, cookie *http.Cookie, csrf string, planned fullPlanned, override bool, reason string) struct {
	ID string `json:"id"`
} {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"planId": planned.Plan.ID, "profileId": planned.Decommission.ProfileID, "planDigest": planned.Decommission.Digest, "confirmation": planned.Decommission.TypedConfirmation, "ownerOverride": override, "overrideReason": reason})
	response := request(t, handler, http.MethodPost, "/api/v1/full-decommission/approve", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("typed approval = %d: %s", response.StatusCode, readAll(t, response))
	}
	var run struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(response.Body).Decode(&run)
	response.Body.Close()
	return run
}
func contains(body []byte, text string) bool {
	for index := 0; index+len(text) <= len(body); index++ {
		if string(body[index:index+len(text)]) == text {
			return true
		}
	}
	return false
}
