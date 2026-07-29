package consoleworkflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// This is the production Store from ADR 0025: compact Change Plan and Workflow
// Run records live as Kubernetes custom resources, so they survive a console
// restart without introducing another application database, and inherit the
// cluster's existing Velero protection. Detailed events are never stored here —
// each run carries a Loki reference instead, which is what keeps a custom
// resource compact enough to belong in etcd at all.

const (
	// APIGroup is the console's own API group. Its custom resources are the only
	// objects the console writes; everything it observes it reads.
	APIGroup = "admin.smallworlds.network"
	// APIVersion is the group's current version.
	APIVersion = "v1alpha1"
	// PlanKind and RunKind are the two custom resource kinds.
	PlanKind = "ChangePlan"
	RunKind  = "WorkflowRun"

	planResource = "changeplans"
	runResource  = "workflowruns"
)

// ResourceClient is the Kubernetes access the store needs. kubeclient.Client
// satisfies it; tests inject a fake API server.
type ResourceClient interface {
	Get(ctx context.Context, path string, target any) error
	Put(ctx context.Context, collectionPath, name string, object any) error
}

// KubernetesStore persists plans and runs as custom resources in one namespace.
type KubernetesStore struct {
	Client    ResourceClient
	Namespace string
	// IsNotFound recognizes the client's missing-object error. It is injected so
	// this package needs no Kubernetes dependency of its own, and so a missing
	// record can be told apart from an unreachable API server — reporting "no
	// such plan" for an outage would be a lie about the Activity Record.
	IsNotFound func(error) bool
}

// NewKubernetesStore returns a store writing into the given namespace.
func NewKubernetesStore(client ResourceClient, namespace string, isNotFound func(error) bool) *KubernetesStore {
	return &KubernetesStore{Client: client, Namespace: namespace, IsNotFound: isNotFound}
}

func (store *KubernetesStore) notFound(err error) bool {
	return err != nil && store.IsNotFound != nil && store.IsNotFound(err)
}

// resource is the wire shape of one custom resource. The whole compact record
// sits under spec, so the Go type is the schema and the two cannot drift.
type resource[Record any] struct {
	APIVersion string   `json:"apiVersion"`
	Kind       string   `json:"kind"`
	Metadata   metadata `json:"metadata"`
	Spec       Record   `json:"spec"`
}

type metadata struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type resourceList[Record any] struct {
	Items []resource[Record] `json:"items"`
}

func (store *KubernetesStore) collectionPath(resource string) string {
	return "/apis/" + APIGroup + "/" + APIVersion + "/namespaces/" + url.PathEscape(store.Namespace) + "/" + resource
}

func (store *KubernetesStore) objectPath(resource, name string) string {
	return store.collectionPath(resource) + "/" + url.PathEscape(name)
}

func (store *KubernetesStore) PutPlan(ctx context.Context, plan ChangePlan) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	object := resource[ChangePlan]{
		APIVersion: APIGroup + "/" + APIVersion,
		Kind:       PlanKind,
		Metadata: metadata{
			Name:      ObjectName(plan.ID),
			Namespace: store.Namespace,
			// The labels are the console's own index: they make a plan findable
			// by intent or actor with a field-free label selector, without
			// putting anything in a label that could carry secret material.
			Labels: map[string]string{
				APIGroup + "/intent":   labelValue(string(plan.Intent)),
				APIGroup + "/approved": fmt.Sprint(plan.Approval != nil),
			},
			Annotations: map[string]string{APIGroup + "/id": plan.ID},
		},
		Spec: plan,
	}
	return store.Client.Put(ctx, store.collectionPath(planResource), object.Metadata.Name, object)
}

func (store *KubernetesStore) GetPlan(ctx context.Context, id string) (ChangePlan, error) {
	var object resource[ChangePlan]
	if err := store.Client.Get(ctx, store.objectPath(planResource, ObjectName(id)), &object); err != nil {
		if store.notFound(err) {
			return ChangePlan{}, ErrNotFound
		}
		return ChangePlan{}, err
	}
	return object.Spec, nil
}

func (store *KubernetesStore) ListPlans(ctx context.Context) ([]ChangePlan, error) {
	var list resourceList[ChangePlan]
	if err := store.Client.Get(ctx, store.collectionPath(planResource), &list); err != nil {
		if store.notFound(err) {
			return nil, nil
		}
		return nil, err
	}
	plans := make([]ChangePlan, 0, len(list.Items))
	for _, item := range list.Items {
		plans = append(plans, item.Spec)
	}
	// Newest first: the console shows the most recent proposals, and etcd's own
	// ordering is by object name, which carries no meaning here.
	sort.SliceStable(plans, func(first, second int) bool { return plans[first].CreatedAt.After(plans[second].CreatedAt) })
	return plans, nil
}

func (store *KubernetesStore) PutRun(ctx context.Context, run WorkflowRun) error {
	if err := run.Validate(); err != nil {
		return err
	}
	object := resource[WorkflowRun]{
		APIVersion: APIGroup + "/" + APIVersion,
		Kind:       RunKind,
		Metadata: metadata{
			Name:      ObjectName(run.ID),
			Namespace: store.Namespace,
			Labels: map[string]string{
				APIGroup + "/intent": labelValue(string(run.Intent)),
				APIGroup + "/phase":  labelValue(string(run.Phase)),
				APIGroup + "/plan":   ObjectName(run.PlanID),
			},
			Annotations: map[string]string{APIGroup + "/id": run.ID},
		},
		Spec: run,
	}
	return store.Client.Put(ctx, store.collectionPath(runResource), object.Metadata.Name, object)
}

func (store *KubernetesStore) GetRun(ctx context.Context, id string) (WorkflowRun, error) {
	var object resource[WorkflowRun]
	if err := store.Client.Get(ctx, store.objectPath(runResource, ObjectName(id)), &object); err != nil {
		if store.notFound(err) {
			return WorkflowRun{}, ErrNotFound
		}
		return WorkflowRun{}, err
	}
	return object.Spec, nil
}

func (store *KubernetesStore) ListRuns(ctx context.Context) ([]WorkflowRun, error) {
	var list resourceList[WorkflowRun]
	if err := store.Client.Get(ctx, store.collectionPath(runResource), &list); err != nil {
		if store.notFound(err) {
			return nil, nil
		}
		return nil, err
	}
	runs := make([]WorkflowRun, 0, len(list.Items))
	for _, item := range list.Items {
		runs = append(runs, item.Spec)
	}
	sort.SliceStable(runs, func(first, second int) bool { return runs[first].StartedAt.After(runs[second].StartedAt) })
	return runs, nil
}

var unsafeNameCharacters = regexp.MustCompile(`[^a-z0-9-]+`)

// ObjectName derives a stable, valid Kubernetes object name from a record id.
// Record ids are the console's own identifiers and need not be RFC 1123 names,
// so the name is a sanitized prefix plus a digest of the full id: two ids that
// sanitize alike still get distinct objects, and the same id always maps to the
// same object, which is what makes a write idempotent.
func ObjectName(id string) string {
	digest := sha256.Sum256([]byte(id))
	suffix := hex.EncodeToString(digest[:])[:12]
	prefix := unsafeNameCharacters.ReplaceAllString(strings.ToLower(id), "-")
	prefix = strings.Trim(prefix, "-")
	if len(prefix) > 40 {
		prefix = strings.Trim(prefix[:40], "-")
	}
	if prefix == "" {
		return "record-" + suffix
	}
	return prefix + "-" + suffix
}

// labelValue bounds a value to what Kubernetes accepts in a label. The console's
// own vocabulary already fits; the clamp is here so a future intent name cannot
// make a write fail at the API server.
func labelValue(value string) string {
	value = unsafeNameCharacters.ReplaceAllString(strings.ToLower(value), "-")
	value = strings.Trim(value, "-")
	if len(value) > 63 {
		value = strings.Trim(value[:63], "-")
	}
	if value == "" {
		return "unset"
	}
	return value
}

// ensure the store satisfies the interface it exists to implement.
var _ Store = (*KubernetesStore)(nil)
