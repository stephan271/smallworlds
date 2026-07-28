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

// A cluster whose names never leave the building: no provider token at all.
func lanCluster() clustersecrets.Cluster {
	return clustersecrets.Cluster{Domain: "home.example", AdminEmail: "operator@home.example"}
}

// The generated manifest has to satisfy the same gate an Operator-supplied one
// does, or the console would be writing something it would refuse by hand.
func TestGeneratedManifestPassesTheSameValidationAsASuppliedOne(t *testing.T) {
	generated, err := clustersecrets.Generate(repository(), lanCluster())
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
		if _, err := clustersecrets.Generate(incomplete, lanCluster()); err == nil {
			t.Fatalf("a manifest was generated without Argo CD access: %#v", incomplete)
		}
	}
}

func TestGeneratedValuesAreDistinctAndFreshEveryTime(t *testing.T) {
	first, err := clustersecrets.Generate(repository(), lanCluster())
	if err != nil {
		t.Fatal(err)
	}
	second, err := clustersecrets.Generate(repository(), lanCluster())
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
	generated, err := clustersecrets.Generate(clustersecrets.Repository{URL: "https://git.example/overlay.git", Username: "operator", Password: hostile}, lanCluster())
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
	generated, err := clustersecrets.Generate(repository(), lanCluster())
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

// k3s applies this manifest from its auto-apply directory long before Argo CD
// has created anything, so a Secret whose namespace does not exist yet is
// refused outright. Shipping the namespaces in the same file is what keeps a
// first installation from spending its opening minutes in a retry loop.
func TestGeneratedManifestBringsTheNamespacesItsSecretsNeed(t *testing.T) {
	generated, err := clustersecrets.Generate(repository(), lanCluster())
	if err != nil {
		t.Fatal(err)
	}
	for _, namespace := range []string{"keycloak", "monitoring", "garage-system", "stalwart", "cert-manager"} {
		if !strings.Contains(generated.Manifest, "kind: Namespace") || !strings.Contains(generated.Manifest, "name: "+namespace+"\n") {
			t.Fatalf("namespace %q is not created by the manifest:\n%s", namespace, generated.Manifest)
		}
	}
	// The Argo CD installation owns its own namespace; taking it over from a
	// manifest k3s applies would be a quiet fight over the same object.
	if strings.Contains(generated.Manifest, "kind: Namespace\nmetadata:\n    name: argocd") {
		t.Fatalf("the manifest claims the argocd namespace:\n%s", generated.Manifest)
	}
	credentials, err := clustersecrets.ReadCredentials(generated.Manifest)
	if err != nil || credentials != generated.Credentials {
		t.Fatalf("namespaces confused the credential read-back: %#v, err = %v", credentials, err)
	}
}

// The three the console did not write on its first attempt. Stalwart's
// provisioner, cert-manager's solver and Renovate each mount one by name, and
// an absent Secret leaves them in CreateContainerConfigError indefinitely —
// which is exactly how a first real installation spent its night.
func TestGeneratedManifestCoversEverythingTheShellInstallerDelivers(t *testing.T) {
	generated, err := clustersecrets.Generate(repository(), lanCluster())
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"name: smallworlds-global-config", "ADMIN_EMAIL: operator@home.example",
		"name: stalwart-dns-secrets", "name: hetzner",
		// Renovate and the remediation agent mount this Secret by name.
		"name: repo-git-creds",
	} {
		if !strings.Contains(generated.Manifest, required) {
			t.Fatalf("manifest is missing %q:\n%s", required, generated.Manifest)
		}
	}
	if err := localbootstrap.ValidateSecretsManifest(generated.Manifest); err != nil {
		t.Fatalf("generated manifest refused: %v", err)
	}
}

// An empty provider token is a real answer, not a missing one: the Secrets are
// still written so the workloads that mount them can start.
func TestAClusterWithoutAProviderTokenStillGetsTheSecretsThatCarryIt(t *testing.T) {
	generated, err := clustersecrets.Generate(repository(), lanCluster())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(generated.Manifest, `HCLOUD_TOKEN: ""`) || !strings.Contains(generated.Manifest, `token: ""`) {
		t.Fatalf("empty provider token was omitted instead of written:\n%s", generated.Manifest)
	}
}

func TestGenerateRefusesWithoutTheDomainItWasPlannedFor(t *testing.T) {
	if _, err := clustersecrets.Generate(repository(), clustersecrets.Cluster{AdminEmail: "operator@home.example"}); err == nil {
		t.Fatal("a manifest was generated without a domain")
	}
}
