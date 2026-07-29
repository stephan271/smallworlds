package consoleworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeResources stands in for etcd: a path-to-document map that outlives the
// store built over it, which is how a console restart is modelled.
type fakeResources struct {
	documents map[string][]byte
	failGet   error
}

func newFakeResources() *fakeResources {
	return &fakeResources{documents: map[string][]byte{}}
}

var errFakeNotFound = errors.New("fake: object not found")

func (resources *fakeResources) Get(_ context.Context, path string, target any) error {
	if resources.failGet != nil {
		return resources.failGet
	}
	if document, ok := resources.documents[path]; ok {
		return json.Unmarshal(document, target)
	}
	// A collection path always exists, even when empty — that is how the API
	// server answers a list for a registered but unused custom resource.
	if !strings.Contains(strings.TrimPrefix(path, "/apis/"), "/") || strings.HasSuffix(path, "changeplans") || strings.HasSuffix(path, "workflowruns") {
		items := []json.RawMessage{}
		for storedPath, document := range resources.documents {
			if strings.HasPrefix(storedPath, path+"/") {
				items = append(items, json.RawMessage(document))
			}
		}
		encoded, err := json.Marshal(map[string]any{"items": items})
		if err != nil {
			return err
		}
		return json.Unmarshal(encoded, target)
	}
	return errFakeNotFound
}

func (resources *fakeResources) Put(_ context.Context, collectionPath, name string, object any) error {
	encoded, err := json.Marshal(object)
	if err != nil {
		return err
	}
	resources.documents[collectionPath+"/"+name] = encoded
	return nil
}

func kubernetesStore(resources *fakeResources) *KubernetesStore {
	return NewKubernetesStore(resources, "operator-console", func(err error) bool { return errors.Is(err, errFakeNotFound) })
}

func storedPlan(id string, at time.Time) ChangePlan {
	plan := ChangePlan{
		ID:        id,
		Intent:    IntentAddCapability,
		Actor:     "ada",
		Summary:   "Add Excalidraw to the community",
		Risks:     []RiskLabel{RiskReversible},
		CreatedAt: at,
		ExpiresAt: at.Add(time.Hour),
	}
	plan.Digest = ComputeDigest(plan.Intent, plan.Actor, plan.Summary, plan.Risks)
	return plan
}

// The point of the CRD store is that compact records outlive the pod. A second
// store over the same backing is a restarted console.
func TestKubernetesStoreSurvivesRestart(t *testing.T) {
	resources := newFakeResources()
	at := time.Now().UTC().Truncate(time.Second)
	plan := storedPlan("plan-01", at)
	approved, err := plan.Approve("ada", at)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	run, err := approved.Start("run-01", at)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run = run.Checkpoint("branch-pushed", at.Add(time.Second))

	before := kubernetesStore(resources)
	if err := before.PutPlan(context.Background(), approved); err != nil {
		t.Fatalf("put plan: %v", err)
	}
	if err := before.PutRun(context.Background(), run); err != nil {
		t.Fatalf("put run: %v", err)
	}

	after := kubernetesStore(resources)
	readPlan, err := after.GetPlan(context.Background(), "plan-01")
	if err != nil {
		t.Fatalf("get plan after restart: %v", err)
	}
	if readPlan.Approval == nil || readPlan.Approval.Actor != "ada" {
		t.Fatalf("approval did not survive the restart: %+v", readPlan)
	}
	if readPlan.Digest != approved.Digest {
		t.Fatalf("digest = %q, want %q", readPlan.Digest, approved.Digest)
	}
	readRun, err := after.GetRun(context.Background(), "run-01")
	if err != nil {
		t.Fatalf("get run after restart: %v", err)
	}
	if readRun.CurrentCheckpoint != "branch-pushed" || len(readRun.Checkpoints) != 1 {
		t.Fatalf("checkpoints did not survive the restart: %+v", readRun)
	}
	// Detailed events are never stored inline; the run must still point at Loki.
	if readRun.Loki.Query == "" {
		t.Fatal("the Loki reference did not survive the restart")
	}
}

func TestKubernetesStoreListsNewestFirst(t *testing.T) {
	resources := newFakeResources()
	store := kubernetesStore(resources)
	base := time.Now().UTC().Truncate(time.Second)
	for index, id := range []string{"plan-old", "plan-new"} {
		plan := storedPlan(id, base.Add(time.Duration(index)*time.Hour))
		if err := store.PutPlan(context.Background(), plan); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}
	plans, err := store.ListPlans(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(plans) != 2 || plans[0].ID != "plan-new" {
		t.Fatalf("plans = %+v, want the newest first", plans)
	}
}

// Writing the same record twice must land on the same object, or a retried
// write would fill etcd with duplicates of one plan.
func TestKubernetesStoreWritesAreIdempotent(t *testing.T) {
	resources := newFakeResources()
	store := kubernetesStore(resources)
	at := time.Now().UTC().Truncate(time.Second)
	for range 3 {
		if err := store.PutPlan(context.Background(), storedPlan("plan-01", at)); err != nil {
			t.Fatalf("put: %v", err)
		}
	}
	if len(resources.documents) != 1 {
		t.Fatalf("stored %d objects, want 1", len(resources.documents))
	}
}

// A record that does not exist and an API server that cannot be reached are
// different answers, and the store must not flatten the second into the first.
func TestKubernetesStoreDistinguishesMissingFromUnreachable(t *testing.T) {
	resources := newFakeResources()
	store := kubernetesStore(resources)

	if _, err := store.GetPlan(context.Background(), "absent"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}

	resources.failGet = errors.New("connection refused")
	_, err := store.GetPlan(context.Background(), "absent")
	if errors.Is(err, ErrNotFound) || err == nil {
		t.Fatalf("error = %v, want the transport failure to surface", err)
	}
}

// A record id is not required to be a valid Kubernetes name, so the mapping has
// to be total, stable, and collision-free.
func TestObjectNameIsStableAndDistinct(t *testing.T) {
	first := ObjectName("Plan/2026-07-28:01")
	second := ObjectName("plan-2026-07-28-01")
	if first == second {
		t.Fatal("ids that sanitize alike must still get distinct object names")
	}
	if first != ObjectName("Plan/2026-07-28:01") {
		t.Fatal("object names must be stable for the same id")
	}
	for _, name := range []string{first, second, ObjectName(""), ObjectName(strings.Repeat("x", 300))} {
		if name == "" || len(name) > 253 {
			t.Fatalf("invalid object name %q", name)
		}
		for _, character := range name {
			if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyz0123456789-", character) {
				t.Fatalf("object name %q carries an invalid character %q", name, character)
			}
		}
		if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
			t.Fatalf("object name %q is not a valid RFC 1123 name", name)
		}
	}
}

// The store validates before it writes; an oversized or malformed record must
// never reach etcd.
func TestKubernetesStoreRejectsInvalidRecords(t *testing.T) {
	resources := newFakeResources()
	store := kubernetesStore(resources)
	if err := store.PutPlan(context.Background(), ChangePlan{ID: "plan-01"}); err == nil {
		t.Fatal("expected an invalid plan to be refused")
	}
	if len(resources.documents) != 0 {
		t.Fatal("an invalid plan must not be written")
	}
}
