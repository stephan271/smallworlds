package consoleworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
)

// ErrNotFound is returned when a plan or run does not exist in the store.
var ErrNotFound = errors.New("consoleworkflow: record not found")

// Store persists compact plans and runs. In production it is backed by
// Kubernetes custom resources, so records survive a console/executor restart;
// this interface is the seam that CRD client plugs into.
type Store interface {
	PutPlan(ctx context.Context, plan ChangePlan) error
	GetPlan(ctx context.Context, id string) (ChangePlan, error)
	ListPlans(ctx context.Context) ([]ChangePlan, error)
	PutRun(ctx context.Context, run WorkflowRun) error
	GetRun(ctx context.Context, id string) (WorkflowRun, error)
	ListRuns(ctx context.Context) ([]WorkflowRun, error)
}

// Backing is the durable data behind a MemoryStore. It stands in for etcd: a
// MemoryStore built over an existing Backing sees every record a previous store
// wrote, modelling how CRD records survive a pod restart. Records are held as
// serialized bytes, exactly as a CRD store would.
type Backing struct {
	mu    sync.RWMutex
	plans map[string][]byte
	runs  map[string][]byte
}

// NewBacking creates empty durable backing.
func NewBacking() *Backing {
	return &Backing{plans: map[string][]byte{}, runs: map[string][]byte{}}
}

// MemoryStore is an in-memory Store over a shared Backing.
type MemoryStore struct {
	backing *Backing
}

// NewMemoryStore builds a store over the given backing. Passing the same backing
// to a second store models a restart that keeps the durable records.
func NewMemoryStore(backing *Backing) *MemoryStore {
	if backing == nil {
		backing = NewBacking()
	}
	return &MemoryStore{backing: backing}
}

func (store *MemoryStore) PutPlan(_ context.Context, plan ChangePlan) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	store.backing.mu.Lock()
	defer store.backing.mu.Unlock()
	store.backing.plans[plan.ID] = encoded
	return nil
}

func (store *MemoryStore) GetPlan(_ context.Context, id string) (ChangePlan, error) {
	store.backing.mu.RLock()
	defer store.backing.mu.RUnlock()
	encoded, ok := store.backing.plans[id]
	if !ok {
		return ChangePlan{}, ErrNotFound
	}
	var plan ChangePlan
	if err := json.Unmarshal(encoded, &plan); err != nil {
		return ChangePlan{}, err
	}
	return plan, nil
}

func (store *MemoryStore) ListPlans(_ context.Context) ([]ChangePlan, error) {
	store.backing.mu.RLock()
	defer store.backing.mu.RUnlock()
	plans := make([]ChangePlan, 0, len(store.backing.plans))
	for _, encoded := range store.backing.plans {
		var plan ChangePlan
		if err := json.Unmarshal(encoded, &plan); err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].ID < plans[j].ID })
	return plans, nil
}

func (store *MemoryStore) PutRun(_ context.Context, run WorkflowRun) error {
	if err := run.Validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal(run)
	if err != nil {
		return err
	}
	store.backing.mu.Lock()
	defer store.backing.mu.Unlock()
	store.backing.runs[run.ID] = encoded
	return nil
}

func (store *MemoryStore) GetRun(_ context.Context, id string) (WorkflowRun, error) {
	store.backing.mu.RLock()
	defer store.backing.mu.RUnlock()
	encoded, ok := store.backing.runs[id]
	if !ok {
		return WorkflowRun{}, ErrNotFound
	}
	var run WorkflowRun
	if err := json.Unmarshal(encoded, &run); err != nil {
		return WorkflowRun{}, err
	}
	return run, nil
}

func (store *MemoryStore) ListRuns(_ context.Context) ([]WorkflowRun, error) {
	store.backing.mu.RLock()
	defer store.backing.mu.RUnlock()
	runs := make([]WorkflowRun, 0, len(store.backing.runs))
	for _, encoded := range store.backing.runs {
		var run WorkflowRun
		if err := json.Unmarshal(encoded, &run); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].ID < runs[j].ID })
	return runs, nil
}
