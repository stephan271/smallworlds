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
	// The name is arbitrary — Argo CD finds this Secret by its label, not by
	// what it is called.
	repositorySecretName = "smallworlds-overlay-repository"
	adminUser            = "admin"
)

// References lists what Generate writes, in the order it writes it.
func References() []Reference {
	return []Reference{
		{Namespace: "keycloak", Name: keycloakSecretName},
		{Namespace: "argocd", Name: repositorySecretName},
		{Namespace: "monitoring", Name: grafanaSecretName},
		{Namespace: "garage-system", Name: garageSecretName},
	}
}

func Generate(repository Repository) (Generated, error) {
	if repository.URL == "" || repository.Username == "" || repository.Password == "" {
		return Generated{}, fmt.Errorf("cluster secrets need the settings repository and its credential")
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
