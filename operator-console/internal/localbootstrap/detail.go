package localbootstrap

import (
	"strconv"
	"strings"
	"time"
)

// Detail is a read-only picture of what the cluster is doing at one moment.
//
// The convergence Observation answers "is it finished", which is all the run
// itself needs. It is not enough for an Operator watching a run that has been
// sitting at awaiting-convergence for half an hour: "not converged yet" and
// "waiting for a Secret nobody created" look identical from the outside, and
// the difference is the whole question. This carries the evidence behind the
// verdict — which node, which Argo CD Application, which workload, and what
// the cluster itself says is wrong with it.
type Detail struct {
	ObservedAt   time.Time              `json:"observedAt"`
	Markers      []string               `json:"markers"`
	Nodes        []NodeCondition        `json:"nodes"`
	Applications []ApplicationCondition `json:"applications"`
	Workloads    []WorkloadCondition    `json:"workloads"`
	// True when the workload list was cut short. An Operator who acts on a
	// truncated list must know it was truncated.
	WorkloadsTruncated bool `json:"workloadsTruncated"`
}

type NodeCondition struct {
	Name    string `json:"name"`
	Ready   bool   `json:"ready"`
	Version string `json:"version,omitempty"`
}

type ApplicationCondition struct {
	Name    string `json:"name"`
	Sync    string `json:"sync,omitempty"`
	Health  string `json:"health,omitempty"`
	Message string `json:"message,omitempty"`
}

type WorkloadCondition struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Phase     string `json:"phase,omitempty"`
	Ready     string `json:"ready,omitempty"`
	Restarts  int    `json:"restarts"`
	Reason    string `json:"reason,omitempty"`
	Message   string `json:"message,omitempty"`
}

// maxDetailWorkloads bounds the payload. A cluster mid-convergence can hold
// hundreds of pending pods, and a list that long is not a diagnosis anyway.
const maxDetailWorkloads = 40

// maxDetailMessage bounds one message. Kubernetes messages are usually a line;
// a runaway one must not turn the record into a log dump.
const maxDetailMessage = 400

// detailCommand emits every section in one privileged shell round trip, so a
// picture is internally consistent rather than assembled from readings taken
// seconds apart. Every part tolerates its own absence: a cluster without the
// Argo CD CRDs yet is a normal state to report, not an error to fail on.
const detailCommand = `
echo '#markers'
ls -1 /etc/smallworlds 2>/dev/null || true
echo '#nodes'
k3s kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"|"}{range .status.conditions[?(@.type=="Ready")]}{.status}{end}{"|"}{.status.nodeInfo.kubeletVersion}{"\n"}{end}' 2>/dev/null || true
echo
echo '#applications'
k3s kubectl -n argocd get applications -o jsonpath='{range .items[*]}{.metadata.name}{"|"}{.status.sync.status}{"|"}{.status.health.status}{"|"}{.status.health.message}{"\n"}{end}' 2>/dev/null || true
echo
echo '#workloads'
k3s kubectl get pods -A -o jsonpath='{range .items[*]}{.metadata.namespace}{"|"}{.metadata.name}{"|"}{.status.phase}{"|"}{range .status.containerStatuses[*]}{.ready}{","}{end}{"|"}{range .status.containerStatuses[*]}{.restartCount}{","}{end}{"|"}{range .status.initContainerStatuses[*]}{.state.waiting.reason}{";"}{end}{range .status.containerStatuses[*]}{.state.waiting.reason}{";"}{end}{"|"}{range .status.initContainerStatuses[*]}{.state.waiting.message}{";"}{end}{range .status.containerStatuses[*]}{.state.waiting.message}{";"}{end}{"\n"}{end}' 2>/dev/null || true
echo
`

// ParseDetail turns the command's sectioned output into a Detail. It is
// deliberately forgiving about what it does not recognise and strict about what
// it reports: a line it cannot read is dropped rather than guessed at, because
// a wrong workload name sends an Operator to look at the wrong thing.
func ParseDetail(output string, observedAt time.Time) Detail {
	detail := Detail{ObservedAt: observedAt.UTC(), Markers: []string{}, Nodes: []NodeCondition{}, Applications: []ApplicationCondition{}, Workloads: []WorkloadCondition{}}
	section := ""
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			section = strings.TrimPrefix(trimmed, "#")
			continue
		}
		if trimmed == "" {
			continue
		}
		switch section {
		case "markers":
			detail.Markers = append(detail.Markers, trimmed)
		case "nodes":
			if node, ok := parseNodeCondition(trimmed); ok {
				detail.Nodes = append(detail.Nodes, node)
			}
		case "applications":
			if application, ok := parseApplicationCondition(trimmed); ok {
				detail.Applications = append(detail.Applications, application)
			}
		case "workloads":
			workload, include := parseWorkloadCondition(trimmed)
			if !include {
				continue
			}
			if len(detail.Workloads) >= maxDetailWorkloads {
				detail.WorkloadsTruncated = true
				continue
			}
			detail.Workloads = append(detail.Workloads, workload)
		}
	}
	return detail
}

func parseNodeCondition(line string) (NodeCondition, bool) {
	fields := strings.SplitN(line, "|", 3)
	if len(fields) < 2 || fields[0] == "" {
		return NodeCondition{}, false
	}
	node := NodeCondition{Name: fields[0], Ready: fields[1] == "True"}
	if len(fields) == 3 {
		node.Version = fields[2]
	}
	return node, true
}

func parseApplicationCondition(line string) (ApplicationCondition, bool) {
	fields := strings.SplitN(line, "|", 4)
	if len(fields) < 3 || fields[0] == "" {
		return ApplicationCondition{}, false
	}
	application := ApplicationCondition{Name: fields[0], Sync: fields[1], Health: fields[2]}
	if len(fields) == 4 {
		application.Message = truncateMessage(fields[3])
	}
	return application, true
}

// parseWorkloadCondition reports whether this workload is worth showing at all.
// A pod that is Running with every container ready is the normal case and says
// nothing; listing it would bury the four that cannot start.
func parseWorkloadCondition(line string) (WorkloadCondition, bool) {
	// The message is last precisely so a stray delimiter inside it cannot shift
	// any other field.
	fields := strings.SplitN(line, "|", 7)
	if len(fields) < 3 || fields[0] == "" || fields[1] == "" {
		return WorkloadCondition{}, false
	}
	workload := WorkloadCondition{Namespace: fields[0], Name: fields[1], Phase: fields[2]}
	ready, total := 0, 0
	if len(fields) > 3 {
		for _, flag := range splitList(fields[3], ",") {
			total++
			if flag == "true" {
				ready++
			}
		}
	}
	if len(fields) > 4 {
		for _, count := range splitList(fields[4], ",") {
			if parsed, err := strconv.Atoi(count); err == nil {
				workload.Restarts += parsed
			}
		}
	}
	if len(fields) > 5 {
		workload.Reason = strings.Join(uniqueList(splitList(fields[5], ";")), ", ")
	}
	if len(fields) > 6 {
		// Every container blocked on the same missing Secret says so separately.
		// Repeating one sentence three times reads as three problems.
		workload.Message = truncateMessage(strings.Join(uniqueList(splitList(fields[6], ";")), "; "))
	}
	if total > 0 {
		workload.Ready = strconv.Itoa(ready) + "/" + strconv.Itoa(total)
	}
	// Succeeded pods are finished Jobs; they are not a problem and not progress.
	if workload.Phase == "Succeeded" {
		return WorkloadCondition{}, false
	}
	settled := workload.Phase == "Running" && total > 0 && ready == total && workload.Reason == ""
	return workload, !settled
}

func splitList(value, separator string) []string {
	parts := make([]string, 0)
	for _, part := range strings.Split(value, separator) {
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

// uniqueList keeps the first occurrence of each entry and its order.
func uniqueList(values []string) []string {
	seen := make(map[string]bool, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	return unique
}

func truncateMessage(message string) string {
	message = strings.TrimSpace(message)
	if len(message) <= maxDetailMessage {
		return message
	}
	return message[:maxDetailMessage] + "…"
}

// Converged reports whether this picture shows a cluster that has arrived. It
// mirrors the run's own verdict rather than replacing it, so the two can be
// compared instead of quietly disagreeing.
func (detail Detail) Converged() bool {
	for _, node := range detail.Nodes {
		if !node.Ready {
			return false
		}
	}
	if len(detail.Nodes) == 0 || len(detail.Applications) == 0 {
		return false
	}
	for _, application := range detail.Applications {
		if application.Sync != "Synced" || application.Health != "Healthy" {
			return false
		}
	}
	return len(detail.Workloads) == 0
}
