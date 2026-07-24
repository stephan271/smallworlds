package addcapability

import (
	"fmt"
	"sort"
	"strings"
)

// repositoryReference is the base repository the tenant manifests and ArgoCD
// Application definitions are pulled from at a pinned release. It matches the
// reference issue 04/05 renders into the overlay.
const repositoryReference = "stephan271/smallworlds"

// proposalFiles renders the catalog-derived Desired Configuration the proposal
// adds: for each newly-added capability, one self-contained overlay unit that
// (1) references the capability's ArgoCD Application manifest at the pinned
// release, repointed at the operator's overlay, and (2) references the tenant
// manifests. The files are purely additive — enabling a capability adds files,
// it never edits live Kubernetes resources — so the diff is an exact new-file
// diff. The overlay root includes the applications/ directory, so writing these
// files is what activates the capability.
func proposalFiles(added []string, overlay OverlayTarget) map[string]string {
	files := make(map[string]string, len(added)*2)
	for _, app := range added {
		files["applications/"+app+".yaml"] = applicationUnit(app, overlay)
		files[app+"/kustomization.yaml"] = tenantUnit(app, overlay)
	}
	return files
}

func applicationUnit(app string, overlay OverlayTarget) string {
	return fmt.Sprintf(`apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - https://raw.githubusercontent.com/%[1]s/%[2]s/infrastructure/kubernetes/apps/%[3]s.yaml
patches:
  - target:
      group: argoproj.io
      kind: Application
      name: %[3]s
    patch: |-
      - op: replace
        path: /spec/source/repoURL
        value: %[4]s
      - op: replace
        path: /spec/source/path
        value: %[3]s
`, repositoryReference, overlay.Release, app, overlay.RepositoryURL)
}

func tenantUnit(app string, overlay OverlayTarget) string {
	return fmt.Sprintf(`apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - https://github.com/%[1]s.git/infrastructure/kubernetes/tenants/%[2]s?ref=%[3]s
`, repositoryReference, app, overlay.Release)
}

// renderDiff renders a deterministic new-file unified diff for the proposal
// files, in the same style as the overlay renderer, so the operator reviews
// exactly the bytes the proposal commits.
func renderDiff(files map[string]string) string {
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
	return diff.String()
}
