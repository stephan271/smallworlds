// Package clustersecrets creates the Kubernetes Secrets a SmallWorlds cluster
// cannot start without, so the Operator never has to author them by hand.
//
// Four Secrets are involved and only one of them is a password anybody chooses:
// Garage's RPC secret and admin token are machine credentials no human ever
// types, the bulk-invite secret is the same, and Argo CD's repository Secret is
// nothing but the settings repository the console already established plus the
// credential already in the Launcher Vault. Only the Keycloak and Grafana admin
// passwords are ever read by a person, and those need to be *knowable*, not
// *chosen* — so they are generated here and revealed once, exactly as
// smallworlds-init.sh has always done for the shell path.
package clustersecrets

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// Repository is Argo CD's access to the private settings repository. Without it
// the cluster installs and then reconciles nothing, so a generated manifest is
// refused rather than written half-complete.
type Repository struct {
	URL      string
	Username string
	Password string
}

// Cluster is what the manifest needs to know about the installation itself.
// DNSToken may be empty: a cluster whose names never leave the building has no
// provider to talk to, and the Secrets that carry it are written empty rather
// than omitted — the workloads that mount them refuse to start otherwise.
type Cluster struct {
	Domain               string
	EnvironmentExtension string
	AdminEmail           string
	DNSToken             string
}

// Credentials are the two logins a person actually uses. Everything else in the
// manifest is machine-to-machine and deliberately absent here.
type Credentials struct {
	KeycloakAdminUser     string `json:"keycloakAdminUser,omitempty"`
	KeycloakAdminPassword string `json:"keycloakAdminPassword,omitempty"`
	GrafanaAdminUser      string `json:"grafanaAdminUser,omitempty"`
	GrafanaAdminPassword  string `json:"grafanaAdminPassword,omitempty"`
}

// Present reports whether anything was recognised. An Operator-supplied
// manifest may name its Secrets differently, and claiming to have found
// credentials that are not there would be worse than saying nothing.
func (credentials Credentials) Present() bool {
	return credentials.KeycloakAdminPassword != "" || credentials.GrafanaAdminPassword != ""
}

// Reference names a Secret without carrying its value.
type Reference struct {
	Namespace string
	Name      string
}

type Generated struct {
	Manifest    string
	Credentials Credentials
}

const (
	keycloakSecretName = "keycloak-admin-creds"
	grafanaSecretName  = "grafana-admin-creds"
	garageSecretName   = "garage-auth-secret"
	// Not arbitrary: Renovate and the remediation agent both mount this Secret
	// by name (apps/renovate-cronjob.yaml, tenants/remediation/deployment.yaml),
	// so it is a project-wide contract rather than a label lookup.
	repositorySecretName = "repo-git-creds"
	stalwartSecretName   = "stalwart-dns-secrets"
	// Mounted by the in-cluster Operator Console
	// (tenants/operator-console/deployment.yaml). Without it the console signs
	// session cookies with a key it invents at startup, which is safe but logs
	// every Operator out on each restart — a machine credential nobody types,
	// so it belongs here with the others rather than in anybody's hands.
	consoleSessionSecret = "operator-console-session"
	dnsProviderSecret    = "hetzner"
	globalConfigName     = "smallworlds-global-config"
	adminUser            = "admin"
)

// References lists what Generate writes, in the order it writes it.
func References() []Reference {
	return []Reference{
		{Namespace: "default", Name: globalConfigName},
		{Namespace: "keycloak", Name: keycloakSecretName},
		{Namespace: "argocd", Name: repositorySecretName},
		{Namespace: "monitoring", Name: grafanaSecretName},
		{Namespace: "garage-system", Name: garageSecretName},
		{Namespace: "garage-backup-system", Name: garageSecretName},
		{Namespace: "stalwart", Name: stalwartSecretName},
		{Namespace: "cert-manager", Name: dnsProviderSecret},
	}
}

func Generate(repository Repository, cluster Cluster) (Generated, error) {
	if repository.URL == "" || repository.Username == "" || repository.Password == "" {
		return Generated{}, fmt.Errorf("cluster secrets need the settings repository and its credential")
	}
	if cluster.Domain == "" {
		return Generated{}, fmt.Errorf("cluster secrets need the domain the installation was planned for")
	}
	keycloakPassword, err := alphanumeric(32)
	if err != nil {
		return Generated{}, err
	}
	inviteSecret, err := alphanumeric(32)
	if err != nil {
		return Generated{}, err
	}
	grafanaPassword, err := alphanumeric(32)
	if err != nil {
		return Generated{}, err
	}
	rpcSecret, err := hexadecimal(32)
	if err != nil {
		return Generated{}, err
	}
	adminToken, err := hexadecimal(32)
	if err != nil {
		return Generated{}, err
	}
	// The backup instance is a separate Garage cluster, not a second bucket, and
	// it gets credentials of its own: the two must never join each other's
	// gossip, and a credential that reaches operational storage should not also
	// reach every Recovery Point (docs/adr/0048).
	backupRPCSecret, err := hexadecimal(32)
	if err != nil {
		return Generated{}, err
	}
	backupAdminToken, err := hexadecimal(32)
	if err != nil {
		return Generated{}, err
	}
	consoleSessionKey, err := hexadecimal(32)
	if err != nil {
		return Generated{}, err
	}
	// The namespaces come first and in the same file. k3s applies this manifest
	// from its auto-apply directory long before Argo CD has created anything, so
	// a Secret whose namespace does not exist yet is simply refused — the first
	// run of this generator produced twelve hundred "namespaces \"keycloak\" not
	// found" retries before Argo CD caught up. smallworlds-init.sh has always
	// interleaved them for exactly this reason. The argocd namespace is left out
	// on purpose: the Argo CD installation owns it, and this manifest should not
	// take that over.
	documents := []document{
		{APIVersion: "v1", Kind: "Namespace", Metadata: metadata{Name: "keycloak"}},
		{APIVersion: "v1", Kind: "Namespace", Metadata: metadata{Name: "monitoring"}},
		{APIVersion: "v1", Kind: "Namespace", Metadata: metadata{Name: "garage-system"}},
		{APIVersion: "v1", Kind: "Namespace", Metadata: metadata{Name: "garage-backup-system"}},
		{APIVersion: "v1", Kind: "Namespace", Metadata: metadata{Name: "stalwart"}},
		{APIVersion: "v1", Kind: "Namespace", Metadata: metadata{Name: "cert-manager"}},
		{APIVersion: "v1", Kind: "Namespace", Metadata: metadata{Name: "operator-console"}},
		// Read by the Immich and Nextcloud setup jobs for the administrator
		// address. Not overlay material: it is delivered with the Secrets because
		// the jobs that read it run before anything in Git is reconciled.
		{
			APIVersion: "v1", Kind: "ConfigMap",
			Metadata: metadata{Name: globalConfigName, Namespace: "default"},
			Data:     map[string]string{"ADMIN_EMAIL": cluster.AdminEmail, "DOMAIN": cluster.Domain, "ENV_EXT": cluster.EnvironmentExtension},
		},
		{
			APIVersion: "v1", Kind: "Secret", Type: "Opaque",
			Metadata:   metadata{Name: keycloakSecretName, Namespace: "keycloak"},
			StringData: map[string]string{"admin-password": keycloakPassword, "bulk-invite-secret": inviteSecret},
		},
		{
			APIVersion: "v1", Kind: "Secret",
			Metadata:   metadata{Name: repositorySecretName, Namespace: "argocd", Labels: map[string]string{"argocd.argoproj.io/secret-type": "repository"}},
			StringData: map[string]string{"type": "git", "url": repository.URL, "username": repository.Username, "password": repository.Password},
		},
		{
			APIVersion: "v1", Kind: "Secret", Type: "Opaque",
			Metadata:   metadata{Name: grafanaSecretName, Namespace: "monitoring"},
			StringData: map[string]string{"admin-user": adminUser, "admin-password": grafanaPassword},
		},
		{
			APIVersion: "v1", Kind: "Secret", Type: "Opaque",
			Metadata:   metadata{Name: garageSecretName, Namespace: "garage-system"},
			StringData: map[string]string{"rpcSecret": rpcSecret, "adminToken": adminToken},
		},
		// apps/garage-backup.yaml runs the chart with secret.create=false, so
		// without this the backup instance never starts — and with it down, every
		// producer in the chain (barman, Velero, pv-backup, the Nextcloud file
		// copy, the pod archive) has nowhere to write and Keycloak's own sync
		// waits behind it. An installation missing this looks healthy right up to
		// the point where somebody needs a Recovery Point.
		{
			APIVersion: "v1", Kind: "Secret", Type: "Opaque",
			Metadata:   metadata{Name: garageSecretName, Namespace: "garage-backup-system"},
			StringData: map[string]string{"rpcSecret": backupRPCSecret, "adminToken": backupAdminToken},
		},
		// Stalwart's provisioner and cert-manager's DNS01 solver mount these
		// whether or not a provider is in play. With names that stay inside the
		// building the token is simply empty — an absent Secret would leave the
		// pods in CreateContainerConfigError forever, which is a far worse way to
		// express "no external DNS" than an empty value.
		{
			APIVersion: "v1", Kind: "Secret", Type: "Opaque",
			Metadata:   metadata{Name: stalwartSecretName, Namespace: "stalwart"},
			StringData: map[string]string{"HCLOUD_TOKEN": cluster.DNSToken, "DOMAIN": cluster.Domain, "ENV_EXT": cluster.EnvironmentExtension},
		},
		{
			APIVersion: "v1", Kind: "Secret", Type: "Opaque",
			Metadata:   metadata{Name: dnsProviderSecret, Namespace: "cert-manager"},
			StringData: map[string]string{"token": cluster.DNSToken},
		},
		{
			APIVersion: "v1", Kind: "Secret", Type: "Opaque",
			Metadata:   metadata{Name: consoleSessionSecret, Namespace: "operator-console"},
			StringData: map[string]string{"session-key": consoleSessionKey},
		},
	}
	// Marshalled rather than assembled from strings: a repository credential is
	// whatever the provider issued, and a token holding a colon or a newline
	// would otherwise produce a manifest that is silently a different document.
	var manifest strings.Builder
	for _, value := range documents {
		encoded, err := yaml.Marshal(value)
		if err != nil {
			return Generated{}, fmt.Errorf("render cluster secret %q: %w", value.Metadata.Name, err)
		}
		manifest.WriteString("---\n")
		manifest.Write(encoded)
	}
	return Generated{
		Manifest:    manifest.String(),
		Credentials: Credentials{KeycloakAdminUser: adminUser, KeycloakAdminPassword: keycloakPassword, GrafanaAdminUser: adminUser, GrafanaAdminPassword: grafanaPassword},
	}, nil
}

// ReadCredentials recovers the two human logins from a stored manifest, so they
// can be shown again later without being held anywhere but the Vault. It works
// on an Operator-supplied manifest too, and reports nothing rather than
// guessing when the Secrets are named differently.
func ReadCredentials(manifest string) (Credentials, error) {
	decoder := yaml.NewDecoder(strings.NewReader(manifest))
	var credentials Credentials
	for {
		var value document
		err := decoder.Decode(&value)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Credentials{}, fmt.Errorf("read cluster secrets manifest: %w", err)
		}
		switch value.Metadata.Name {
		case keycloakSecretName:
			credentials.KeycloakAdminPassword = value.StringData["admin-password"]
			if credentials.KeycloakAdminPassword != "" {
				credentials.KeycloakAdminUser = adminUser
			}
		case grafanaSecretName:
			credentials.GrafanaAdminPassword = value.StringData["admin-password"]
			if credentials.GrafanaAdminPassword != "" {
				credentials.GrafanaAdminUser = value.StringData["admin-user"]
				if credentials.GrafanaAdminUser == "" {
					credentials.GrafanaAdminUser = adminUser
				}
			}
		}
	}
	return credentials, nil
}

type metadata struct {
	Name      string            `yaml:"name"`
	Namespace string            `yaml:"namespace,omitempty"`
	Labels    map[string]string `yaml:"labels,omitempty"`
}

type document struct {
	APIVersion string            `yaml:"apiVersion"`
	Kind       string            `yaml:"kind"`
	Metadata   metadata          `yaml:"metadata"`
	Type       string            `yaml:"type,omitempty"`
	StringData map[string]string `yaml:"stringData,omitempty"`
	Data       map[string]string `yaml:"data,omitempty"`
}

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// alphanumeric avoids punctuation on purpose: these values are pasted into
// terminals, shell variables and login forms by people, and a quoting accident
// in that path is a support call rather than a security gain — the length
// carries the entropy.
func alphanumeric(length int) (string, error) {
	// Rejection sampling rather than a modulo fold: 256 is not a multiple of 62,
	// so folding would make the first few letters measurably more likely.
	limit := byte(256 - 256%len(alphabet))
	value := make([]byte, 0, length)
	buffer := make([]byte, length)
	for len(value) < length {
		if _, err := rand.Read(buffer); err != nil {
			return "", fmt.Errorf("generate cluster secret: %w", err)
		}
		for _, source := range buffer {
			if source >= limit {
				continue
			}
			value = append(value, alphabet[int(source)%len(alphabet)])
			if len(value) == length {
				break
			}
		}
	}
	return string(value), nil
}

func hexadecimal(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate cluster secret: %w", err)
	}
	return fmt.Sprintf("%x", raw), nil
}
