package localbootstrap_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/localbootstrap"
)

// The sample is the shape a real converging cluster produces: a healthy node, a
// root Application that is Synced but Degraded, workloads that cannot start
// because a Cluster Secret was never supplied, and a great many pods that are
// simply fine.
const sampleDetailOutput = `#markers
argocd-ready
bootstrap-complete
k3s-ready
overlay-applied
#nodes
smallworlds-local-node|True|v1.36.2+k3s1
#applications
cert-manager|Synced|Healthy|
keycloak|Synced|Progressing|
kube-prometheus-stack|Synced|Degraded|deployment has 0 available replicas
smallworlds-root|Synced|Degraded|
velero|OutOfSync|Healthy|
#workloads
argocd|argocd-server-7d4|Running|true,|0,|;|;
garage-system|garage-0|Pending|false,|0,|CreateContainerConfigError;|secret "garage-auth-secret" not found;
keycloak|keycloak-keycloakx-0|Pending|false,|0,|CreateContainerConfigError;|secret "keycloak-admin-creds" not found;
trivy-system|scan-vulnerabilityreport-576|Pending|false,|0,|PodInitializing;|
velero|velero-upgrade-crds-qsv74|Succeeded|true,|0,|;|;
monitoring|grafana-59445b44b9|Running|true,false,true,|2,0,1,|CreateContainerConfigError;CreateContainerConfigError;CreateContainerConfigError;|secret "grafana-admin-creds" not found;secret "grafana-admin-creds" not found;secret "grafana-admin-creds" not found;
`

func TestParseDetailReportsOnlyWhatIsNotSettled(t *testing.T) {
	observedAt := time.Date(2026, 7, 27, 12, 30, 0, 0, time.UTC)
	detail := localbootstrap.ParseDetail(sampleDetailOutput, observedAt)

	if !detail.ObservedAt.Equal(observedAt) {
		t.Fatalf("observedAt = %s, want %s", detail.ObservedAt, observedAt)
	}
	if len(detail.Markers) != 4 || detail.Markers[0] != "argocd-ready" {
		t.Fatalf("markers = %v", detail.Markers)
	}
	if len(detail.Nodes) != 1 || !detail.Nodes[0].Ready || detail.Nodes[0].Version != "v1.36.2+k3s1" {
		t.Fatalf("nodes = %+v", detail.Nodes)
	}
	if len(detail.Applications) != 5 {
		t.Fatalf("applications = %+v", detail.Applications)
	}
	// Every Application is reported, healthy or not: an Operator reading this is
	// asking what the cluster is doing, and "these fourteen are fine" is part of
	// the answer.
	if detail.Applications[2].Health != "Degraded" || detail.Applications[2].Message != "deployment has 0 available replicas" {
		t.Fatalf("degraded application = %+v", detail.Applications[2])
	}

	// A Running pod with every container ready says nothing, and a Succeeded one
	// is a finished Job. Listing either would bury the three that cannot start.
	names := make([]string, 0, len(detail.Workloads))
	for _, workload := range detail.Workloads {
		names = append(names, workload.Name)
	}
	want := []string{"garage-0", "keycloak-keycloakx-0", "scan-vulnerabilityreport-576", "grafana-59445b44b9"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("workloads = %v, want %v", names, want)
	}

	keycloak := detail.Workloads[1]
	if keycloak.Namespace != "keycloak" || keycloak.Ready != "0/1" || keycloak.Reason != "CreateContainerConfigError" {
		t.Fatalf("keycloak workload = %+v", keycloak)
	}
	// The cluster's own words about what is wrong are the whole point of this
	// view; a reason without them sends an Operator hunting.
	if keycloak.Message != `secret "keycloak-admin-creds" not found` {
		t.Fatalf("keycloak message = %q", keycloak.Message)
	}
	// A partially ready pod is not settled even while its phase reads Running.
	grafana := detail.Workloads[3]
	if grafana.Ready != "2/3" || grafana.Restarts != 3 {
		t.Fatalf("grafana workload = %+v", grafana)
	}
	// Three containers blocked on one missing Secret is one problem. Repeating
	// the sentence per container reads as three, and sends an Operator looking
	// for two things that do not exist.
	if grafana.Reason != "CreateContainerConfigError" {
		t.Fatalf("grafana reason = %q", grafana.Reason)
	}
	if grafana.Message != `secret "grafana-admin-creds" not found` {
		t.Fatalf("grafana message = %q", grafana.Message)
	}
	if detail.WorkloadsTruncated {
		t.Fatal("a six-line sample was reported as truncated")
	}
	if detail.Converged() {
		t.Fatal("a cluster with a Degraded root Application was reported as converged")
	}
}

func TestParseDetailToleratesAnEmptyAndAConvergedCluster(t *testing.T) {
	empty := localbootstrap.ParseDetail("", time.Now())
	if len(empty.Markers) != 0 || len(empty.Nodes) != 0 || len(empty.Applications) != 0 || len(empty.Workloads) != 0 {
		t.Fatalf("empty output produced %+v", empty)
	}
	// Nothing observed is not the same as everything healthy, and must never be
	// rounded up to it.
	if empty.Converged() {
		t.Fatal("an unobserved cluster was reported as converged")
	}

	converged := localbootstrap.ParseDetail(`#nodes
node-a|True|v1.36.2+k3s1
#applications
smallworlds-root|Synced|Healthy|
#workloads
argocd|argocd-server-7d4|Running|true,|0,|;|;
`, time.Now())
	if !converged.Converged() {
		t.Fatalf("a healthy cluster was not reported as converged: %+v", converged)
	}
}

func TestParseDetailKeepsAMessageWhole(t *testing.T) {
	// A message is the only free-form field, so it is read last: a delimiter
	// inside it must not shift the namespace or the name, which are what an
	// Operator uses to go and look.
	detail := localbootstrap.ParseDetail(`#workloads
kube-system|coredns-abc|Pending|false,|0,|CreateContainerConfigError;|open /etc/x|y: no such file
`, time.Now())
	if len(detail.Workloads) != 1 {
		t.Fatalf("workloads = %+v", detail.Workloads)
	}
	if detail.Workloads[0].Namespace != "kube-system" || detail.Workloads[0].Name != "coredns-abc" {
		t.Fatalf("identity shifted: %+v", detail.Workloads[0])
	}
	if detail.Workloads[0].Message != "open /etc/x|y: no such file" {
		t.Fatalf("message = %q", detail.Workloads[0].Message)
	}
}

func TestParseDetailBoundsTheWorkloadListAndSaysSo(t *testing.T) {
	var builder strings.Builder
	builder.WriteString("#workloads\n")
	for index := 0; index < 60; index++ {
		builder.WriteString("ns|pod-x|Pending|false,|0,|ImagePullBackOff;|\n")
	}
	detail := localbootstrap.ParseDetail(builder.String(), time.Now())
	if len(detail.Workloads) != 40 {
		t.Fatalf("workloads = %d, want 40", len(detail.Workloads))
	}
	// A list an Operator acts on must say when it is not the whole list.
	if !detail.WorkloadsTruncated {
		t.Fatal("a truncated workload list was not reported as truncated")
	}
}
