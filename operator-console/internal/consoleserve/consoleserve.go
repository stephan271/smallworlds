// Package consoleserve wires the in-cluster Operator Console into something a
// pod can run. Everything it composes already exists and is tested on its own —
// the assessment engine, the OIDC login, the live observers, the CRD-backed
// Activity Record, the HTTP surface, the embedded client. What was missing was
// the one place that reads a deployment's configuration and connects them, which
// is why the console existed as code but was served by nothing.
//
// Two rules shape this package. Configuration is read once, at startup, and a
// missing required value fails the process rather than producing a console that
// silently cannot log anyone in. And every optional adapter that is absent
// leaves its facet unknown rather than healthy: a console that cannot see the
// cluster must say so.
package consoleserve

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/assessment"
	"github.com/stephan271/smallworlds/operator-console/internal/capability"
	"github.com/stephan271/smallworlds/operator-console/internal/console"
	"github.com/stephan271/smallworlds/operator-console/internal/consoleauth"
	"github.com/stephan271/smallworlds/operator-console/internal/consoleworkflow"
	"github.com/stephan271/smallworlds/operator-console/internal/kubeclient"
	"github.com/stephan271/smallworlds/operator-console/internal/observers"
	"github.com/stephan271/smallworlds/operator-console/internal/protection"
	"github.com/stephan271/smallworlds/operator-console/internal/webui"
)

// Settings is the console's deployment configuration. It carries no secret
// beyond the OIDC client secret and the session key, both of which arrive from
// the cluster's Secret rather than from the GitOps Overlay (ADR 0010).
type Settings struct {
	// Address is the listen address inside the pod.
	Address string
	// Issuer is the Keycloak realm URL.
	Issuer string
	// ClientID and ClientSecret identify the console's Keycloak client. Per the
	// project-wide contract they come from the keycloak-secret Secret's clientId
	// and client-secret keys.
	ClientID     string
	ClientSecret string
	// ExternalURL is the console's own address as an Operator's browser sees it,
	// used to build the OIDC redirect URI. It must match the redirect URI
	// registered on the Keycloak client exactly.
	ExternalURL string
	// BaseDomain is the Private Network base domain used for Grafana/Argo CD
	// deep links.
	BaseDomain string
	// SessionKey signs session cookies. An empty key is replaced with a random
	// one, which is safe but logs every Operator out on each restart.
	SessionKey []byte
	// Namespace holds the console's own Change Plan and Workflow Run resources.
	Namespace string
	// ArgoNamespace and RootApplication locate Argo CD's Applications.
	ArgoNamespace   string
	RootApplication string
	// GatewayEntrypoint is the Traefik entrypoint the Private Gateway serves.
	GatewayEntrypoint string
	// PublicEntrypoints are the entrypoints reachable from the internet.
	PublicEntrypoints []string
	// DeploymentMode is the cluster's deployment mode.
	DeploymentMode capability.DeploymentMode
	// EvidenceMaxAge bounds how old an observation may be before its facet is
	// treated as stale.
	EvidenceMaxAge time.Duration
	// RecoveryPointMaxAge bounds how old a Recovery Point may be before
	// protection degrades.
	RecoveryPointMaxAge time.Duration
}

// SettingsFromEnvironment reads the deployment's configuration. Required values
// missing is a startup failure: a console that cannot complete a login is worse
// than one that does not start, because only the second is visible.
func SettingsFromEnvironment() (Settings, error) {
	settings := Settings{
		Address:             valueOr("SMALLWORLDS_CONSOLE_ADDRESS", ":8080"),
		Issuer:              strings.TrimRight(os.Getenv("SMALLWORLDS_OIDC_ISSUER"), "/"),
		ClientID:            os.Getenv("SMALLWORLDS_OIDC_CLIENT_ID"),
		ClientSecret:        os.Getenv("SMALLWORLDS_OIDC_CLIENT_SECRET"),
		ExternalURL:         strings.TrimRight(os.Getenv("SMALLWORLDS_CONSOLE_URL"), "/"),
		BaseDomain:          os.Getenv("SMALLWORLDS_BASE_DOMAIN"),
		SessionKey:          []byte(os.Getenv("SMALLWORLDS_SESSION_KEY")),
		Namespace:           valueOr("SMALLWORLDS_CONSOLE_NAMESPACE", "operator-console"),
		ArgoNamespace:       valueOr("SMALLWORLDS_ARGOCD_NAMESPACE", "argocd"),
		RootApplication:     valueOr("SMALLWORLDS_ROOT_APPLICATION", "smallworlds-root"),
		GatewayEntrypoint:   valueOr("SMALLWORLDS_GATEWAY_ENTRYPOINT", "private-gateway"),
		DeploymentMode:      capability.DeploymentMode(valueOr("SMALLWORLDS_DEPLOYMENT_MODE", string(capability.Hetzner))),
		EvidenceMaxAge:      5 * time.Minute,
		RecoveryPointMaxAge: 26 * time.Hour,
	}
	if entrypoints := os.Getenv("SMALLWORLDS_PUBLIC_ENTRYPOINTS"); entrypoints != "" {
		for _, entrypoint := range strings.Split(entrypoints, ",") {
			if trimmed := strings.TrimSpace(entrypoint); trimmed != "" {
				settings.PublicEntrypoints = append(settings.PublicEntrypoints, trimmed)
			}
		}
	}
	var missing []string
	if settings.Issuer == "" {
		missing = append(missing, "SMALLWORLDS_OIDC_ISSUER")
	}
	if settings.ClientID == "" {
		missing = append(missing, "SMALLWORLDS_OIDC_CLIENT_ID")
	}
	if settings.ExternalURL == "" {
		missing = append(missing, "SMALLWORLDS_CONSOLE_URL")
	}
	if len(missing) > 0 {
		return Settings{}, fmt.Errorf("consoleserve: missing required configuration: %s", strings.Join(missing, ", "))
	}
	return settings, nil
}

func valueOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

// RedirectURI is the OIDC callback the console registers and Keycloak returns to.
func (settings Settings) RedirectURI() string {
	return settings.ExternalURL + "/api/v1/auth/callback"
}

// Server is a running in-cluster console.
type Server struct {
	Handler  http.Handler
	Settings Settings
}

// New builds the console from its deployment settings, discovering Keycloak's
// endpoints and connecting to the Kubernetes API. It returns an error rather
// than a degraded console when either is unreachable at startup: the deployment
// has a readiness probe, and a pod that cannot do its job should fail it.
func New(ctx context.Context, settings Settings) (*Server, error) {
	client, err := kubeclient.InCluster()
	if err != nil {
		return nil, err
	}
	return newWithClient(ctx, settings, client, adapters{})
}

// adapters are the pieces New builds for itself in production and the tests
// substitute: the Kubernetes client, the token exchanger, and the resolver. They
// are the only three things in this package that reach outside the process.
type adapters struct {
	exchanger  consoleauth.TokenExchanger
	lookupHost func(ctx context.Context, host string) ([]string, error)
}

// newWithClient is the seam the tests use, so the whole composition can be
// exercised against a fake API server and a fake Keycloak.
func newWithClient(ctx context.Context, settings Settings, client *kubeclient.Client, injected adapters) (*Server, error) {
	exchanger := injected.exchanger
	if exchanger == nil {
		live, err := consoleauth.NewLiveExchanger(ctx, nil, settings.Issuer, settings.ClientID, settings.ClientSecret, settings.RedirectURI())
		if err != nil {
			return nil, err
		}
		exchanger = live
	}
	endpoints, err := consoleauth.Discover(ctx, nil, settings.Issuer)
	if err != nil {
		return nil, err
	}

	catalog := capability.DefaultCatalog()
	sources := &observers.LiveSources{
		Reader:            client,
		ArgoNamespace:     settings.ArgoNamespace,
		RootApplication:   settings.RootApplication,
		Dependencies:      dependencies(catalog),
		GatewayEntrypoint: settings.GatewayEntrypoint,
		PublicEntrypoints: settings.PublicEntrypoints,
		LookupHost:        injected.lookupHost,
	}
	assessor := Assessor{
		Gatherer: observers.Gatherer{
			Configuration: sources,
			Delivery:      sources,
			Runtime:       sources,
			Access:        sources,
			Freshness: assessment.Freshness{
				Evidence:      settings.EvidenceMaxAge,
				RecoveryPoint: settings.RecoveryPointMaxAge,
			},
		},
	}

	server, err := console.New(console.Config{
		Issuer:                settings.Issuer,
		ClientID:              settings.ClientID,
		AuthorizationEndpoint: endpoints.AuthorizationEndpoint,
		RedirectURI:           settings.RedirectURI(),
		Exchanger:             exchanger,
		Assessor:              assessor,
		Catalog:               CapabilityRefs(catalog),
		RichCatalog:           catalog,
		DeploymentMode:        settings.DeploymentMode,
		BaseDomain:            settings.BaseDomain,
		SessionKey:            settings.SessionKey,
		Workflow: consoleworkflow.NewKubernetesStore(client, settings.Namespace, func(err error) bool {
			return errors.Is(err, kubeclient.ErrNotFound)
		}),
	})
	if err != nil {
		return nil, err
	}
	return &Server{Handler: withProbes(webui.NewConsole(server)), Settings: settings}, nil
}

// withProbes adds the liveness and readiness endpoints the Deployment probes.
// They are deliberately outside the Console Role gate: kubelet holds no session,
// and a probe that required one would restart a perfectly healthy pod.
func withProbes(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/healthz", "/readyz":
			response.Header().Set("Cache-Control", "no-store")
			response.WriteHeader(http.StatusOK)
			_, _ = response.Write([]byte("ok\n"))
			return
		}
		next.ServeHTTP(response, request)
	})
}

// Assessor composes the live observers with the protection inventory. Protection
// is not one of the observers' five sources because it has its own inventory of
// declared datasets; folding its evidence in here reuses that inventory instead
// of restating its aggregation rules a second time.
type Assessor struct {
	Gatherer   observers.Gatherer
	Protection interface {
		Report(ctx context.Context) []protection.DatasetProtection
	}
}

func (assessor Assessor) Assess(ctx context.Context, ref assessment.CapabilityRef) assessment.CapabilityAssessment {
	input := assessor.Gatherer.Gather(ctx, ref)
	if assessor.Protection != nil && ref.Stateful {
		input.Protection = protection.CapabilityEvidence(ref.ID, assessor.Protection.Report(ctx), input.Now)
	}
	return assessment.Assess(input)
}

func dependencies(catalog capability.Catalog) map[string][]string {
	declared := make(map[string][]string, len(catalog.Capabilities))
	for _, entry := range catalog.Capabilities {
		if len(entry.Dependencies) > 0 {
			declared[entry.ID] = entry.Dependencies
		}
	}
	return declared
}
