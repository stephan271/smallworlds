package capability

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

type OverlayInput struct {
	Selection     Selection
	Release       string
	RepositoryURL string
	Domain        string
	// EnvironmentExtension sits between each hostname's label and the domain, so
	// a .dev cluster's hostnames can never collide with production's. Empty for
	// production.
	EnvironmentExtension string
}

type Overlay struct {
	Files      map[string]string `json:"files"`
	Diff       string            `json:"diff"`
	Assessment Assessment        `json:"assessment"`
}

var pinnedRelease = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)

// A key literally named password, token or secret, carrying a value. Deliberately
// not existingSecret:, secretName: or passwordKey:, which name secrets rather
// than contain them.
var secretValue = regexp.MustCompile(`(?im)^[\t -]*(password|token|secret)[\t ]*:[\t ]*\S`)

var validDomain = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)

func (catalog Catalog) RenderOverlay(input OverlayInput) (Overlay, error) {
	if !pinnedRelease.MatchString(input.Release) {
		return Overlay{}, fmt.Errorf("release must be an exact pinned tag")
	}
	repository, err := url.Parse(input.RepositoryURL)
	if err != nil || repository.Scheme != "https" || repository.Host == "" || repository.User != nil {
		return Overlay{}, fmt.Errorf("repository URL must be credential-free HTTPS")
	}
	if !validDomain.MatchString(input.Domain) {
		return Overlay{}, fmt.Errorf("invalid domain")
	}
	assessment, err := catalog.Assess(input.Selection)
	if err != nil {
		return Overlay{}, err
	}
	// Always installed, and each one carries hostnames that have to follow the
	// operator's domain — so each needs an overlay file of its own, whether or
	// not anybody chose it. Headscale belongs here for the same reason the other
	// three do: it is reached at an ordinary address before any device can join
	// the network it coordinates.
	// The Operator Console belongs here for a second reason on top of the
	// hostname: it is told its own address rather than merely routed to it, so
	// an overlay that skipped it would deploy a console whose OIDC redirect
	// still names the project's domain, and no Operator could log in.
	apps := []string{"dashboard", "keycloak", "headscale", "operator-console"}
	apps = append(apps, assessment.CommunityIDs...)
	sort.Strings(apps)
	files := map[string]string{}
	var root strings.Builder
	root.WriteString("apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - overlay-config.yaml\n")
	root.WriteString("  - https://github.com/stephan271/smallworlds.git/infrastructure/kubernetes?ref=" + input.Release + "\n")
	for _, app := range assessment.CommunityIDs {
		root.WriteString("  - https://raw.githubusercontent.com/stephan271/smallworlds/" + input.Release + "/infrastructure/kubernetes/apps/" + app + ".yaml\n")
	}
	root.WriteString("patches:\n")
	for _, app := range apps {
		root.WriteString("  - target:\n      group: argoproj.io\n      kind: Application\n      name: " + app + "\n    patch: |-\n      - op: replace\n        path: /spec/source/repoURL\n        value: " + input.RepositoryURL + "\n      - op: replace\n        path: /spec/source/path\n        value: " + app + "\n")
	}
	// Root-level components are not tenants and have no per-app file of their own,
	// so their hostnames are repointed here. Without this the GitOps dashboard
	// stays on the project's domain no matter what the operator chose.
	for _, component := range []string{"argocd-ingress", "kube-prometheus-stack"} {
		root.WriteString(DomainPatches(component, input.Domain, input.EnvironmentExtension))
	}
	files["kustomization.yaml"] = root.String()
	files["overlay-config.yaml"] = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: smallworlds-overlay\n  namespace: default\ndata:\n  baseDomain: " + input.Domain + "\n  deploymentMode: " + string(input.Selection.DeploymentMode) + "\n  smallworldsRelease: " + input.Release + "\n"
	for _, app := range apps {
		contents := "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - https://github.com/stephan271/smallworlds.git/infrastructure/kubernetes/tenants/" + app + "?ref=" + input.Release + "\n"
		if patches := DomainPatches(app, input.Domain, input.EnvironmentExtension); patches != "" {
			contents += "patches:\n" + patches
		}
		files[app+"/kustomization.yaml"] = contents
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var diff strings.Builder
	for _, path := range paths {
		diff.WriteString("diff --git a/" + path + " b/" + path + "\nnew file mode 100644\n--- /dev/null\n+++ b/" + path + "\n")
		for _, line := range strings.Split(strings.TrimSuffix(files[path], "\n"), "\n") {
			diff.WriteString("+" + line + "\n")
		}
	}
	return Overlay{Files: files, Diff: diff.String(), Assessment: assessment}, nil
}

func ValidateOverlay(overlay Overlay) error {
	root, found := overlay.Files["kustomization.yaml"]
	if !found || !strings.HasPrefix(root, "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\n") {
		return fmt.Errorf("missing root Kustomization")
	}
	// A secret VALUE must never reach Git. A secret NAME must: an overlay that
	// points Grafana at an existing Secret, or names the Secret cert-manager will
	// write a certificate into, is doing the safe thing. Matching the bare word
	// "secret" refused exactly those, so match a key that actually carries a
	// value instead.
	if secretValue.MatchString(overlay.Diff) {
		return fmt.Errorf("overlay contains secret-like field")
	}
	for path, contents := range overlay.Files {
		if path == "overlay-config.yaml" {
			if !strings.Contains(contents, "kind: ConfigMap") {
				return fmt.Errorf("invalid rendered file %q", path)
			}
			continue
		}
		if !strings.HasSuffix(path, "kustomization.yaml") || !strings.Contains(contents, "apiVersion: kustomize.config.k8s.io/v1beta1") || !strings.Contains(contents, "kind: Kustomization") {
			return fmt.Errorf("invalid rendered file %q", path)
		}
	}
	return nil
}

// Change is what a proposal against an existing overlay commits: the complete
// overlay as it should stand afterwards, and a diff against how it stands now.
type Change struct {
	Files map[string]string `json:"-"`
	Diff  string            `json:"diff"`
}

// RenderChange renders the overlay for `to` and the diff from `from`.
//
// It exists so that every flow which proposes a change to an overlay — adding a
// community application, moving to a new release — commits what this package
// would have established, rather than its own idea of the same thing. Two such
// ideas had drifted: one wrote an applications/ directory no overlay root ever
// included, so its proposals would have merged and created nothing; the other
// wrote a pins file with no reader anywhere. Both were invisible because
// nothing rendered the real overlay next to them.
func (catalog Catalog) RenderChange(from, to OverlayInput) (Change, error) {
	before, err := catalog.RenderOverlay(from)
	if err != nil {
		return Change{}, fmt.Errorf("render the overlay as it stands: %w", err)
	}
	after, err := catalog.RenderOverlay(to)
	if err != nil {
		return Change{}, fmt.Errorf("render the overlay as proposed: %w", err)
	}
	paths := map[string]bool{}
	for path := range before.Files {
		paths[path] = true
	}
	for path := range after.Files {
		paths[path] = true
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	var diff strings.Builder
	for _, path := range ordered {
		old, existed := before.Files[path]
		next, remains := after.Files[path]
		if old == next {
			continue
		}
		switch {
		case !existed:
			diff.WriteString("diff --git a/" + path + " b/" + path + "\nnew file mode 100644\n--- /dev/null\n+++ b/" + path + "\n")
		case !remains:
			diff.WriteString("diff --git a/" + path + " b/" + path + "\ndeleted file mode 100644\n--- a/" + path + "\n+++ /dev/null\n")
		default:
			diff.WriteString("diff --git a/" + path + " b/" + path + "\n--- a/" + path + "\n+++ b/" + path + "\n")
		}
		for _, line := range unifiedLines(lines(old), lines(next)) {
			diff.WriteString(line + "\n")
		}
	}
	return Change{Files: after.Files, Diff: diff.String()}, nil
}

// unifiedLines renders the change between two files as context, removals and
// additions. A whole-file replacement would be far easier and useless to read:
// adding one application to an overlay touches three lines of a forty-line root
// kustomization, and an operator asked to approve it should see those three,
// not the other thirty-seven twice.
func unifiedLines(before, after []string) []string {
	// Longest common subsequence over lines. Overlay files are tens of lines
	// long, so the quadratic table is far cheaper than a dependency.
	table := make([][]int, len(before)+1)
	for row := range table {
		table[row] = make([]int, len(after)+1)
	}
	for row := len(before) - 1; row >= 0; row-- {
		for column := len(after) - 1; column >= 0; column-- {
			if before[row] == after[column] {
				table[row][column] = table[row+1][column+1] + 1
			} else if table[row+1][column] >= table[row][column+1] {
				table[row][column] = table[row+1][column]
			} else {
				table[row][column] = table[row][column+1]
			}
		}
	}
	rendered := make([]string, 0, len(before)+len(after))
	row, column := 0, 0
	for row < len(before) && column < len(after) {
		switch {
		case before[row] == after[column]:
			rendered = append(rendered, " "+before[row])
			row, column = row+1, column+1
		case table[row+1][column] >= table[row][column+1]:
			rendered = append(rendered, "-"+before[row])
			row++
		default:
			rendered = append(rendered, "+"+after[column])
			column++
		}
	}
	for ; row < len(before); row++ {
		rendered = append(rendered, "-"+before[row])
	}
	for ; column < len(after); column++ {
		rendered = append(rendered, "+"+after[column])
	}
	return rendered
}

func lines(contents string) []string {
	if contents == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(contents, "\n"), "\n")
}
