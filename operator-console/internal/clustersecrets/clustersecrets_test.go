package clustersecrets_test

import (
	"strings"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/clustersecrets"
	"github.com/stephan271/smallworlds/operator-console/internal/localbootstrap"
)

func repository() clustersecrets.Repository {
	return clustersecrets.Repository{URL: "https://github.com/octocat/overlay", Username: "octocat", Password: "git-token"}
}

// The generated manifest has to satisfy the same gate an Operator-supplied one
// does, or the console would be writing something it would refuse by hand.
func TestGeneratedManifestPassesTheSameValidationAsASuppliedOne(t *testing.T) {
	generated, err := clustersecrets.Generate(repository())
	if err != nil {
		t.Fatal(err)
	}
	if err := localbootstrap.ValidateSecretsManifest(generated.Manifest); err != nil {
		t.Fatalf("generated manifest refused: %v\n%s", err, generated.Manifest)
	}
	for _, reference := range clustersecrets.References() {
		if !strings.Contains(generated.Manifest, "name: "+reference.Name) || !strings.Contains(generated.Manifest, "namespace: "+reference.Namespace) {
			t.Fatalf("manifest is missing %s/%s:\n%s", reference.Namespace, reference.Name, generated.Manifest)
		}
	}
	// Argo CD finds a repository credential by this label, not by the Secret's
	// name, so its absence would be a cluster that installs and reconciles
	// nothing.
	if !strings.Contains(generated.Manifest, "argocd.argoproj.io/secret-type: repository") {
		t.Fatalf("repository Secret is not labelled for Argo CD:\n%s", generated.Manifest)
	}
	if !strings.Contains(generated.Manifest, "url: https://github.com/octocat/overlay") || !strings.Contains(generated.Manifest, "password: git-token") {
		t.Fatalf("repository credential was not written:\n%s", generated.Manifest)
	}
}

func TestGenerateRefusesWithoutARepositoryCredential(t *testing.T) {
	for _, incomplete := range []clustersecrets.Repository{
		{Username: "octocat", Password: "git-token"},
		{URL: "https://github.com/octocat/overlay", Password: "git-token"},
		{URL: "https://github.com/octocat/overlay", Username: "octocat"},
	} {
		if _, err := clustersecrets.Generate(incomplete); err == nil {
			t.Fatalf("a manifest was generated without Argo CD access: %#v", incomplete)
		}
	}
}

func TestGeneratedValuesAreDistinctAndFreshEveryTime(t *testing.T) {
	first, err := clustersecrets.Generate(repository())
	if err != nil {
		t.Fatal(err)
	}
	second, err := clustersecrets.Generate(repository())
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"keycloak": first.Credentials.KeycloakAdminPassword,
		"grafana":  first.Credentials.GrafanaAdminPassword,
	}
	if values["keycloak"] == values["grafana"] || len(values["keycloak"]) != 32 || len(values["grafana"]) != 32 {
		t.Fatalf("credentials = %#v", first.Credentials)
	}
	if first.Credentials == second.Credentials || first.Manifest == second.Manifest {
		t.Fatal("a second cluster was given the same credentials as the first")
	}
}

// A provider token is whatever the provider issued. Assembling YAML by hand
// would turn one holding a colon, a quote or a newline into a different
// document — silently, and only on that Operator's cluster.
func TestARepositoryTokenThatIsHostileToYAMLSurvivesIntact(t *testing.T) {
	hostile := "ghp_a:b #c\n\"quoted\"\n  indented: value"
	generated, err := clustersecrets.Generate(clustersecrets.Repository{URL: "https://git.example/overlay.git", Username: "operator", Password: hostile})
	if err != nil {
		t.Fatal(err)
	}
	if err := localbootstrap.ValidateSecretsManifest(generated.Manifest); err != nil {
		t.Fatalf("manifest refused: %v\n%s", err, generated.Manifest)
	}
	credentials, err := clustersecrets.ReadCredentials(generated.Manifest)
	if err != nil || credentials.KeycloakAdminPassword != generated.Credentials.KeycloakAdminPassword {
		t.Fatalf("credentials=%#v err=%v", credentials, err)
	}
}

func TestReadCredentialsRecoversWhatWasGenerated(t *testing.T) {
	generated, err := clustersecrets.Generate(repository())
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := clustersecrets.ReadCredentials(generated.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if credentials != generated.Credentials || !credentials.Present() {
		t.Fatalf("read %#v, generated %#v", credentials, generated.Credentials)
	}
}

// An Operator who brings their own manifest names things how they like. Saying
// "here are your admin credentials" and showing nothing, or showing something
// invented, would both be worse than saying there is nothing to show.
func TestReadCredentialsReportsNothingForAnUnfamiliarManifest(t *testing.T) {
	credentials, err := clustersecrets.ReadCredentials("apiVersion: v1\nkind: Secret\nmetadata:\n  name: my-own-secret\n  namespace: default\nstringData:\n  admin-password: hunter2\n")
	if err != nil {
		t.Fatal(err)
	}
	if credentials.Present() {
		t.Fatalf("credentials were claimed from an unfamiliar manifest: %#v", credentials)
	}
}
