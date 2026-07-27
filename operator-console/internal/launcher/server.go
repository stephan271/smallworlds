package launcher

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/stephan271/smallworlds/operator-console/internal/bootstrapassets"
	"github.com/stephan271/smallworlds/operator-console/internal/capability"
	"github.com/stephan271/smallworlds/operator-console/internal/clusterca"
	"github.com/stephan271/smallworlds/operator-console/internal/enrollment"
	"github.com/stephan271/smallworlds/operator-console/internal/firstowner"
	"github.com/stephan271/smallworlds/operator-console/internal/gatewayaccess"
	"github.com/stephan271/smallworlds/operator-console/internal/githttps"
	"github.com/stephan271/smallworlds/operator-console/internal/github"
	"github.com/stephan271/smallworlds/operator-console/internal/handoffassessment"
	"github.com/stephan271/smallworlds/operator-console/internal/handoffverification"
	"github.com/stephan271/smallworlds/operator-console/internal/hetzner"
	"github.com/stephan271/smallworlds/operator-console/internal/hetznerprovision"
	"github.com/stephan271/smallworlds/operator-console/internal/localbootstrap"
	"github.com/stephan271/smallworlds/operator-console/internal/nodeinspect"
	"github.com/stephan271/smallworlds/operator-console/internal/offsite"
	"github.com/stephan271/smallworlds/operator-console/internal/operatordevice"
	"github.com/stephan271/smallworlds/operator-console/internal/privatenetwork"
	"github.com/stephan271/smallworlds/operator-console/internal/recovery"
	"github.com/stephan271/smallworlds/operator-console/internal/state"
	"github.com/stephan271/smallworlds/operator-console/internal/tailscaleclient"
	"github.com/stephan271/smallworlds/operator-console/internal/tofu"
	"github.com/stephan271/smallworlds/operator-console/internal/vault"
	"github.com/stephan271/smallworlds/operator-console/internal/workflow"
)

const sessionCookieName = "smallworlds_session"
const sessionLifetime = 12 * time.Hour

type Config struct {
	DataDir                 string
	LaunchToken             string
	WrappingStore           vault.WrappingStore
	GitHubClient            *github.Client
	GenericGitClient        GenericGitClient
	BootstrapAssets         *bootstrapassets.Manager
	LocalBootstrapRunner    localbootstrap.Runner
	NodeInspector           NodeInspector
	HandoffVerifier         handoffverification.Verifier
	PasskeyVerifier         firstowner.PasskeyVerifier
	OffsiteInspector        offsite.Inspector
	ClusterSecretApplier    ClusterSecretApplier
	OffsiteValidationRunner OffsiteValidationRunner
	HetznerProvider         HetznerProvider
	// HetznerReconciler applies an approved infrastructure plan. The default
	// refuses: without a verified pinned toolchain, applying with whatever
	// OpenTofu happens to be installed would break reproducibility.
	HetznerReconciler hetznerprovision.Reconciler
	// HetznerConvergence watches a provisioned node come up.
	HetznerConvergence hetznerprovision.ConvergenceObserver
	// PreserveDecommissionInspector and Executor are deliberately separate:
	// inspection is read-only and may be available before the narrowly-scoped
	// provider/local-node mutation adapter is installed.
	PreserveDecommissionInspector PreserveDecommissionInspector
	PreserveDecommissionExecutor  PreserveDecommissionExecutor
	// Full decommission has its own protection-aware inspection and narrowly
	// scoped executor. It must never reuse a preserve-data approval.
	FullDecommissionInspector FullDecommissionInspector
	FullDecommissionExecutor  FullDecommissionExecutor
}

// GenericGitClient permits deterministic Launcher contract tests while the
// production client remains an embedded Go implementation with no git binary.
type GenericGitClient interface {
	ValidateAccess(context.Context, string, string, string) error
	RemoteContainsCommit(context.Context, string, string, string, string) (bool, error)
	InitializeEmptyRemote(context.Context, string, string, string, map[string]string) (githttps.Identity, error)
	CreateProposalBranch(context.Context, string, string, string, string, map[string]string) (githttps.Proposal, error)
}

type NodeInspector interface {
	InspectSameHost(profileID string, requirements nodeinspect.Requirements) (nodeinspect.Report, nodeinspect.Assessment, error)
	InspectRemote(context.Context, nodeinspect.Target, nodeinspect.Credentials, string, string, nodeinspect.Requirements) (nodeinspect.Report, nodeinspect.Assessment, error)
}

type productionNodeInspector struct{}

func (productionNodeInspector) InspectSameHost(profileID string, requirements nodeinspect.Requirements) (nodeinspect.Report, nodeinspect.Assessment, error) {
	report, err := nodeinspect.InspectSameHost(profileID, requirements.DataDirectory)
	if err != nil {
		return nodeinspect.Report{}, nodeinspect.Assessment{}, err
	}
	return report, nodeinspect.Assess(report, requirements), nil
}

func (productionNodeInspector) InspectRemote(ctx context.Context, target nodeinspect.Target, credentials nodeinspect.Credentials, fingerprint, profileID string, requirements nodeinspect.Requirements) (nodeinspect.Report, nodeinspect.Assessment, error) {
	return nodeinspect.InspectRemote(ctx, target, credentials, fingerprint, profileID, requirements)
}

type session struct {
	csrfToken string
	expiresAt time.Time
}

type Server struct {
	launchToken string
	dataDir     string

	mu                        sync.RWMutex
	tokenUsed                 bool
	sessions                  map[string]session
	store                     *state.Store
	vault                     *vault.Vault
	workflow                  *workflow.Engine
	github                    *github.Client
	genericGit                GenericGitClient
	assets                    *bootstrapassets.Manager
	nodes                     NodeInspector
	handoff                   handoffverification.Verifier
	passkey                   firstowner.PasskeyVerifier
	offsite                   offsite.Inspector
	secrets                   ClusterSecretApplier
	offsiteValidator          OffsiteValidationRunner
	hetzner                   HetznerProvider
	hetznerProvision          *hetznerprovision.Service
	localBootstrap            *localbootstrap.Service
	decommissionInspector     PreserveDecommissionInspector
	decommissionExecutor      PreserveDecommissionExecutor
	decommissionActive        sync.Map
	fullDecommissionInspector FullDecommissionInspector
	fullDecommissionExecutor  FullDecommissionExecutor
	fullDecommissionActive    sync.Map
}

func New(config Config) (*Server, error) {
	if config.DataDir == "" {
		return nil, errors.New("data directory is required")
	}
	if config.LaunchToken == "" {
		return nil, errors.New("launch token is required")
	}

	store, err := state.Open(config.DataDir)
	if err != nil {
		return nil, err
	}
	workflowEngine := workflow.New(store)
	githubClient := config.GitHubClient
	if githubClient == nil {
		githubClient = github.New("https://api.github.com", nil)
	}
	genericGitClient := config.GenericGitClient
	if genericGitClient == nil {
		genericGitClient = githttps.New()
	}
	assetManager := config.BootstrapAssets
	if assetManager == nil {
		assetManager, err = bootstrapassets.NewManager(config.DataDir, bootstrapassets.DefaultCatalog(), nil)
		if err != nil {
			store.Close()
			return nil, err
		}
	}
	vaultStore := vault.New(config.DataDir, config.WrappingStore)
	bootstrapRunner := config.LocalBootstrapRunner
	if bootstrapRunner == nil {
		bootstrapRunner = localbootstrap.ProductionRunner{}
	}
	bootstrapService := localbootstrap.NewService(store, localbootstrap.OpenManagerAsset(assetManager), vaultStore.Load, bootstrapRunner)
	nodeInspector := config.NodeInspector
	if nodeInspector == nil {
		nodeInspector = productionNodeInspector{}
	}
	handoffVerifier := config.HandoffVerifier
	if handoffVerifier == nil {
		handoffVerifier = handoffverification.NewLiveVerifier()
	}
	passkeyVerifier := config.PasskeyVerifier
	if passkeyVerifier == nil {
		// The launcher serves the client over http on loopback, so the WebAuthn
		// relying-party id is 127.0.0.1 and only loopback origins are accepted.
		passkeyVerifier = firstowner.NewWebAuthnPasskeyVerifier("127.0.0.1", firstowner.LoopbackOriginAllowed)
	}
	offsiteInspector := config.OffsiteInspector
	if offsiteInspector == nil {
		// Without a real S3 client the launcher cannot inspect a destination
		// bucket, so it honestly reports versioning unknown (forcing an explicit
		// acknowledgement) rather than claiming versioning is enabled.
		offsiteInspector = unavailableOffsiteInspector{}
	}
	clusterSecretApplier := config.ClusterSecretApplier
	if clusterSecretApplier == nil {
		// Without a live cluster adapter the launcher cannot write a Cluster
		// Secret, so it refuses honestly rather than claiming the credentials
		// reached the cluster — the live adapter is deferred like the others.
		clusterSecretApplier = unavailableClusterSecretApplier{}
	}
	offsiteValidationRunner := config.OffsiteValidationRunner
	if offsiteValidationRunner == nil {
		// Without a live cluster adapter the launcher cannot start backup and
		// replication or read a Recovery Point, so it refuses rather than
		// fabricating evidence — the live adapter is deferred like the others.
		offsiteValidationRunner = unavailableOffsiteValidationRunner{}
	}
	hetznerProvider := config.HetznerProvider
	if hetznerProvider == nil {
		// The production provider is read-only by construction: every call it
		// makes is a GET apart from the write-authority probe, which the
		// provider itself rejects before it can create anything.
		hetznerProvider = hetzner.NewClient(hetzner.DefaultAPIBaseURL, nil)
	}
	hetznerReconciler := config.HetznerReconciler
	if hetznerReconciler == nil {
		// The pinned toolchain is not published yet, so the production
		// reconciler resolves no verified binary and the launcher refuses
		// honestly rather than reaching for an ambient tofu.
		hetznerReconciler = hetznerprovision.TofuReconciler{
			Workspaces: func(profileID string) (*tofu.Workspace, error) {
				return tofu.OpenWorkspace(config.DataDir, profileID)
			},
			Binaries: assetBinaries{manager: assetManager},
		}
	}
	hetznerConvergence := config.HetznerConvergence
	if hetznerConvergence == nil {
		hetznerConvergence = unavailableConvergenceObserver{}
	}
	decommissionInspector := config.PreserveDecommissionInspector
	if decommissionInspector == nil {
		decommissionInspector = unavailablePreserveDecommissionInspector{}
	}
	decommissionExecutor := config.PreserveDecommissionExecutor
	if decommissionExecutor == nil {
		decommissionExecutor = unavailablePreserveDecommissionExecutor{}
	}
	fullDecommissionInspector := config.FullDecommissionInspector
	if fullDecommissionInspector == nil {
		fullDecommissionInspector = unavailableFullDecommissionInspector{}
	}
	fullDecommissionExecutor := config.FullDecommissionExecutor
	if fullDecommissionExecutor == nil {
		fullDecommissionExecutor = unavailableFullDecommissionExecutor{}
	}
	workflowEngine.RegisterExecutor("BootstrapLocalNode", bootstrapService.Execute)
	server := &Server{
		launchToken:      config.LaunchToken,
		dataDir:          config.DataDir,
		sessions:         make(map[string]session),
		store:            store,
		vault:            vaultStore,
		workflow:         workflowEngine,
		github:           githubClient,
		genericGit:       genericGitClient,
		assets:           assetManager,
		nodes:            nodeInspector,
		handoff:          handoffVerifier,
		passkey:          passkeyVerifier,
		offsite:          offsiteInspector,
		secrets:          clusterSecretApplier,
		offsiteValidator: offsiteValidationRunner,

		hetzner:                   hetznerProvider,
		localBootstrap:            bootstrapService,
		decommissionInspector:     decommissionInspector,
		decommissionExecutor:      decommissionExecutor,
		fullDecommissionInspector: fullDecommissionInspector,
		fullDecommissionExecutor:  fullDecommissionExecutor,
	}
	// Registered after the server exists (the executor is a server method) and
	// before ResumeActive, so a validation run interrupted by a restart resumes.
	workflowEngine.RegisterExecutor(offsiteValidationIntent, server.executeOffsiteValidation)
	server.hetznerProvision = hetznerprovision.NewService(
		store, hetznerInspector{server: server}, hetznerReconciler, hetznerConvergence,
		vaultStore.Load, server.loadApprovedHetznerPlan, server.buildHetznerModuleInput,
	)
	workflowEngine.RegisterExecutor(hetznerProvisionIntent, server.hetznerProvision.Execute)
	workflowEngine.RegisterExecutor(preserveDecommissionIntent, server.executePreserveDecommission)
	workflowEngine.RegisterExecutor(fullDecommissionIntent, server.executeFullDecommission)
	if err := workflowEngine.ResumeActive(context.Background()); err != nil {
		store.Close()
		return nil, err
	}
	return server, nil
}

func (server *Server) Close() error {
	server.vault.Lock()
	return server.store.Close()
}

func (server *Server) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'")

	switch {
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/session/exchange":
		server.exchangeSession(response, request)
	case request.Method == http.MethodGet && request.URL.Path == "/api/v1/session":
		server.getSession(response, request)
	case request.URL.Path == "/api/v1/vault/unlock":
		server.unlockVault(response, request)
	case request.URL.Path == "/api/v1/vault":
		server.vaultStatus(response, request)
	case request.URL.Path == "/api/v1/recovery-bundles/export":
		server.exportRecoveryBundle(response, request)
	case request.URL.Path == "/api/v1/recovery-bundles/preview":
		server.previewRecoveryBundle(response, request)
	case request.URL.Path == "/api/v1/recovery-bundles/import":
		server.importRecoveryBundle(response, request)
	case request.URL.Path == "/api/v1/capabilities":
		server.capabilities(response, request)
	case request.URL.Path == "/api/v1/capabilities/plan":
		server.capabilityPlan(response, request)
	case request.URL.Path == "/api/v1/github/token/validate":
		server.validateGitHubToken(response, request)
	case request.URL.Path == "/api/v1/github/overlay/establish":
		server.establishGitHubOverlay(response, request)
	case request.URL.Path == "/api/v1/generic-git/token/validate":
		server.validateGenericGitCredentials(response, request)
	case request.URL.Path == "/api/v1/generic-git/overlay/establish":
		server.establishGenericGitOverlay(response, request)
	case request.URL.Path == "/api/v1/generic-git/overlay/propose":
		server.proposeGenericGitOverlay(response, request)
	case request.URL.Path == "/api/v1/bootstrap-assets":
		server.bootstrapAssets(response, request)
	case request.URL.Path == "/api/v1/bootstrap-assets/acquire":
		server.acquireBootstrapAssets(response, request)
	case request.URL.Path == "/api/v1/nodes/probe":
		server.probeNode(response, request)
	case request.URL.Path == "/api/v1/nodes/capabilities":
		server.nodeCapabilities(response, request)
	case request.URL.Path == "/api/v1/nodes/trust":
		server.trustNode(response, request)
	case request.URL.Path == "/api/v1/nodes/inspect":
		server.inspectNode(response, request)
	case request.URL.Path == "/api/v1/nodes/clean":
		server.cleanNode(response, request)
	case request.URL.Path == "/api/v1/nodes/ssh-key/plan":
		server.planNodeSSHKey(response, request)
	case request.URL.Path == "/api/v1/local-bootstrap/plan":
		server.planLocalBootstrap(response, request)
	case request.URL.Path == "/api/v1/cluster-secrets/credentials":
		server.revealClusterSecretCredentials(response, request)
	case request.URL.Path == "/api/v1/cluster-ca":
		server.clusterCAStatus(response, request)
	case request.URL.Path == "/api/v1/cluster-ca/establish":
		server.establishClusterCA(response, request)
	case request.URL.Path == "/api/v1/cluster-ca/device-trust":
		server.installClusterCADeviceTrust(response, request)
	case request.URL.Path == "/api/v1/private-network":
		server.privateNetworkStatus(response, request)
	case request.URL.Path == "/api/v1/private-network/establish":
		server.establishPrivateNetwork(response, request)
	case request.URL.Path == "/api/v1/tailscale-client":
		server.tailscaleClient(response, request)
	case request.URL.Path == "/api/v1/enrollment":
		server.enrollmentStatus(response, request)
	case request.URL.Path == "/api/v1/enrollment/establish":
		server.establishEnrollment(response, request)
	case request.URL.Path == "/api/v1/enrollment/launcher/consume":
		server.consumeLauncherEnrollment(response, request)
	case request.URL.Path == "/api/v1/gateway-access":
		server.gatewayAccessStatus(response, request)
	case request.URL.Path == "/api/v1/gateway-access/check":
		server.checkGatewayAccessHost(response, request)
	case request.URL.Path == "/api/v1/handoff":
		server.handoffStatus(response, request)
	case request.URL.Path == "/api/v1/handoff/verify":
		server.verifyHandoff(response, request)
	case request.URL.Path == "/api/v1/handoff/close-temporary-access":
		server.closeTemporaryAccess(response, request)
	case request.URL.Path == "/api/v1/first-owner":
		server.firstOwnerStatus(response, request)
	case request.URL.Path == "/api/v1/first-owner/claim":
		server.claimFirstOwner(response, request)
	case request.URL.Path == "/api/v1/first-owner/register":
		server.registerFirstOwner(response, request)
	case request.URL.Path == "/api/v1/handoff-assessment":
		server.handoffAssessment(response, request)
	case request.URL.Path == "/api/v1/cluster-detail":
		server.clusterDetail(response, request)
	case request.URL.Path == "/api/v1/hetzner":
		server.hetznerStatus(response, request)
	case request.URL.Path == "/api/v1/hetzner/token/validate":
		server.validateHetznerToken(response, request)
	case request.URL.Path == "/api/v1/hetzner/inspect":
		server.inspectHetzner(response, request)
	case request.URL.Path == "/api/v1/hetzner/toolchain/acquire":
		server.acquireHetznerToolchain(response, request)
	case request.URL.Path == "/api/v1/hetzner/temporary-access/narrow":
		server.hetznerTemporaryAccess(response, request)
	case request.URL.Path == "/api/v1/hetzner/presets":
		server.hetznerPresets(response, request)
	case request.URL.Path == "/api/v1/hetzner/plan":
		server.planHetzner(response, request)
	case request.URL.Path == "/api/v1/offsite":
		server.offsiteStatus(response, request)
	case request.URL.Path == "/api/v1/offsite/inspect":
		server.inspectOffsite(response, request)
	case request.URL.Path == "/api/v1/offsite/plan":
		server.planOffsite(response, request)
	case request.URL.Path == "/api/v1/offsite/propose":
		server.proposeOffsite(response, request)
	case request.URL.Path == "/api/v1/offsite/validate":
		server.validateOffsite(response, request)
	case request.URL.Path == "/api/v1/decommission":
		server.decommissionStatus(response, request)
	case request.URL.Path == "/api/v1/decommission/inspect":
		server.inspectDecommission(response, request)
	case request.URL.Path == "/api/v1/decommission/plan":
		server.planPreserveDecommission(response, request)
	case strings.HasPrefix(request.URL.Path, "/api/v1/decommission/runs/"):
		server.resumePreserveDecommission(response, request)
	case request.URL.Path == "/api/v1/full-decommission":
		server.fullDecommissionStatus(response, request)
	case request.URL.Path == "/api/v1/full-decommission/plan":
		server.planFullDecommission(response, request)
	case request.URL.Path == "/api/v1/full-decommission/approve":
		server.approveFullDecommission(response, request)
	case request.URL.Path == "/api/v1/full-decommission/activity":
		server.exportFullDecommissionActivity(response, request)
	case strings.HasPrefix(request.URL.Path, "/api/v1/full-decommission/runs/"):
		server.resumeFullDecommission(response, request)
	case request.URL.Path == "/api/v1/profiles":
		server.profiles(response, request)
	case strings.HasPrefix(request.URL.Path, "/api/v1/profiles/"):
		server.profile(response, request)
	case request.URL.Path == "/api/v1/plans":
		server.plans(response, request)
	case strings.HasPrefix(request.URL.Path, "/api/v1/plans/"):
		server.plan(response, request)
	case strings.HasPrefix(request.URL.Path, "/api/v1/runs/"):
		server.run(response, request)
	case request.URL.Path == "/api/v1/events":
		server.events(response, request)
	default:
		http.NotFound(response, request)
	}
}

func (server *Server) previewRecoveryBundle(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		Bundle     string `json:"bundle"`
		Passphrase string `json:"passphrase"`
		Identity   string `json:"identity"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 24*1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Bundle == "" || !validRecoveryCredential(input.Passphrase, input.Identity) {
		writeError(response, http.StatusBadRequest, "invalid_recovery_bundle")
		return
	}
	bundle, err := base64.StdEncoding.DecodeString(input.Bundle)
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_recovery_bundle")
		return
	}
	payload, err := openRecoveryBundle(bundle, input.Passphrase, input.Identity)
	if errors.Is(err, recovery.ErrCannotDecrypt) {
		writeError(response, http.StatusUnauthorized, "recovery_bundle_credentials_incorrect")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_recovery_bundle")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"format":  payload.Format,
		"version": payload.Version,
		"profile": map[string]string{
			"id":             payload.Profile.ID,
			"name":           payload.Profile.Name,
			"deploymentMode": payload.Profile.DeploymentMode,
		},
	})
}

func (server *Server) exportRecoveryBundle(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID  string   `json:"profileId"`
		Passphrase string   `json:"passphrase"`
		Recipients []string `json:"recipients"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" || !validRecoveryExportCredential(input.Passphrase, input.Recipients) {
		writeError(response, http.StatusBadRequest, "invalid_recovery_bundle_export")
		return
	}
	snapshot, err := server.store.ExportProfileSnapshot(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "recovery_bundle_export_failed")
		return
	}
	vaultMaterial, err := server.vault.ExportPrefix(input.ProfileID + "/")
	if errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "recovery_bundle_export_failed")
		return
	}
	payload := recovery.Payload{
		Format:  "smallworlds-recovery-bundle",
		Version: 1,
		Profile: snapshot.Profile,
		WorkflowSnapshot: recovery.WorkflowSnapshot{
			Plans:           snapshot.Plans,
			Runs:            snapshot.Runs,
			Events:          snapshot.Events,
			BootstrapPlans:  snapshot.BootstrapPlans,
			OverlayIdentity: snapshot.OverlayIdentity,
			NodeTrust:       snapshot.NodeTrust,
		},
		InfrastructureState:  json.RawMessage(`{}`),
		VaultMaterial:        vaultMaterial,
		CredentialReferences: snapshot.CredentialReferences,
	}
	var bundle []byte
	if input.Passphrase != "" {
		bundle, err = recovery.ExportWithPassphrase(payload, input.Passphrase)
	} else {
		bundle, err = recovery.ExportWithRecipients(payload, input.Recipients)
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "recovery_bundle_export_failed")
		return
	}
	response.Header().Set("Content-Type", "application/octet-stream")
	response.Header().Set("Content-Disposition", `attachment; filename="smallworlds-recovery.bundle"`)
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(bundle)
}

func (server *Server) importRecoveryBundle(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		Bundle            string `json:"bundle"`
		Passphrase        string `json:"passphrase"`
		Identity          string `json:"identity"`
		ExpectedProfileID string `json:"expectedProfileId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 24*1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Bundle == "" || input.ExpectedProfileID == "" || !validRecoveryCredential(input.Passphrase, input.Identity) {
		writeError(response, http.StatusBadRequest, "invalid_recovery_bundle")
		return
	}
	bundle, err := base64.StdEncoding.DecodeString(input.Bundle)
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_recovery_bundle")
		return
	}
	payload, err := openRecoveryBundle(bundle, input.Passphrase, input.Identity)
	if errors.Is(err, recovery.ErrCannotDecrypt) {
		writeError(response, http.StatusUnauthorized, "recovery_bundle_credentials_incorrect")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_recovery_bundle")
		return
	}
	if !sameToken(payload.Profile.ID, input.ExpectedProfileID) {
		writeError(response, http.StatusConflict, "recovery_bundle_identity_mismatch")
		return
	}
	snapshot := state.ProfileSnapshot{
		Profile:              payload.Profile,
		Plans:                payload.WorkflowSnapshot.Plans,
		Runs:                 payload.WorkflowSnapshot.Runs,
		Events:               payload.WorkflowSnapshot.Events,
		BootstrapPlans:       payload.WorkflowSnapshot.BootstrapPlans,
		OverlayIdentity:      payload.WorkflowSnapshot.OverlayIdentity,
		NodeTrust:            payload.WorkflowSnapshot.NodeTrust,
		CredentialReferences: payload.CredentialReferences,
	}
	if err := server.store.CanImportProfileSnapshot(request.Context(), snapshot); errors.Is(err, state.ErrConflict) {
		writeError(response, http.StatusConflict, "lifecycle_authority_already_exists")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "recovery_bundle_import_failed")
		return
	}
	if err := server.vault.Import(payload.VaultMaterial); errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	} else if errors.Is(err, vault.ErrSecretConflict) {
		writeError(response, http.StatusConflict, "recovery_bundle_vault_conflict")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "recovery_bundle_import_failed")
		return
	}
	if err := server.store.ImportProfileSnapshot(request.Context(), snapshot); err != nil {
		_ = server.vault.RemoveImported(payload.VaultMaterial)
		if errors.Is(err, state.ErrConflict) {
			writeError(response, http.StatusConflict, "lifecycle_authority_already_exists")
			return
		}
		writeError(response, http.StatusInternalServerError, "recovery_bundle_import_failed")
		return
	}
	if err := server.workflow.ResumeActive(request.Context()); err != nil {
		writeError(response, http.StatusInternalServerError, "recovery_bundle_import_failed")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{
		"profile": map[string]string{
			"id":   payload.Profile.ID,
			"name": payload.Profile.Name,
		},
	})
}

func validRecoveryCredential(passphrase, identity string) bool {
	return (utf8.RuneCountInString(passphrase) >= 12 && identity == "") || (passphrase == "" && identity != "")
}

func validRecoveryExportCredential(passphrase string, recipients []string) bool {
	return (utf8.RuneCountInString(passphrase) >= 12 && len(recipients) == 0) || (passphrase == "" && len(recipients) > 0)
}

func openRecoveryBundle(bundle []byte, passphrase, identity string) (recovery.Payload, error) {
	if passphrase != "" {
		return recovery.OpenWithPassphrase(bundle, passphrase)
	}
	return recovery.OpenWithIdentity(bundle, identity)
}

func (server *Server) unlockVault(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		Method     string `json:"method"`
		Passphrase string `json:"passphrase"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_vault_unlock")
		return
	}
	var status vault.Status
	var err error
	switch input.Method {
	case "passphrase":
		if input.Passphrase == "" {
			writeError(response, http.StatusBadRequest, "invalid_vault_unlock")
			return
		}
		if utf8.RuneCountInString(input.Passphrase) < 12 {
			writeError(response, http.StatusBadRequest, "vault_passphrase_too_short")
			return
		}
		status, err = server.vault.UnlockWithPassphrase(request.Context(), input.Passphrase)
	case "operating-system":
		if input.Passphrase != "" {
			writeError(response, http.StatusBadRequest, "invalid_vault_unlock")
			return
		}
		status, err = server.vault.UnlockWithOSCredentialStore(request.Context())
	default:
		writeError(response, http.StatusBadRequest, "invalid_vault_unlock")
		return
	}
	if errors.Is(err, vault.ErrIncorrectPassphrase) {
		writeError(response, http.StatusUnauthorized, "vault_passphrase_incorrect")
		return
	}
	if errors.Is(err, vault.ErrCredentialStoreUnavailable) {
		writeError(response, http.StatusServiceUnavailable, "os_credential_store_unavailable")
		return
	}
	if errors.Is(err, vault.ErrWrappingKeyMissing) {
		writeError(response, http.StatusConflict, "vault_wrapping_key_missing")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "vault_unlock_failed")
		return
	}
	if err := server.workflow.ResumeActive(request.Context()); err != nil {
		writeError(response, http.StatusInternalServerError, "workflow_resume_failed")
		return
	}
	writeJSON(response, http.StatusOK, status)
}

func (server *Server) vaultStatus(response http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedSession(request); !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	writeJSON(response, http.StatusOK, server.vault.Status(request.Context()))
}

type workflowEvent struct {
	ID         int64          `json:"id"`
	ProfileID  string         `json:"profileId"`
	RunID      string         `json:"runId"`
	Type       string         `json:"type"`
	MessageKey string         `json:"messageKey"`
	Parameters map[string]any `json:"parameters"`
	OccurredAt string         `json:"occurredAt"`
}

func (server *Server) events(response http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedSession(request); !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	profileID := request.URL.Query().Get("profileId")
	if profileID == "" {
		writeError(response, http.StatusBadRequest, "profile_required")
		return
	}
	cursorText := request.URL.Query().Get("cursor")
	if headerCursor := request.Header.Get("Last-Event-ID"); headerCursor != "" {
		cursorText = headerCursor
	}
	var cursor int64
	if cursorText != "" {
		parsed, err := strconv.ParseInt(cursorText, 10, 64)
		if err != nil || parsed < 0 {
			writeError(response, http.StatusBadRequest, "invalid_event_cursor")
			return
		}
		cursor = parsed
	}
	events, err := server.store.ListEvents(request.Context(), profileID, cursor)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "events_unavailable")
		return
	}
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Accel-Buffering", "no")
	response.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(response, "retry: 1000\n\n")
	for _, record := range events {
		parameters := make(map[string]any)
		if err := json.Unmarshal([]byte(record.Parameters), &parameters); err != nil {
			continue
		}
		payload, err := json.Marshal(workflowEvent{
			ID:         record.ID,
			ProfileID:  record.ProfileID,
			RunID:      record.RunID,
			Type:       record.Type,
			MessageKey: record.MessageKey,
			Parameters: parameters,
			OccurredAt: record.OccurredAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		})
		if err != nil {
			continue
		}
		_, _ = fmt.Fprintf(response, "id: %d\nevent: workflow\ndata: %s\n\n", record.ID, payload)
	}
}

func (server *Server) run(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, "/api/v1/runs/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(response, request)
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" && request.Method == http.MethodPost {
		if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
			writeError(response, http.StatusForbidden, "csrf_required")
			return
		}
		run, err := server.workflow.Cancel(request.Context(), parts[0])
		if errors.Is(err, state.ErrNotFound) {
			writeError(response, http.StatusConflict, "run_not_cancellable")
			return
		}
		if err != nil {
			writeError(response, http.StatusInternalServerError, "run_cancellation_failed")
			return
		}
		writeJSON(response, http.StatusAccepted, run)
		return
	}
	if len(parts) != 1 || request.Method != http.MethodGet {
		http.NotFound(response, request)
		return
	}
	run, err := server.workflow.GetRun(request.Context(), parts[0])
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "run_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "run_unavailable")
		return
	}
	writeJSON(response, http.StatusOK, run)
}

// clusterCAMaterial is the launcher-owned JSON persisted for a profile's
// Cluster CA. It holds only public certificate material and secret-free
// metadata; the private root and intermediate keys live in the Launcher Vault.
type clusterCAMaterial struct {
	Reference                  clusterca.Reference `json:"reference"`
	RootCertificatePEM         string              `json:"rootCertificatePem"`
	IntermediateCertificatePEM string              `json:"intermediateCertificatePem"`
}

func clusterCARootKeyVaultKey(profileID string) string { return profileID + "/cluster-ca-root-key" }

func clusterCAIntermediateKeyVaultKey(profileID string) string {
	return profileID + "/cluster-ca-intermediate-key"
}

func writeClusterCAReference(response http.ResponseWriter, status int, reference clusterca.Reference, installed bool) {
	writeJSON(response, status, map[string]any{
		"reference":            reference,
		"rootFingerprint":      reference.RootFingerprint,
		"deviceTrustInstalled": installed,
	})
}

func (server *Server) establishClusterCA(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID string `json:"profileId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_cluster_ca_request")
		return
	}
	profile, err := server.store.GetProfile(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "cluster_ca_failed")
		return
	}
	// The private Cluster CA exists specifically because a LAN-only cluster has
	// no publicly registered domain; internet-exposed and Hetzner modes use
	// publicly trusted ACME certificates instead.
	if profile.DeploymentMode != "local-lan" {
		writeError(response, http.StatusConflict, "cluster_ca_lan_only")
		return
	}
	// Resumable: a previously established authority is returned unchanged so the
	// root identity — and therefore any installed device trust — stays stable.
	if existing, existingErr := server.store.GetClusterCAReference(request.Context(), input.ProfileID); existingErr == nil {
		var material clusterCAMaterial
		if json.Unmarshal([]byte(existing.Material), &material) != nil {
			writeError(response, http.StatusInternalServerError, "cluster_ca_failed")
			return
		}
		writeClusterCAReference(response, http.StatusOK, material.Reference, existing.DeviceTrustInstalled)
		return
	} else if !errors.Is(existingErr, state.ErrNotFound) {
		writeError(response, http.StatusInternalServerError, "cluster_ca_failed")
		return
	}
	now := time.Now().UTC()
	authority, err := clusterca.CreateAuthority(input.ProfileID, now)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "cluster_ca_failed")
		return
	}
	intermediate, err := authority.IssueIntermediate(now)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "cluster_ca_failed")
		return
	}
	rootKeyPEM, err := authority.RootPrivateKeyPEM()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "cluster_ca_failed")
		return
	}
	intermediateKeyPEM, err := intermediate.PrivateKeyPEM()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "cluster_ca_failed")
		return
	}
	// The root key stays with the Lifecycle Authority; the intermediate key is
	// the only signing key destined for the cluster. Both are held in the
	// Launcher Vault and never returned to the browser.
	if err := server.vault.Store(clusterCARootKeyVaultKey(input.ProfileID), rootKeyPEM); errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "cluster_ca_storage_failed")
		return
	}
	if err := server.vault.Store(clusterCAIntermediateKeyVaultKey(input.ProfileID), intermediateKeyPEM); errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "cluster_ca_storage_failed")
		return
	}
	reference := authority.Reference(intermediate)
	encoded, err := json.Marshal(clusterCAMaterial{Reference: reference, RootCertificatePEM: authority.RootCertificatePEM(), IntermediateCertificatePEM: intermediate.CertificatePEM()})
	if err != nil {
		writeError(response, http.StatusInternalServerError, "cluster_ca_failed")
		return
	}
	if err := server.store.RecordClusterCAReference(request.Context(), state.ClusterCAReference{ProfileID: input.ProfileID, Material: string(encoded), DeviceTrustInstalled: false, RecordedAt: now}); err != nil {
		writeError(response, http.StatusInternalServerError, "cluster_ca_failed")
		return
	}
	for _, entry := range []struct {
		kind     string
		vaultKey string
		expires  time.Time
	}{
		{"cluster-ca-root-key", clusterCARootKeyVaultKey(input.ProfileID), reference.RootNotAfter},
		{"cluster-ca-intermediate-key", clusterCAIntermediateKeyVaultKey(input.ProfileID), reference.IntermediateNotAfter},
	} {
		if err := server.store.UpsertCredentialReference(request.Context(), state.CredentialReference{ProfileID: input.ProfileID, Kind: entry.kind, VaultKey: entry.vaultKey, Source: "launcher", ExpiresAt: entry.expires, RotationStatus: credentialRotationStatus(entry.expires, now)}); err != nil {
			writeError(response, http.StatusInternalServerError, "cluster_ca_failed")
			return
		}
	}
	writeClusterCAReference(response, http.StatusCreated, reference, false)
}

func (server *Server) clusterCAStatus(response http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedSession(request); !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	profileID := request.URL.Query().Get("profileId")
	if profileID == "" {
		writeError(response, http.StatusBadRequest, "profile_required")
		return
	}
	record, err := server.store.GetClusterCAReference(request.Context(), profileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "cluster_ca_not_established")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "cluster_ca_failed")
		return
	}
	var material clusterCAMaterial
	if json.Unmarshal([]byte(record.Material), &material) != nil {
		writeError(response, http.StatusInternalServerError, "cluster_ca_failed")
		return
	}
	writeClusterCAReference(response, http.StatusOK, material.Reference, record.DeviceTrustInstalled)
}

func (server *Server) installClusterCADeviceTrust(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID string `json:"profileId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_cluster_ca_request")
		return
	}
	record, err := server.store.GetClusterCAReference(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "cluster_ca_not_established")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "cluster_ca_failed")
		return
	}
	var material clusterCAMaterial
	if json.Unmarshal([]byte(record.Material), &material) != nil {
		writeError(response, http.StatusInternalServerError, "cluster_ca_failed")
		return
	}
	if err := server.store.MarkClusterCADeviceTrustInstalled(request.Context(), input.ProfileID); err != nil {
		writeError(response, http.StatusInternalServerError, "cluster_ca_failed")
		return
	}
	// The root certificate is public trust material the Operator installs on the
	// current device; it carries no private key.
	writeJSON(response, http.StatusOK, map[string]any{
		"profileId":            input.ProfileID,
		"rootCertificatePem":   material.RootCertificatePEM,
		"fingerprint":          material.Reference.RootFingerprint,
		"subject":              material.Reference.RootSubject,
		"notAfter":             material.Reference.RootNotAfter,
		"deviceTrustInstalled": true,
	})
}

func (server *Server) handoffAssessment(response http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedSession(request); !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	profileID := request.URL.Query().Get("profileId")
	if profileID == "" {
		writeError(response, http.StatusBadRequest, "profile_required")
		return
	}
	ctx := request.Context()
	profile, err := server.store.GetProfile(ctx, profileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "handoff_assessment_failed")
		return
	}
	mode := handoffAssessmentMode(profile.DeploymentMode)
	var inputs handoffassessment.Inputs

	if record, err := server.store.GetClusterCAReference(ctx, profileID); err == nil {
		inputs.DeviceTrustInstalled = record.DeviceTrustInstalled
	} else if !errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusInternalServerError, "handoff_assessment_failed")
		return
	}

	if record, err := server.store.GetPrivateNetwork(ctx, profileID); err == nil {
		if reference, parseErr := privatenetwork.ParseReference(record.Reference); parseErr == nil {
			inputs.PrivateNetworkReady = true
			for _, endpoint := range reference.OperatorEndpoints {
				if endpoint.Name == "console" {
					inputs.ConsoleHost = endpoint.FQDN
				}
			}
			if _, policyErr := server.gatewayPolicy(request, profileID); policyErr == nil {
				inputs.GatewayAccessEnforced = true
			}
		}
	} else if !errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusInternalServerError, "handoff_assessment_failed")
		return
	}

	if record, err := server.store.GetEnrollment(ctx, profileID); err == nil {
		if reference, parseErr := enrollment.ParseReference(record.Reference); parseErr == nil {
			inputs.GatewayIdentityReady = true
			inputs.LauncherEnrolled = reference.Launcher.Used
		}
	} else if !errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusInternalServerError, "handoff_assessment_failed")
		return
	}

	if record, err := server.store.GetHandoffState(ctx, profileID); err == nil {
		var report handoffverification.Report
		if json.Unmarshal([]byte(record.Report), &report) == nil {
			inputs.HandoffVerified = report.Verified
		}
		inputs.TemporaryAccessClosed = record.Closed
	} else if !errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusInternalServerError, "handoff_assessment_failed")
		return
	}

	if record, err := server.store.GetFirstOwnerState(ctx, profileID); err == nil {
		if ownerState, parseErr := firstowner.ParseState(record.State); parseErr == nil {
			inputs.OwnerRegistered = ownerState.OwnerRegistered
			inputs.BootstrapGrantDisabled = ownerState.BootstrapGrantDisabled
		}
	} else if !errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusInternalServerError, "handoff_assessment_failed")
		return
	}

	assessment, err := handoffassessment.Evaluate(mode, inputs)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "handoff_assessment_failed")
		return
	}
	writeJSON(response, http.StatusOK, assessment)
}

// clusterDetail reports what the installed cluster is doing, in the cluster's
// own words. It is read-only and deliberately reachable while a run is in
// flight: a run parked at awaiting-convergence tells an Operator that something
// is not finished, and nothing whatever about what.
//
// Every failure here is an ordinary answer rather than an error the interface
// has to apologise for — there may be no installation yet, the vault may be
// locked, the machine may be unreachable. Each of those is a true and useful
// thing to say, so each is reported as such.
func (server *Server) clusterDetail(response http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedSession(request); !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	profileID := request.URL.Query().Get("profileId")
	if profileID == "" {
		writeError(response, http.StatusBadRequest, "profile_required")
		return
	}
	if _, err := server.store.GetProfile(request.Context(), profileID); errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "cluster_detail_unavailable")
		return
	}
	if server.localBootstrap == nil {
		writeError(response, http.StatusConflict, "cluster_not_installed")
		return
	}
	detail, err := server.localBootstrap.Detail(request.Context(), profileID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		writeError(response, http.StatusConflict, "cluster_not_installed")
		return
	case errors.Is(err, vault.ErrLocked):
		writeError(response, http.StatusLocked, "vault_locked")
		return
	case errors.Is(err, localbootstrap.ErrExecutionPrecondition):
		writeError(response, http.StatusConflict, "cluster_detail_precondition_changed")
		return
	case err != nil:
		writeError(response, http.StatusBadGateway, "cluster_detail_unreachable")
		return
	}
	writeJSON(response, http.StatusOK, detail)
}

func (server *Server) claimFirstOwner(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID string `json:"profileId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_first_owner_request")
		return
	}
	if _, err := server.store.GetProfile(request.Context(), input.ProfileID); errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "first_owner_failed")
		return
	}
	// A permanently disabled bootstrap grant can never issue a new claim.
	if existing, existingErr := server.store.GetFirstOwnerState(request.Context(), input.ProfileID); existingErr == nil {
		if current, parseErr := firstowner.ParseState(existing.State); parseErr == nil && current.BootstrapGrantDisabled {
			writeError(response, http.StatusConflict, "bootstrap_grant_disabled")
			return
		}
	} else if !errors.Is(existingErr, state.ErrNotFound) {
		writeError(response, http.StatusInternalServerError, "first_owner_failed")
		return
	}
	ownerState, err := firstowner.Plan(time.Now().UTC())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "first_owner_failed")
		return
	}
	encoded, err := ownerState.Marshal()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "first_owner_failed")
		return
	}
	if err := server.store.RecordFirstOwnerState(request.Context(), state.FirstOwnerState{ProfileID: input.ProfileID, State: encoded, RecordedAt: time.Now().UTC()}); err != nil {
		writeError(response, http.StatusInternalServerError, "first_owner_failed")
		return
	}
	writeJSON(response, http.StatusCreated, ownerState)
}

func (server *Server) registerFirstOwner(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID         string `json:"profileId"`
		CredentialID      string `json:"credentialId"`
		ClientDataJSON    string `json:"clientDataJson"`
		AttestationObject string `json:"attestationObject"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_first_owner_request")
		return
	}
	record, err := server.store.GetFirstOwnerState(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusConflict, "first_owner_claim_required")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "first_owner_failed")
		return
	}
	ownerState, err := firstowner.ParseState(record.State)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "first_owner_failed")
		return
	}
	if ownerState.BootstrapGrantDisabled {
		writeError(response, http.StatusConflict, "bootstrap_grant_disabled")
		return
	}
	credentialID, err := server.passkey.Verify(request.Context(), ownerState.Claim.Challenge, firstowner.Registration{CredentialID: input.CredentialID, ClientDataJSON: input.ClientDataJSON, AttestationObject: input.AttestationObject})
	if errors.Is(err, firstowner.ErrChallengeMismatch) {
		writeError(response, http.StatusConflict, "passkey_challenge_mismatch")
		return
	}
	if errors.Is(err, firstowner.ErrInvalidRegistration) {
		writeError(response, http.StatusBadRequest, "invalid_passkey_registration")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadGateway, "passkey_verification_failed")
		return
	}
	registered, err := ownerState.RegisterOwner(time.Now().UTC(), credentialID)
	if errors.Is(err, firstowner.ErrClaimExpired) {
		writeError(response, http.StatusConflict, "first_owner_claim_expired")
		return
	}
	if errors.Is(err, firstowner.ErrGrantAlreadyDisabled) {
		writeError(response, http.StatusConflict, "bootstrap_grant_disabled")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "first_owner_failed")
		return
	}
	encoded, err := registered.Marshal()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "first_owner_failed")
		return
	}
	if err := server.store.RecordFirstOwnerState(request.Context(), state.FirstOwnerState{ProfileID: input.ProfileID, State: encoded, RecordedAt: time.Now().UTC()}); err != nil {
		writeError(response, http.StatusInternalServerError, "first_owner_failed")
		return
	}
	writeJSON(response, http.StatusOK, registered)
}

func (server *Server) firstOwnerStatus(response http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedSession(request); !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	profileID := request.URL.Query().Get("profileId")
	if profileID == "" {
		writeError(response, http.StatusBadRequest, "profile_required")
		return
	}
	record, err := server.store.GetFirstOwnerState(request.Context(), profileID)
	if errors.Is(err, state.ErrNotFound) {
		writeJSON(response, http.StatusOK, map[string]any{"ownerRegistered": false, "bootstrapGrantDisabled": false})
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "first_owner_failed")
		return
	}
	ownerState, err := firstowner.ParseState(record.State)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "first_owner_failed")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ownerRegistered": ownerState.OwnerRegistered, "bootstrapGrantDisabled": ownerState.BootstrapGrantDisabled, "claim": ownerState.Claim})
}

// handoffTarget assembles the verification target from the Cluster CA, Private
// Network, and enrollment already established for a profile. A non-empty
// prerequisite code names the first missing dependency.
// privateNetworkShape maps a profile's Deployment Mode onto the Private Network
// shape it uses. Only the LAN-only mode keeps coordination private; every
// publicly addressed installation publishes it so an Operator can join a device
// from outside the local network.
func privateNetworkShape(deploymentMode string) privatenetwork.Shape {
	switch deploymentMode {
	case "local-lan":
		return privatenetwork.LANOnly
	case "hetzner", "local-public":
		return privatenetwork.PublicCoordination
	default:
		return privatenetwork.Shape("")
	}
}

// handoffAssessmentMode maps a Deployment Mode onto the assessment shape, which
// decides which steps apply and which limitations the Operator is told about.
func handoffAssessmentMode(deploymentMode string) handoffassessment.Mode {
	if deploymentMode == "local-lan" {
		return handoffassessment.LANOnly
	}
	return handoffassessment.PubliclyAddressed
}

// publishedHostnames are the names an installation genuinely publishes in public
// DNS. The Private Network is validated against them so an operator interface
// can never share a name with a record that has a public route to it.
func publishedHostnames(domain string) []string {
	hosts := make([]string, 0, len(hetzner.DefaultRecordNames)+1)
	hosts = append(hosts, domain)
	for _, record := range hetzner.DefaultRecordNames {
		hosts = append(hosts, record+"."+domain)
	}
	return hosts
}

func (server *Server) handoffTarget(request *http.Request, profileID string) (handoffverification.Target, string, error) {
	ctx := request.Context()
	profile, err := server.store.GetProfile(ctx, profileID)
	if errors.Is(err, state.ErrNotFound) {
		return handoffverification.Target{}, "profile_not_found", nil
	}
	if err != nil {
		return handoffverification.Target{}, "", err
	}
	// A LAN-only installation's operator interfaces are signed by the private
	// Cluster CA, so verification pins that root. A publicly addressed one holds
	// publicly trusted ACME certificates and has no root to pin — verifying
	// against the device's own trust store is what proves the Operator's browser
	// will accept them.
	anchor := handoffverification.PublicTrust
	var material clusterCAMaterial
	if operatordevice.DeploymentMode(profile.DeploymentMode).RequiresClusterCATrust() {
		anchor = handoffverification.ClusterCARoot
		caRecord, caErr := server.store.GetClusterCAReference(ctx, profileID)
		if errors.Is(caErr, state.ErrNotFound) {
			return handoffverification.Target{}, "cluster_ca_required", nil
		}
		if caErr != nil {
			return handoffverification.Target{}, "", caErr
		}
		if json.Unmarshal([]byte(caRecord.Material), &material) != nil {
			return handoffverification.Target{}, "", fmt.Errorf("decode cluster CA material")
		}
	}
	netRecord, err := server.store.GetPrivateNetwork(ctx, profileID)
	if errors.Is(err, state.ErrNotFound) {
		return handoffverification.Target{}, "private_network_required", nil
	}
	if err != nil {
		return handoffverification.Target{}, "", err
	}
	networkReference, err := privatenetwork.ParseReference(netRecord.Reference)
	if err != nil {
		return handoffverification.Target{}, "", err
	}
	enrollmentRecord, err := server.store.GetEnrollment(ctx, profileID)
	if errors.Is(err, state.ErrNotFound) {
		return handoffverification.Target{}, "enrollment_required", nil
	}
	if err != nil {
		return handoffverification.Target{}, "", err
	}
	enrollmentReference, err := enrollment.ParseReference(enrollmentRecord.Reference)
	if err != nil {
		return handoffverification.Target{}, "", err
	}
	hosts := make([]string, 0, len(networkReference.OperatorEndpoints))
	for _, endpoint := range networkReference.OperatorEndpoints {
		hosts = append(hosts, endpoint.FQDN)
	}
	// The identity provider serves the community's own domain in every mode, so
	// it comes from the overlay rather than the Private Network.
	overlay, err := server.store.GetOverlayIdentity(ctx, profileID)
	if errors.Is(err, state.ErrNotFound) || err == nil && overlay.Domain == "" {
		return handoffverification.Target{}, "gitops_overlay_required", nil
	}
	if err != nil {
		return handoffverification.Target{}, "", err
	}
	target := handoffverification.Target{
		Anchor:                  anchor,
		IdentityIssuerURL:       "https://identity." + overlay.Domain + "/realms/smallworlds",
		BaseDomain:              networkReference.BaseDomain,
		GatewayHostname:         networkReference.GatewayHostname,
		OperatorHosts:           hosts,
		RootFingerprint:         material.Reference.RootFingerprint,
		RootCertificatePEM:      material.RootCertificatePEM,
		GatewayIdentityHostname: enrollmentReference.Gateway.Hostname,
	}
	if err := target.Validate(); err != nil {
		return handoffverification.Target{}, "", err
	}
	return target, "", nil
}

// runHandoffVerification builds the target, observes it, and returns the report.
// It writes the appropriate error response and returns ok=false on failure.
func (server *Server) runHandoffVerification(response http.ResponseWriter, request *http.Request, profileID string) (handoffverification.Report, bool) {
	target, missing, err := server.handoffTarget(request, profileID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "handoff_verification_failed")
		return handoffverification.Report{}, false
	}
	if missing != "" {
		writeError(response, http.StatusConflict, missing)
		return handoffverification.Report{}, false
	}
	observations, err := server.handoff.Observe(request.Context(), target)
	if errors.Is(err, handoffverification.ErrVerificationUnavailable) {
		writeError(response, http.StatusServiceUnavailable, "handoff_verification_unavailable")
		return handoffverification.Report{}, false
	}
	if err != nil {
		writeError(response, http.StatusBadGateway, "handoff_verification_failed")
		return handoffverification.Report{}, false
	}
	report := handoffverification.Evaluate(observations)
	encoded, err := report.Marshal()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "handoff_verification_failed")
		return handoffverification.Report{}, false
	}
	closed := false
	if existing, existingErr := server.store.GetHandoffState(request.Context(), profileID); existingErr == nil {
		closed = existing.Closed
	} else if !errors.Is(existingErr, state.ErrNotFound) {
		writeError(response, http.StatusInternalServerError, "handoff_verification_failed")
		return handoffverification.Report{}, false
	}
	if err := server.store.RecordHandoffState(request.Context(), state.HandoffState{ProfileID: profileID, Closed: closed, Report: encoded, RecordedAt: time.Now().UTC()}); err != nil {
		writeError(response, http.StatusInternalServerError, "handoff_verification_failed")
		return handoffverification.Report{}, false
	}
	return report, true
}

func (server *Server) verifyHandoff(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID string `json:"profileId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_handoff_request")
		return
	}
	report, ok := server.runHandoffVerification(response, request, input.ProfileID)
	if !ok {
		return
	}
	writeJSON(response, http.StatusOK, report)
}

func (server *Server) closeTemporaryAccess(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID string `json:"profileId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_handoff_request")
		return
	}
	// The temporary administration path is closed only after a fresh, complete
	// verification — never on a stale prior result.
	report, ok := server.runHandoffVerification(response, request, input.ProfileID)
	if !ok {
		return
	}
	if !report.PermitsClosure() {
		writeJSON(response, http.StatusConflict, map[string]any{"code": "handoff_verification_incomplete", "report": report})
		return
	}
	encoded, err := report.Marshal()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "handoff_verification_failed")
		return
	}
	if err := server.store.RecordHandoffState(request.Context(), state.HandoffState{ProfileID: input.ProfileID, Closed: true, Report: encoded, RecordedAt: time.Now().UTC()}); err != nil {
		writeError(response, http.StatusInternalServerError, "handoff_verification_failed")
		return
	}
	// A publicly addressed installation also has a provider-side path — the SSH
	// and Kubernetes API firewall rules — that must actually be removed, not just
	// recorded as closed. Marking it here makes the next reconciliation render a
	// firewall without them. The gate is the same verification just performed.
	if access := server.loadTemporaryAccess(request.Context(), input.ProfileID); access.Open {
		closed, closeErr := access.Close(report.PermitsClosure(), time.Now().UTC())
		if closeErr != nil || server.storeTemporaryAccess(request.Context(), input.ProfileID, closed) != nil {
			writeError(response, http.StatusInternalServerError, "handoff_verification_failed")
			return
		}
	}
	writeJSON(response, http.StatusOK, map[string]any{"closed": true, "report": report})
}

func (server *Server) handoffStatus(response http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedSession(request); !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	profileID := request.URL.Query().Get("profileId")
	if profileID == "" {
		writeError(response, http.StatusBadRequest, "profile_required")
		return
	}
	record, err := server.store.GetHandoffState(request.Context(), profileID)
	if errors.Is(err, state.ErrNotFound) {
		writeJSON(response, http.StatusOK, map[string]any{"closed": false, "verified": false})
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "handoff_verification_failed")
		return
	}
	var report handoffverification.Report
	if json.Unmarshal([]byte(record.Report), &report) != nil {
		writeError(response, http.StatusInternalServerError, "handoff_verification_failed")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"closed": record.Closed, "verified": report.Verified, "report": report})
}

func (server *Server) gatewayPolicy(request *http.Request, profileID string) (gatewayaccess.Policy, error) {
	network, err := server.store.GetPrivateNetwork(request.Context(), profileID)
	if err != nil {
		return gatewayaccess.Policy{}, err
	}
	reference, err := privatenetwork.ParseReference(network.Reference)
	if err != nil {
		return gatewayaccess.Policy{}, err
	}
	hosts := make([]string, 0, len(reference.OperatorEndpoints))
	for _, endpoint := range reference.OperatorEndpoints {
		hosts = append(hosts, endpoint.FQDN)
	}
	return gatewayaccess.Plan(reference.BaseDomain, reference.GatewayHostname, hosts)
}

func (server *Server) gatewayAccessStatus(response http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedSession(request); !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	profileID := request.URL.Query().Get("profileId")
	if profileID == "" {
		writeError(response, http.StatusBadRequest, "profile_required")
		return
	}
	policy, err := server.gatewayPolicy(request, profileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "private_network_required")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "gateway_access_failed")
		return
	}
	writeJSON(response, http.StatusOK, policy)
}

func (server *Server) checkGatewayAccessHost(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID string `json:"profileId"`
		Host      string `json:"host"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" || input.Host == "" {
		writeError(response, http.StatusBadRequest, "invalid_gateway_access_check")
		return
	}
	policy, err := server.gatewayPolicy(request, input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "private_network_required")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "gateway_access_failed")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"host": input.Host, "allowed": policy.HostAllowed(input.Host)})
}

func launcherEnrollmentVaultKey(profileID string) string {
	return profileID + "/launcher-enrollment-preauth"
}

func gatewayIdentityVaultKey(profileID string) string {
	return profileID + "/gateway-auth-key"
}

func (server *Server) establishEnrollment(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID string `json:"profileId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_enrollment_request")
		return
	}
	// Enrollment binds to the established Private Network's base domain, which in
	// turn is only established in the LAN-only shape.
	network, err := server.store.GetPrivateNetwork(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusConflict, "private_network_required")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "enrollment_failed")
		return
	}
	networkReference, err := privatenetwork.ParseReference(network.Reference)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "enrollment_failed")
		return
	}
	// Resumable: a previously established enrollment is returned unchanged so the
	// stable gateway identity and any consumption state stay intact.
	if existing, existingErr := server.store.GetEnrollment(request.Context(), input.ProfileID); existingErr == nil {
		reference, parseErr := enrollment.ParseReference(existing.Reference)
		if parseErr != nil {
			writeError(response, http.StatusInternalServerError, "enrollment_failed")
			return
		}
		writeJSON(response, http.StatusOK, reference)
		return
	} else if !errors.Is(existingErr, state.ErrNotFound) {
		writeError(response, http.StatusInternalServerError, "enrollment_failed")
		return
	}
	now := time.Now().UTC()
	reference, err := enrollment.Plan(networkReference.BaseDomain, now)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "enrollment_failed")
		return
	}
	launcherSecret, err := enrollment.GenerateCredentialSecret()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "enrollment_failed")
		return
	}
	gatewaySecret, err := enrollment.GenerateCredentialSecret()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "enrollment_failed")
		return
	}
	// Both credential secrets are held in the Launcher Vault and never returned
	// to the browser; the gateway key is a Cluster Secret destined for the pod.
	if err := server.vault.Store(launcherEnrollmentVaultKey(input.ProfileID), launcherSecret); errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "enrollment_storage_failed")
		return
	}
	if err := server.vault.Store(gatewayIdentityVaultKey(input.ProfileID), gatewaySecret); errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "enrollment_storage_failed")
		return
	}
	encoded, err := reference.Marshal()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "enrollment_failed")
		return
	}
	if err := server.store.RecordEnrollment(request.Context(), state.EnrollmentReference{ProfileID: input.ProfileID, Reference: encoded, RecordedAt: now}); err != nil {
		writeError(response, http.StatusInternalServerError, "enrollment_failed")
		return
	}
	stable := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	launcherExpiry := stable
	if reference.Launcher.ExpiresAt != nil {
		launcherExpiry = *reference.Launcher.ExpiresAt
	}
	for _, entry := range []struct {
		kind     string
		vaultKey string
		expires  time.Time
	}{
		{"launcher-enrollment-preauth", launcherEnrollmentVaultKey(input.ProfileID), launcherExpiry},
		{"gateway-auth-key", gatewayIdentityVaultKey(input.ProfileID), stable},
	} {
		if err := server.store.UpsertCredentialReference(request.Context(), state.CredentialReference{ProfileID: input.ProfileID, Kind: entry.kind, VaultKey: entry.vaultKey, Source: "launcher", ExpiresAt: entry.expires, RotationStatus: credentialRotationStatus(entry.expires, now)}); err != nil {
			writeError(response, http.StatusInternalServerError, "enrollment_failed")
			return
		}
	}
	writeJSON(response, http.StatusCreated, reference)
}

func (server *Server) enrollmentStatus(response http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedSession(request); !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	profileID := request.URL.Query().Get("profileId")
	if profileID == "" {
		writeError(response, http.StatusBadRequest, "profile_required")
		return
	}
	record, err := server.store.GetEnrollment(request.Context(), profileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "enrollment_not_established")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "enrollment_failed")
		return
	}
	reference, err := enrollment.ParseReference(record.Reference)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "enrollment_failed")
		return
	}
	writeJSON(response, http.StatusOK, reference)
}

func (server *Server) consumeLauncherEnrollment(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID string `json:"profileId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_enrollment_request")
		return
	}
	record, err := server.store.GetEnrollment(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "enrollment_not_established")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "enrollment_failed")
		return
	}
	reference, err := enrollment.ParseReference(record.Reference)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "enrollment_failed")
		return
	}
	consumed, err := reference.ConsumeLauncher(time.Now().UTC())
	if errors.Is(err, enrollment.ErrLauncherAlreadyUsed) {
		writeError(response, http.StatusConflict, "launcher_enrollment_already_used")
		return
	}
	if errors.Is(err, enrollment.ErrLauncherExpired) {
		writeError(response, http.StatusConflict, "launcher_enrollment_expired")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "enrollment_failed")
		return
	}
	// A single-use credential is destroyed on consumption: remove the secret and
	// its custody reference so it can never be presented again.
	if err := server.vault.Delete(launcherEnrollmentVaultKey(input.ProfileID)); errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	} else if err != nil && !errors.Is(err, vault.ErrSecretNotFound) {
		writeError(response, http.StatusInternalServerError, "enrollment_failed")
		return
	}
	encoded, err := consumed.Marshal()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "enrollment_failed")
		return
	}
	if err := server.store.RecordEnrollment(request.Context(), state.EnrollmentReference{ProfileID: input.ProfileID, Reference: encoded, RecordedAt: time.Now().UTC()}); err != nil {
		writeError(response, http.StatusInternalServerError, "enrollment_failed")
		return
	}
	if err := server.store.DeleteCredentialReference(request.Context(), input.ProfileID, "launcher-enrollment-preauth"); err != nil && !errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusInternalServerError, "enrollment_failed")
		return
	}
	writeJSON(response, http.StatusOK, consumed)
}

func (server *Server) tailscaleClient(response http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedSession(request); !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	offer, err := tailscaleclient.Plan(tailscaleclient.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}, tailscaleclient.DetectInstalled(), tailscaleclient.DefaultCatalog())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "tailscale_client_unavailable")
		return
	}
	writeJSON(response, http.StatusOK, offer)
}

func privateNetworkCoordinationVaultKey(profileID string) string {
	return profileID + "/headscale-coordination-secret"
}

func (server *Server) establishPrivateNetwork(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID  string `json:"profileId"`
		BaseDomain string `json:"baseDomain"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" || input.BaseDomain == "" {
		writeError(response, http.StatusBadRequest, "invalid_private_network_request")
		return
	}
	profile, err := server.store.GetProfile(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "private_network_failed")
		return
	}
	shape := privateNetworkShape(profile.DeploymentMode)
	if !shape.Valid() {
		writeError(response, http.StatusConflict, "private_network_mode_unsupported")
		return
	}
	// A publicly addressed installation publishes its coordination endpoint under
	// the public domain, so an Operator can join a device from anywhere. It needs
	// that domain; a LAN-only installation must not be given one.
	publicDomain, published := "", []string(nil)
	if shape == privatenetwork.PublicCoordination {
		overlay, overlayErr := server.store.GetOverlayIdentity(request.Context(), input.ProfileID)
		if errors.Is(overlayErr, state.ErrNotFound) || overlayErr == nil && overlay.Domain == "" {
			writeError(response, http.StatusConflict, "public_domain_required")
			return
		}
		if overlayErr != nil {
			writeError(response, http.StatusInternalServerError, "private_network_failed")
			return
		}
		publicDomain = overlay.Domain
		published = publishedHostnames(overlay.Domain)
	}
	// Resumable: a previously established network is returned unchanged so the
	// coordination identity and operator hostnames stay stable.
	if existing, existingErr := server.store.GetPrivateNetwork(request.Context(), input.ProfileID); existingErr == nil {
		reference, parseErr := privatenetwork.ParseReference(existing.Reference)
		if parseErr != nil {
			writeError(response, http.StatusInternalServerError, "private_network_failed")
			return
		}
		writeJSON(response, http.StatusOK, reference)
		return
	} else if !errors.Is(existingErr, state.ErrNotFound) {
		writeError(response, http.StatusInternalServerError, "private_network_failed")
		return
	}
	reference, err := privatenetwork.Plan(privatenetwork.Input{
		Shape:              shape,
		ProfileID:          input.ProfileID,
		BaseDomain:         input.BaseDomain,
		PublicDomain:       publicDomain,
		PublishedHostnames: published,
	})
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_private_network_request")
		return
	}
	secret, err := privatenetwork.GenerateCoordinationSecret()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "private_network_failed")
		return
	}
	// The Headscale coordination secret is held in the Launcher Vault and never
	// returned to the browser.
	if err := server.vault.Store(privateNetworkCoordinationVaultKey(input.ProfileID), secret); errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "private_network_storage_failed")
		return
	}
	encoded, err := reference.Marshal()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "private_network_failed")
		return
	}
	now := time.Now().UTC()
	if err := server.store.RecordPrivateNetwork(request.Context(), state.PrivateNetworkReference{ProfileID: input.ProfileID, Reference: encoded, RecordedAt: now}); err != nil {
		writeError(response, http.StatusInternalServerError, "private_network_failed")
		return
	}
	expiresAt := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := server.store.UpsertCredentialReference(request.Context(), state.CredentialReference{ProfileID: input.ProfileID, Kind: "headscale-coordination-secret", VaultKey: privateNetworkCoordinationVaultKey(input.ProfileID), Source: "launcher", ExpiresAt: expiresAt, RotationStatus: "current"}); err != nil {
		writeError(response, http.StatusInternalServerError, "private_network_failed")
		return
	}
	writeJSON(response, http.StatusCreated, reference)
}

func (server *Server) privateNetworkStatus(response http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedSession(request); !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	profileID := request.URL.Query().Get("profileId")
	if profileID == "" {
		writeError(response, http.StatusBadRequest, "profile_required")
		return
	}
	record, err := server.store.GetPrivateNetwork(request.Context(), profileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "private_network_not_established")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "private_network_failed")
		return
	}
	reference, err := privatenetwork.ParseReference(record.Reference)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "private_network_failed")
		return
	}
	writeJSON(response, http.StatusOK, reference)
}

func (server *Server) plans(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID string `json:"profileId"`
		Intent    string `json:"intent"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" || input.Intent != "VerifyLauncher" {
		writeError(response, http.StatusBadRequest, "invalid_plan_intent")
		return
	}
	plan, err := server.workflow.PlanVerification(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "plan_creation_failed")
		return
	}
	writeJSON(response, http.StatusCreated, plan)
}

type capabilityRequest struct {
	ProfileID     string                   `json:"profileId"`
	Mode          capability.SelectionMode `json:"mode"`
	CommunityIDs  []string                 `json:"communityIds"`
	Release       string                   `json:"release"`
	RepositoryURL string                   `json:"repositoryUrl"`
	Domain        string                   `json:"domain"`
	// Placed between each hostname's label and the domain, so a .dev cluster's
	// hostnames can never collide with production's. Empty for production.
	EnvironmentExtension string `json:"environmentExtension,omitempty"`
}

func (server *Server) validateGitHubToken(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID string           `json:"profileId"`
		Token     string           `json:"token"`
		Authority github.Authority `json:"authority"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 32*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" || (input.Authority != github.CreationAuthority && input.Authority != github.OngoingAuthority) {
		writeError(response, http.StatusBadRequest, "invalid_github_token")
		return
	}
	input.Token = server.rememberedSecret(input.Token, input.ProfileID+"/github-"+string(input.Authority)+"-token")
	if input.Token == "" {
		writeError(response, http.StatusBadRequest, "invalid_github_token")
		return
	}
	if _, err := server.store.GetProfile(request.Context(), input.ProfileID); errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "github_token_validation_failed")
		return
	}
	status, err := server.github.ValidateToken(request.Context(), input.Token, input.Authority)
	if errors.Is(err, github.ErrRateLimited) {
		writeError(response, http.StatusTooManyRequests, "github_rate_limited")
		return
	}
	if errors.Is(err, github.ErrUnauthorized) || errors.Is(err, github.ErrInsufficientAuthority) {
		writeError(response, http.StatusForbidden, "github_token_insufficient_authority")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadGateway, "github_token_validation_failed")
		return
	}
	vaultKey := input.ProfileID + "/github-" + string(input.Authority) + "-token"
	if err := server.vault.Store(vaultKey, input.Token); errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "github_token_storage_failed")
		return
	}
	expiresAt := status.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if err := server.store.UpsertCredentialReference(request.Context(), state.CredentialReference{ProfileID: input.ProfileID, Kind: "github-" + string(input.Authority) + "-token", VaultKey: vaultKey, Source: "operator", ExpiresAt: expiresAt, RotationStatus: credentialRotationStatus(expiresAt, time.Now())}); err != nil {
		writeError(response, http.StatusInternalServerError, "github_token_storage_failed")
		return
	}
	if input.Authority == github.OngoingAuthority {
		creationKey := input.ProfileID + "/github-creation-token"
		if err := server.vault.Delete(creationKey); err != nil && !errors.Is(err, vault.ErrSecretNotFound) {
			writeError(response, http.StatusInternalServerError, "github_token_rotation_failed")
			return
		}
		if err := server.store.DeleteCredentialReference(request.Context(), input.ProfileID, "github-creation-token"); err != nil && !errors.Is(err, state.ErrNotFound) {
			writeError(response, http.StatusInternalServerError, "github_token_rotation_failed")
			return
		}
	}
	writeJSON(response, http.StatusOK, map[string]any{"owner": status.Owner, "expiresAt": status.ExpiresAt, "authority": input.Authority, "stored": true, "authorityVerified": status.AuthorityVerified})
}

func (server *Server) establishGitHubOverlay(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		capabilityRequest
		PlanID         string `json:"planId"`
		RepositoryName string `json:"repositoryName"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 96*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" || input.PlanID == "" || input.RepositoryName == "" {
		writeError(response, http.StatusBadRequest, "invalid_github_overlay")
		return
	}
	plan, err := server.store.GetPlan(request.Context(), input.PlanID)
	if errors.Is(err, state.ErrNotFound) || plan.ProfileID != input.ProfileID || plan.Intent != "ApplyCapabilities" || plan.Status != "approved" {
		writeError(response, http.StatusConflict, "github_overlay_plan_not_approved")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "github_overlay_failed")
		return
	}
	profile, err := server.store.GetProfile(request.Context(), input.ProfileID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "github_overlay_failed")
		return
	}
	overlay, err := capability.DefaultCatalog().RenderOverlay(capability.OverlayInput{Selection: capability.Selection{Mode: input.Mode, DeploymentMode: capability.DeploymentMode(profile.DeploymentMode), CommunityIDs: input.CommunityIDs}, Release: input.Release, RepositoryURL: "https://github.com/placeholder/" + input.RepositoryName + ".git", Domain: input.Domain, EnvironmentExtension: input.EnvironmentExtension})
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_github_overlay")
		return
	}
	token, err := server.vault.Load(input.ProfileID + "/github-creation-token")
	if errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	}
	if errors.Is(err, vault.ErrSecretNotFound) {
		writeError(response, http.StatusConflict, "github_creation_token_missing")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "github_overlay_failed")
		return
	}
	repository, err := server.github.CreatePrivateRepository(request.Context(), token, input.RepositoryName)
	if errors.Is(err, github.ErrRepositoryNotEmpty) {
		writeError(response, http.StatusConflict, "github_repository_not_empty")
		return
	}
	if errors.Is(err, github.ErrRepositoryNotPrivate) {
		writeError(response, http.StatusConflict, "github_repository_not_private")
		return
	}
	if refuseGitHubOverlay(response, "create overlay repository", "github_repository_creation_failed", err) {
		return
	}
	for path, contents := range overlay.Files {
		overlay.Files[path] = strings.ReplaceAll(contents, "https://github.com/placeholder/"+input.RepositoryName+".git", repository.HTMLURL+".git")
	}
	commit, err := server.github.WriteInitialFiles(request.Context(), token, repository, overlay.Files)
	if refuseGitHubOverlay(response, "write overlay files", "github_overlay_initialization_failed", err) {
		return
	}
	identity := state.OverlayIdentity{ProfileID: input.ProfileID, Provider: "github", Repository: repository.FullName, RepositoryURL: repository.HTMLURL, Release: input.Release, Commit: commit, Domain: input.Domain, MemoryMi: overlay.Assessment.Resources.MemoryMi, StorageGi: overlay.Assessment.Resources.StorageGi, RecordedAt: time.Now().UTC()}
	if err := server.store.RecordOverlayIdentity(request.Context(), identity); err != nil {
		writeError(response, http.StatusInternalServerError, "github_overlay_identity_failed")
		return
	}
	writeJSON(response, http.StatusCreated, identity)
}

func (server *Server) validateGenericGitCredentials(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID     string `json:"profileId"`
		RepositoryURL string `json:"repositoryUrl"`
		Username      string `json:"username"`
		Token         string `json:"token"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 32*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_generic_git_credentials")
		return
	}
	input.Username = server.rememberedSecret(input.Username, input.ProfileID+"/generic-git-username")
	input.Token = server.rememberedSecret(input.Token, input.ProfileID+"/generic-git-token")
	if input.Username == "" || input.Token == "" {
		writeError(response, http.StatusBadRequest, "invalid_generic_git_credentials")
		return
	}
	if _, err := githttps.ValidateRemoteURL(input.RepositoryURL); err != nil {
		writeError(response, http.StatusBadRequest, "unsupported_git_remote")
		return
	}
	if _, err := server.store.GetProfile(request.Context(), input.ProfileID); errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_validation_failed")
		return
	}
	if err := server.genericGit.ValidateAccess(request.Context(), input.RepositoryURL, input.Username, input.Token); errors.Is(err, githttps.ErrAuthentication) {
		writeError(response, http.StatusForbidden, "generic_git_authentication_failed")
		return
	} else if err != nil {
		writeError(response, http.StatusBadGateway, "generic_git_validation_failed")
		return
	}
	usernameKey := input.ProfileID + "/generic-git-username"
	tokenKey := input.ProfileID + "/generic-git-token"
	if err := server.vault.Store(usernameKey, input.Username); errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_storage_failed")
		return
	}
	if err := server.vault.Store(tokenKey, input.Token); errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_storage_failed")
		return
	}
	expiresAt := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, reference := range []state.CredentialReference{
		{ProfileID: input.ProfileID, Kind: "generic-git-username", VaultKey: usernameKey, Source: "operator", ExpiresAt: expiresAt, RotationStatus: "current"},
		{ProfileID: input.ProfileID, Kind: "generic-git-token", VaultKey: tokenKey, Source: "operator", ExpiresAt: expiresAt, RotationStatus: "current"},
	} {
		if err := server.store.UpsertCredentialReference(request.Context(), reference); err != nil {
			writeError(response, http.StatusInternalServerError, "generic_git_storage_failed")
			return
		}
	}
	writeJSON(response, http.StatusOK, map[string]any{"repositoryUrl": input.RepositoryURL, "stored": true})
}

func (server *Server) establishGenericGitOverlay(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		capabilityRequest
		PlanID string `json:"planId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 96*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" || input.PlanID == "" {
		writeError(response, http.StatusBadRequest, "invalid_generic_git_overlay")
		return
	}
	if _, err := githttps.ValidateRemoteURL(input.RepositoryURL); err != nil {
		writeError(response, http.StatusBadRequest, "unsupported_git_remote")
		return
	}
	plan, err := server.store.GetPlan(request.Context(), input.PlanID)
	if errors.Is(err, state.ErrNotFound) || plan.ProfileID != input.ProfileID || plan.Intent != "ApplyCapabilities" || plan.Status != "approved" {
		writeError(response, http.StatusConflict, "generic_git_overlay_plan_not_approved")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_overlay_failed")
		return
	}
	profile, err := server.store.GetProfile(request.Context(), input.ProfileID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_overlay_failed")
		return
	}
	overlay, err := capability.DefaultCatalog().RenderOverlay(capability.OverlayInput{Selection: capability.Selection{Mode: input.Mode, DeploymentMode: capability.DeploymentMode(profile.DeploymentMode), CommunityIDs: input.CommunityIDs}, Release: input.Release, RepositoryURL: input.RepositoryURL, Domain: input.Domain, EnvironmentExtension: input.EnvironmentExtension})
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_generic_git_overlay")
		return
	}
	if !matchesOverlayPlan(plan, profile, overlay.Diff) {
		writeError(response, http.StatusConflict, "generic_git_overlay_plan_mismatch")
		return
	}
	username, err := server.vault.Load(input.ProfileID + "/generic-git-username")
	if errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	}
	if errors.Is(err, vault.ErrSecretNotFound) {
		writeError(response, http.StatusConflict, "generic_git_credentials_missing")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_overlay_failed")
		return
	}
	token, err := server.vault.Load(input.ProfileID + "/generic-git-token")
	if errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	}
	if errors.Is(err, vault.ErrSecretNotFound) {
		writeError(response, http.StatusConflict, "generic_git_credentials_missing")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_overlay_failed")
		return
	}
	if recorded, err := server.store.GetOverlayIdentity(request.Context(), input.ProfileID); err == nil {
		if recorded.Provider != "generic-https" || recorded.RepositoryURL != input.RepositoryURL {
			writeError(response, http.StatusConflict, "generic_git_overlay_identity_conflict")
			return
		}
		present, verifyErr := server.genericGit.RemoteContainsCommit(request.Context(), input.RepositoryURL, username, token, recorded.Commit)
		if errors.Is(verifyErr, githttps.ErrAuthentication) {
			writeError(response, http.StatusForbidden, "generic_git_authentication_failed")
			return
		}
		if verifyErr != nil {
			writeError(response, http.StatusBadGateway, "generic_git_resume_check_failed")
			return
		}
		if present {
			writeJSON(response, http.StatusOK, recorded)
			return
		}
		// The recorded commit is gone from the remote. Refusing here was a dead
		// end: an operator who emptied the repository in order to establish it
		// again had no way forward and no way to withdraw the recorded identity.
		// Fall through instead — initialization accepts nothing but an empty
		// remote, so a repository that still holds other commits stays protected,
		// and the recorded identity is replaced rather than contradicted.
	} else if !errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusInternalServerError, "generic_git_overlay_failed")
		return
	}
	remoteIdentity, err := server.genericGit.InitializeEmptyRemote(request.Context(), input.RepositoryURL, username, token, overlay.Files)
	if errors.Is(err, githttps.ErrAuthentication) {
		writeError(response, http.StatusForbidden, "generic_git_authentication_failed")
		return
	}
	if errors.Is(err, githttps.ErrRemoteNotEmpty) || errors.Is(err, githttps.ErrConcurrentChange) {
		writeError(response, http.StatusConflict, "generic_git_remote_conflict")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadGateway, "generic_git_overlay_initialization_failed")
		return
	}
	identity := state.OverlayIdentity{ProfileID: input.ProfileID, Provider: "generic-https", Repository: remoteIdentity.RepositoryURL, RepositoryURL: remoteIdentity.RepositoryURL, Release: input.Release, Commit: remoteIdentity.Commit, Domain: input.Domain, MemoryMi: overlay.Assessment.Resources.MemoryMi, StorageGi: overlay.Assessment.Resources.StorageGi, RecordedAt: time.Now().UTC()}
	if err := server.store.RecordOverlayIdentity(request.Context(), identity); err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_overlay_identity_failed")
		return
	}
	writeJSON(response, http.StatusCreated, identity)
}

func (server *Server) proposeGenericGitOverlay(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		capabilityRequest
		PlanID string `json:"planId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 96*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" || input.PlanID == "" {
		writeError(response, http.StatusBadRequest, "invalid_generic_git_proposal")
		return
	}
	if _, err := githttps.ValidateRemoteURL(input.RepositoryURL); err != nil {
		writeError(response, http.StatusBadRequest, "unsupported_git_remote")
		return
	}
	plan, err := server.store.GetPlan(request.Context(), input.PlanID)
	if errors.Is(err, state.ErrNotFound) || plan.ProfileID != input.ProfileID || plan.Intent != "ApplyCapabilities" || plan.Status != "approved" {
		writeError(response, http.StatusConflict, "generic_git_proposal_plan_not_approved")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_proposal_failed")
		return
	}
	profile, err := server.store.GetProfile(request.Context(), input.ProfileID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_proposal_failed")
		return
	}
	overlay, err := capability.DefaultCatalog().RenderOverlay(capability.OverlayInput{Selection: capability.Selection{Mode: input.Mode, DeploymentMode: capability.DeploymentMode(profile.DeploymentMode), CommunityIDs: input.CommunityIDs}, Release: input.Release, RepositoryURL: input.RepositoryURL, Domain: input.Domain, EnvironmentExtension: input.EnvironmentExtension})
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_generic_git_proposal")
		return
	}
	if !matchesOverlayPlan(plan, profile, overlay.Diff) {
		writeError(response, http.StatusConflict, "generic_git_proposal_plan_mismatch")
		return
	}
	recorded, err := server.store.GetOverlayIdentity(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) || recorded.Provider != "generic-https" || recorded.RepositoryURL != input.RepositoryURL {
		writeError(response, http.StatusConflict, "generic_git_overlay_identity_missing")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_proposal_failed")
		return
	}
	username, err := server.vault.Load(input.ProfileID + "/generic-git-username")
	if errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	}
	if errors.Is(err, vault.ErrSecretNotFound) {
		writeError(response, http.StatusConflict, "generic_git_credentials_missing")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_proposal_failed")
		return
	}
	token, err := server.vault.Load(input.ProfileID + "/generic-git-token")
	if errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	}
	if errors.Is(err, vault.ErrSecretNotFound) {
		writeError(response, http.StatusConflict, "generic_git_credentials_missing")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_proposal_failed")
		return
	}
	proposal, err := server.genericGit.CreateProposalBranch(request.Context(), input.RepositoryURL, username, token, githttps.ProposalBranchForDiff(overlay.Diff), overlay.Files)
	if errors.Is(err, githttps.ErrAuthentication) {
		writeError(response, http.StatusForbidden, "generic_git_authentication_failed")
		return
	}
	if errors.Is(err, githttps.ErrConcurrentChange) {
		writeError(response, http.StatusConflict, "generic_git_proposal_conflict")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadGateway, "generic_git_proposal_failed")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]string{
		"branch":              proposal.Branch,
		"commit":              proposal.Commit,
		"mergeInstructionKey": "generic_git_manual_merge",
	})
}

func matchesOverlayPlan(plan state.PlanRecord, profile state.Profile, overlayDiff string) bool {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%d\n%s", "ApplyCapabilities", profile.ID, profile.Revision, overlayDiff)))
	return plan.Digest == fmt.Sprintf("%x", digest[:])
}

func (server *Server) bootstrapAssets(response http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedSession(request); !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	release := request.URL.Query().Get("release")
	if release == "" {
		writeError(response, http.StatusBadRequest, "bootstrap_asset_release_required")
		return
	}
	assets, err := server.assets.Requirements(request.Context(), release)
	if errors.Is(err, bootstrapassets.ErrUnknownRelease) || errors.Is(err, bootstrapassets.ErrUntrustedManifest) {
		writeError(response, http.StatusConflict, "bootstrap_asset_release_unavailable")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "bootstrap_asset_status_failed")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"release":                   release,
		"assets":                    assets,
		"offlineBundleAvailability": "future",
	})
}

func (server *Server) acquireBootstrapAssets(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		Release string `json:"release"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Release == "" {
		writeError(response, http.StatusBadRequest, "invalid_bootstrap_asset_request")
		return
	}
	assets, err := server.assets.Acquire(request.Context(), input.Release)
	if errors.Is(err, bootstrapassets.ErrUnknownRelease) || errors.Is(err, bootstrapassets.ErrUntrustedManifest) {
		writeError(response, http.StatusConflict, "bootstrap_asset_release_unavailable")
		return
	}
	if errors.Is(err, bootstrapassets.ErrIntegrity) {
		writeError(response, http.StatusBadGateway, "bootstrap_asset_integrity_failed")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadGateway, "bootstrap_asset_acquisition_failed")
		return
	}
	if err := server.workflow.ResumeActive(request.Context()); err != nil {
		writeError(response, http.StatusInternalServerError, "workflow_resume_failed")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{
		"release":                   input.Release,
		"assets":                    assets,
		"offlineBundleAvailability": "future",
	})
}

type nodeTargetRequest struct {
	Kind     nodeinspect.TargetKind `json:"kind"`
	Host     string                 `json:"host"`
	Port     int                    `json:"port"`
	Username string                 `json:"username"`
}

type nodeAuthenticationRequest struct {
	Kind          nodeinspect.AuthenticationKind `json:"kind"`
	Password      string                         `json:"password"`
	PrivateKey    string                         `json:"privateKey"`
	KeyPassphrase string                         `json:"keyPassphrase"`
	SudoPassword  string                         `json:"sudoPassword"`
}

func (server *Server) nodeCapabilities(response http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedSession(request); !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	writeJSON(response, http.StatusOK, map[string]bool{"sameHostSupported": runtime.GOOS == "linux"})
}

func (input nodeTargetRequest) target() nodeinspect.Target {
	return nodeinspect.Target{Kind: input.Kind, Host: input.Host, Port: input.Port, Username: input.Username}
}

func (server *Server) probeNode(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID string            `json:"profileId"`
		Target    nodeTargetRequest `json:"target"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_node_target")
		return
	}
	target := input.Target.target()
	if err := target.Validate(runtime.GOOS); err != nil || target.Kind != nodeinspect.RemoteTarget {
		writeError(response, http.StatusBadRequest, "invalid_node_target")
		return
	}
	if _, err := server.store.GetProfile(request.Context(), input.ProfileID); errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	fingerprint, err := nodeinspect.ProbeHostKey(request.Context(), target)
	if err != nil {
		writeError(response, http.StatusBadGateway, "node_host_key_probe_failed")
		return
	}
	if trust, err := server.store.GetNodeTrust(request.Context(), input.ProfileID); err == nil && (trust.Host != target.Host || trust.Port != target.Port || trust.Username != target.Username || trust.Fingerprint != fingerprint) {
		writeError(response, http.StatusConflict, "node_host_key_mismatch")
		return
	} else if err != nil && !errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusInternalServerError, "node_host_key_probe_failed")
		return
	}
	if err := server.store.RecordPendingNodeTrust(request.Context(), state.PendingNodeTrust{ProfileID: input.ProfileID, Host: target.Host, Port: target.Port, Username: target.Username, Fingerprint: fingerprint, ObservedAt: time.Now().UTC()}); err != nil {
		writeError(response, http.StatusInternalServerError, "node_host_key_probe_failed")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"target": target, "fingerprint": fingerprint, "requiresConfirmation": true})
}

func (server *Server) trustNode(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID   string            `json:"profileId"`
		Target      nodeTargetRequest `json:"target"`
		Fingerprint string            `json:"fingerprint"`
		Confirmed   bool              `json:"confirmed"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" || !input.Confirmed || !strings.HasPrefix(input.Fingerprint, "SHA256:") {
		writeError(response, http.StatusBadRequest, "invalid_node_trust_confirmation")
		return
	}
	target := input.Target.target()
	if err := target.Validate(runtime.GOOS); err != nil || target.Kind != nodeinspect.RemoteTarget {
		writeError(response, http.StatusBadRequest, "invalid_node_target")
		return
	}
	if _, err := server.store.GetProfile(request.Context(), input.ProfileID); errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	pending, err := server.store.GetPendingNodeTrust(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) || pending.Host != target.Host || pending.Port != target.Port || pending.Username != target.Username || pending.Fingerprint != input.Fingerprint || time.Since(pending.ObservedAt) > 10*time.Minute {
		writeError(response, http.StatusConflict, "node_host_key_confirmation_required")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "node_trust_storage_failed")
		return
	}
	if err := server.store.RecordNodeTrust(request.Context(), state.NodeTrust{ProfileID: input.ProfileID, Host: target.Host, Port: target.Port, Username: target.Username, Fingerprint: input.Fingerprint, ConfirmedAt: time.Now().UTC()}); err != nil {
		writeError(response, http.StatusInternalServerError, "node_trust_storage_failed")
		return
	}
	if err := server.store.DeletePendingNodeTrust(request.Context(), input.ProfileID); err != nil {
		writeError(response, http.StatusInternalServerError, "node_trust_storage_failed")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"target": target, "fingerprint": input.Fingerprint})
}

func (server *Server) inspectNode(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID      string                    `json:"profileId"`
		Target         nodeTargetRequest         `json:"target"`
		Authentication nodeAuthenticationRequest `json:"authentication"`
		DataDirectory  string                    `json:"dataDirectory"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 512*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" || input.DataDirectory == "" || nodeinspect.ValidateDataDirectory(input.DataDirectory) != nil {
		writeError(response, http.StatusBadRequest, "invalid_node_inspection")
		return
	}
	target := input.Target.target()
	if err := target.Validate(runtime.GOOS); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_node_target")
		return
	}
	profile, err := server.store.GetProfile(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "node_inspection_failed")
		return
	}
	assessment, err := capability.DefaultCatalog().Assess(capability.Selection{Mode: capability.Minimal, DeploymentMode: capability.DeploymentMode(profile.DeploymentMode)})
	if err != nil {
		writeError(response, http.StatusInternalServerError, "node_requirements_failed")
		return
	}
	requirements := nodeinspect.Requirements{ProfileID: profile.ID, MemoryMi: assessment.Resources.MemoryMi, DiskGi: assessment.Resources.StorageGi, DataDirectory: input.DataDirectory, RequiredPorts: []int{80, 443, 6443}}
	if target.Kind == nodeinspect.SameHostTarget {
		report, result, err := server.nodes.InspectSameHost(profile.ID, requirements)
		if err != nil {
			writeError(response, http.StatusConflict, "same_host_inspection_unsupported")
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"target": target, "report": report, "assessment": result})
		return
	}
	trust, err := server.store.GetNodeTrust(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) || trust.Host != target.Host || trust.Port != target.Port || trust.Username != target.Username {
		writeError(response, http.StatusConflict, "node_host_key_confirmation_required")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "node_inspection_failed")
		return
	}
	credentials, err := server.storeNodeCredentials(request.Context(), input.ProfileID, input.Authentication)
	if errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_node_credentials")
		return
	}
	report, result, err := server.nodes.InspectRemote(request.Context(), target, credentials, trust.Fingerprint, profile.ID, requirements)
	if errors.Is(err, nodeinspect.ErrHostKeyMismatch) {
		writeError(response, http.StatusConflict, "node_host_key_mismatch")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadGateway, "node_inspection_failed")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"target": target, "report": report, "assessment": result})
}

func (server *Server) planNodeSSHKey(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID string `json:"profileId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_node_ssh_key_plan")
		return
	}
	trust, err := server.store.GetNodeTrust(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusConflict, "node_host_key_confirmation_required")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "node_ssh_key_plan_failed")
		return
	}
	plan, err := server.workflow.PlanChange(request.Context(), input.ProfileID, "PrepareNodeSSHKey", "node-ssh-key\n"+trust.Host+"\n"+trust.Fingerprint, []workflow.Effect{{Code: "node.ssh_key.prepared", MessageKey: "plan.effect.node_ssh_key"}})
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "node_ssh_key_plan_failed")
		return
	}
	writeJSON(response, http.StatusCreated, plan)
}

func (server *Server) planLocalBootstrap(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID       string                       `json:"profileId"`
		Target          nodeTargetRequest            `json:"target"`
		Authentication  nodeAuthenticationRequest    `json:"authentication"`
		Release         string                       `json:"release"`
		Configuration   localbootstrap.Configuration `json:"configuration"`
		SecretsManifest string                       `json:"secretsManifest"`
		PublicExposure  *struct {
			DNS01Provider      string `json:"dns01Provider"`
			DNSZone            string `json:"dnsZone"`
			DNSToken           string `json:"dnsToken"`
			PublicIPBehavior   string `json:"publicIpBehavior"`
			RouterAcknowledged bool   `json:"routerAcknowledged"`
		} `json:"publicExposure"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 2*1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" || input.Release == "" {
		writeError(response, http.StatusBadRequest, "invalid_local_bootstrap_plan")
		return
	}
	// Which releases are installable is settled by signed evidence, not by a
	// version compiled into this launcher: the asset check further down accepts
	// any release whose published manifest verifies against the trusted release
	// key. Only the shape of the identifier is rejected here.
	if err := bootstrapassets.ValidateRelease(input.Release); err != nil {
		writeError(response, http.StatusConflict, "local_bootstrap_release_unsupported")
		return
	}
	if input.Configuration.DataDirectory == "" {
		input.Configuration.DataDirectory = "/var/lib/smallworlds-data"
	}
	if input.Configuration.NodeName == "" {
		input.Configuration.NodeName = "smallworlds-local-node"
	}
	profile, err := server.store.GetProfile(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	if err != nil || profile.DeploymentMode != "local-lan" && profile.DeploymentMode != "local-public" {
		writeError(response, http.StatusConflict, "local_bootstrap_profile_incompatible")
		return
	}
	if profile.DeploymentMode == "local-public" {
		if input.PublicExposure == nil {
			writeError(response, http.StatusBadRequest, "local_public_configuration_required")
			return
		}
		if input.PublicExposure.PublicIPBehavior == "" {
			input.PublicExposure.PublicIPBehavior = "dynamic-ddns"
		}
		credentialKey := profile.ID + "/local-public-dns-token"
		input.Configuration.ManageDNS = true
		input.Configuration.Public = &localbootstrap.PublicConfiguration{
			DNS01Provider: input.PublicExposure.DNS01Provider, DNSZone: input.PublicExposure.DNSZone,
			DNSCredentialKey: credentialKey, PublicIPBehavior: input.PublicExposure.PublicIPBehavior,
			RouterAcknowledged: input.PublicExposure.RouterAcknowledged,
		}
		if err := input.Configuration.Validate(); err != nil {
			if !input.PublicExposure.RouterAcknowledged {
				writeError(response, http.StatusConflict, "router_forwarding_acknowledgement_required")
			} else {
				writeError(response, http.StatusBadRequest, "invalid_local_bootstrap_plan")
			}
			return
		}
		tokenPresent := strings.TrimSpace(input.PublicExposure.DNSToken) != ""
		if tokenPresent {
			if len(input.PublicExposure.DNSToken) < 16 || len(input.PublicExposure.DNSToken) > 1024 || strings.ContainsAny(input.PublicExposure.DNSToken, "\r\n") {
				writeError(response, http.StatusBadRequest, "invalid_dns_provider_token")
				return
			}
			if err := server.vault.Store(credentialKey, input.PublicExposure.DNSToken); errors.Is(err, vault.ErrLocked) {
				writeError(response, http.StatusLocked, "vault_locked")
				return
			} else if err != nil {
				writeError(response, http.StatusInternalServerError, "dns_provider_storage_failed")
				return
			}
			if err := server.store.UpsertCredentialReference(request.Context(), state.CredentialReference{ProfileID: profile.ID, Kind: "local-public-dns-token", VaultKey: credentialKey, Source: "operator", ExpiresAt: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), RotationStatus: "current"}); err != nil {
				writeError(response, http.StatusInternalServerError, "dns_provider_storage_failed")
				return
			}
		} else if present, containsErr := server.vault.Contains(credentialKey); containsErr != nil || !present {
			if errors.Is(containsErr, vault.ErrLocked) {
				writeError(response, http.StatusLocked, "vault_locked")
			} else {
				writeError(response, http.StatusBadRequest, "dns_provider_token_required")
			}
			return
		}
		dnsToken := input.PublicExposure.DNSToken
		if dnsToken == "" {
			dnsToken, err = server.vault.Load(credentialKey)
			if err != nil {
				writeError(response, http.StatusLocked, "vault_locked")
				return
			}
		}
		nameservers, lookupErr := server.hetzner.Nameservers(request.Context(), dnsToken, input.PublicExposure.DNSZone)
		if lookupErr != nil {
			if ok := server.writeProviderError(response, lookupErr); !ok {
				return
			}
		}
		delegation := hetzner.CheckDelegation(input.PublicExposure.DNSZone, nameservers, capability.LocalPublic)
		if !delegation.Satisfied() {
			writeJSON(response, http.StatusConflict, map[string]any{"code": "public_dns_delegation_required", "delegation": delegation})
			return
		}
	} else if input.PublicExposure != nil || input.Configuration.ManageDNS || input.Configuration.Public != nil {
		writeError(response, http.StatusConflict, "local_public_configuration_not_allowed")
		return
	}
	if err := input.Configuration.Validate(); err != nil {
		if profile.DeploymentMode == "local-public" && input.PublicExposure != nil && !input.PublicExposure.RouterAcknowledged {
			writeError(response, http.StatusConflict, "router_forwarding_acknowledgement_required")
		} else {
			writeError(response, http.StatusBadRequest, "invalid_local_bootstrap_plan")
		}
		return
	}
	overlay, err := server.store.GetOverlayIdentity(request.Context(), profile.ID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusConflict, "gitops_overlay_required")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "local_bootstrap_plan_failed")
		return
	}
	if overlay.Release != input.Release {
		writeError(response, http.StatusConflict, "local_bootstrap_release_mismatch")
		return
	}
	if overlay.Domain != "" && overlay.Domain != input.Configuration.Domain {
		writeError(response, http.StatusConflict, "local_bootstrap_domain_mismatch")
		return
	}
	assetStatuses, err := server.assets.Requirements(request.Context(), input.Release)
	if errors.Is(err, bootstrapassets.ErrUnknownRelease) || errors.Is(err, bootstrapassets.ErrUntrustedManifest) {
		writeError(response, http.StatusConflict, "bootstrap_asset_release_unavailable")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "bootstrap_asset_status_failed")
		return
	}
	var selectedAsset bootstrapassets.Status
	for _, candidate := range assetStatuses {
		if candidate.ID == "bootstrap-linux-amd64" {
			selectedAsset = candidate
			break
		}
	}
	if selectedAsset.ID == "" || selectedAsset.State != bootstrapassets.StateReady {
		writeError(response, http.StatusConflict, "bootstrap_assets_not_ready")
		return
	}
	target := input.Target.target()
	if err := target.Validate(runtime.GOOS); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_node_target")
		return
	}
	assessment, err := capability.DefaultCatalog().Assess(capability.Selection{Mode: capability.Minimal, DeploymentMode: capability.DeploymentMode(profile.DeploymentMode)})
	if err != nil {
		writeError(response, http.StatusInternalServerError, "node_requirements_failed")
		return
	}
	requirements := nodeinspect.Requirements{ProfileID: profile.ID, MemoryMi: assessment.Resources.MemoryMi, DiskGi: assessment.Resources.StorageGi, DataDirectory: input.Configuration.DataDirectory, RequiredPorts: []int{80, 443, 6443}}
	if overlay.MemoryMi > 0 {
		requirements.MemoryMi = overlay.MemoryMi
	}
	if overlay.StorageGi > 0 {
		requirements.DiskGi = overlay.StorageGi
	}
	var report nodeinspect.Report
	var nodeAssessment nodeinspect.Assessment
	hostFingerprint := ""
	authenticationKind := "same-host"
	if target.Kind == nodeinspect.SameHostTarget {
		if input.Authentication.SudoPassword != "" {
			if err := server.vault.Store(profile.ID+"/node-sudo-password", input.Authentication.SudoPassword); errors.Is(err, vault.ErrLocked) {
				writeError(response, http.StatusLocked, "vault_locked")
				return
			} else if err != nil {
				writeError(response, http.StatusInternalServerError, "node_credentials_storage_failed")
				return
			}
			expiresAt := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
			if err := server.store.UpsertCredentialReference(request.Context(), state.CredentialReference{ProfileID: profile.ID, Kind: "node-sudo-password", VaultKey: profile.ID + "/node-sudo-password", Source: "operator", ExpiresAt: expiresAt, RotationStatus: "current"}); err != nil {
				writeError(response, http.StatusInternalServerError, "node_credentials_storage_failed")
				return
			}
		}
		report, nodeAssessment, err = server.nodes.InspectSameHost(profile.ID, requirements)
	} else {
		trust, trustErr := server.store.GetNodeTrust(request.Context(), profile.ID)
		if errors.Is(trustErr, state.ErrNotFound) || trust.Host != target.Host || trust.Port != target.Port || trust.Username != target.Username {
			writeError(response, http.StatusConflict, "node_host_key_confirmation_required")
			return
		}
		if trustErr != nil {
			writeError(response, http.StatusInternalServerError, "local_bootstrap_plan_failed")
			return
		}
		credentials, credentialErr := server.storeNodeCredentials(request.Context(), profile.ID, input.Authentication)
		if errors.Is(credentialErr, vault.ErrLocked) {
			writeError(response, http.StatusLocked, "vault_locked")
			return
		}
		if credentialErr != nil {
			writeError(response, http.StatusBadRequest, "invalid_node_credentials")
			return
		}
		hostFingerprint = trust.Fingerprint
		authenticationKind = string(credentials.Kind)
		report, nodeAssessment, err = server.nodes.InspectRemote(request.Context(), target, credentials, trust.Fingerprint, profile.ID, requirements)
	}
	if errors.Is(err, nodeinspect.ErrHostKeyMismatch) {
		writeError(response, http.StatusConflict, "node_host_key_mismatch")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadGateway, "node_reinspection_failed")
		return
	}
	if !nodeAssessment.Ready {
		writeJSON(response, http.StatusConflict, map[string]any{"code": "node_bootstrap_preconditions_failed", "assessment": nodeAssessment})
		return
	}
	secretVaultKey := ""
	if input.SecretsManifest != "" {
		if err := localbootstrap.ValidateSecretsManifest(input.SecretsManifest); err != nil {
			writeError(response, http.StatusBadRequest, "invalid_cluster_secrets_manifest")
			return
		}
		secretVaultKey = profile.ID + "/cluster-secrets-manifest"
		if err := server.vault.Store(secretVaultKey, input.SecretsManifest); errors.Is(err, vault.ErrLocked) {
			writeError(response, http.StatusLocked, "vault_locked")
			return
		} else if err != nil {
			writeError(response, http.StatusInternalServerError, "cluster_secrets_storage_failed")
			return
		}
		// Recorded like any other custodied secret, so the interface can say that
		// one exists — and stop demanding it — without ever seeing its value.
		if err := server.store.UpsertCredentialReference(request.Context(), state.CredentialReference{ProfileID: profile.ID, Kind: "cluster-secrets-manifest", VaultKey: secretVaultKey, Source: "operator", ExpiresAt: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), RotationStatus: "current"}); err != nil {
			writeError(response, http.StatusInternalServerError, "cluster_secrets_storage_failed")
			return
		}
	} else if present, containsErr := server.vault.Contains(profile.ID + "/cluster-secrets-manifest"); containsErr == nil && present {
		secretVaultKey = profile.ID + "/cluster-secrets-manifest"
	} else if errors.Is(containsErr, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	}
	// Nothing supplied and nothing kept: create them. Asking an Operator to
	// author these by hand was asking for machine credentials nobody ever reads
	// (Garage's RPC secret and admin token, the bulk-invite secret) and for a
	// repository credential this console already holds in its own Vault.
	// smallworlds-init.sh has always generated them for the shell path; the
	// console refusing to was the regression.
	if secretVaultKey == "" {
		generated, generateErr := server.generateClusterSecrets(request.Context(), profile.ID, overlay)
		if errors.Is(generateErr, vault.ErrLocked) {
			writeError(response, http.StatusLocked, "vault_locked")
			return
		}
		// Refuse rather than build a cluster that cannot start. Keycloak, Garage
		// and Grafana each mount a Secret this manifest is the only source of, and
		// Argo CD cannot read the private settings repository without its
		// credential; without them the install completes, Argo CD reports
		// Degraded, and the pods sit in CreateContainerConfigError for as long as
		// anyone is willing to wait. That is a far worse outcome than being told
		// now — so a manifest that cannot be completed is not written at all.
		if generateErr != nil {
			log.Printf("cluster secrets: generate for profile %s: %v", profile.ID, generateErr)
			writeError(response, http.StatusConflict, "cluster_secrets_required")
			return
		}
		secretVaultKey = generated
	}
	inspectionDigest, err := localbootstrap.InspectionDigest(report)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "local_bootstrap_plan_failed")
		return
	}
	nodeIdentity := report.NodeIdentity
	inspectedAt := time.Now().UTC()
	binding := localbootstrap.Binding{
		PlanID: "pending", ProfileID: profile.ID, ProfileRevision: profile.Revision, Target: target,
		HostFingerprint: hostFingerprint, NodeIdentity: nodeIdentity, InspectionDigest: inspectionDigest, InspectedAt: inspectedAt,
		Release: input.Release, AssetID: selectedAsset.ID, AssetSHA256: selectedAsset.SHA256,
		OverlayRepositoryURL: overlay.RepositoryURL, OverlayCommit: overlay.Commit, OverlayRelease: overlay.Release,
		AuthenticationKind: authenticationKind, SecretsVaultKey: secretVaultKey, Configuration: input.Configuration,
	}
	if _, err := binding.Marshal(); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_local_bootstrap_plan")
		return
	}
	effects := []workflow.Effect{
		{Code: "node.privileged.bootstrap", MessageKey: "plan.effect.local_bootstrap_privileged"},
		{Code: "node.data_paths.prepared", MessageKey: "plan.effect.local_bootstrap_data"},
		{Code: "kubernetes.k3s.installed", MessageKey: "plan.effect.local_bootstrap_k3s"},
		{Code: "gitops.argocd.configured", MessageKey: "plan.effect.local_bootstrap_argocd"},
	}
	risks := []workflow.Risk{
		{Code: "node.network_ports.changed", MessageKey: "plan.risk.local_bootstrap_exposure"},
		{Code: "node.services.may_restart", MessageKey: "plan.risk.local_bootstrap_downtime"},
		{Code: "node.atomic_install", MessageKey: "plan.risk.local_bootstrap_cancellation"},
		{Code: "node.data_preserved_on_retry", MessageKey: "plan.risk.local_bootstrap_recovery"},
	}
	if profile.DeploymentMode == "local-public" {
		effects = append(effects,
			workflow.Effect{Code: "dns.dynamic_records.managed", MessageKey: "plan.effect.local_public_ddns"},
			workflow.Effect{Code: "certificates.public.issued", MessageKey: "plan.effect.local_public_certificates"},
			workflow.Effect{Code: "members.public_ingress.enabled", MessageKey: "plan.effect.local_public_member_ingress"},
			workflow.Effect{Code: "headscale.public_coordination.enabled", MessageKey: "plan.effect.local_public_headscale"},
		)
		risks = append(risks,
			workflow.Risk{Code: "router.manual_forwarding", MessageKey: "plan.risk.local_public_router"},
			workflow.Risk{Code: "dns.certificate.propagation_wait", MessageKey: "plan.risk.local_public_propagation"},
		)
	}
	plan, err := server.workflow.PlanChangeWithRisks(request.Context(), profile.ID, "BootstrapLocalNode", binding.DigestDetail(), effects, risks)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "local_bootstrap_plan_failed")
		return
	}
	binding.PlanID = plan.ID
	encodedBinding, err := binding.Marshal()
	if err != nil || server.store.RecordBootstrapPlan(request.Context(), state.BootstrapPlanRecord{PlanID: plan.ID, ProfileID: profile.ID, Binding: encodedBinding, CreatedAt: plan.CreatedAt}) != nil {
		writeError(response, http.StatusInternalServerError, "local_bootstrap_plan_failed")
		return
	}
	plan.Preconditions.NodeIdentity = nodeIdentity
	plan.Preconditions.HostFingerprint = hostFingerprint
	plan.Preconditions.InspectionDigest = inspectionDigest
	plan.Preconditions.InspectedAt = inspectedAt
	plan.Preconditions.BootstrapRelease = input.Release
	plan.Preconditions.OverlayCommit = overlay.Commit
	plan.Preconditions.DataDirectory = input.Configuration.DataDirectory
	result := map[string]any{"plan": plan, "inspection": map[string]any{"target": target, "report": report, "assessment": nodeAssessment}}
	if profile.DeploymentMode == "local-public" {
		result["routerForwarding"] = map[string]any{
			"acknowledged":           true,
			"automaticConfiguration": false,
			"dedicatedVerification":  false,
			"rules": []map[string]any{
				{"protocol": "tcp", "externalPort": 80, "targetPort": 80, "purpose": "http-acme-and-member-redirect"},
				{"protocol": "tcp", "externalPort": 443, "targetPort": 443, "purpose": "https-member-applications-and-headscale"},
				{"protocol": "udp", "externalPort": 10000, "targetPort": 10000, "purpose": "jitsi-media"},
			},
		}
	}
	writeJSON(response, http.StatusCreated, result)
}

// rememberedSecret returns the value the browser supplied, or — when it supplied
// nothing — the one already custodied in the Launcher Vault under vaultKey.
//
// This is what lets a step be revisited without retyping a token: the browser is
// never sent a stored secret back (it cannot be), so an operator returning to a
// finished step sees an empty field. Treating that empty field as "use what you
// already have" is the difference between a journey that can be resumed and one
// that must be redone. Supplying a value always wins, so deliberate rotation
// still works, and a locked vault simply yields nothing.
func (server *Server) rememberedSecret(supplied, vaultKey string) string {
	if supplied != "" {
		return supplied
	}
	stored, err := server.vault.Load(vaultKey)
	if err != nil {
		return ""
	}
	return stored
}

func (server *Server) storeNodeCredentials(ctx context.Context, profileID string, input nodeAuthenticationRequest) (nodeinspect.Credentials, error) {
	if input.Kind != nodeinspect.AgentAuthentication && input.Kind != nodeinspect.PrivateKeyAuthentication && input.Kind != nodeinspect.PasswordAuthentication {
		return nodeinspect.Credentials{}, fmt.Errorf("unsupported node authentication")
	}

	if input.Kind == nodeinspect.PasswordAuthentication {
		input.Password = server.rememberedSecret(input.Password, profileID+"/node-password")
	}
	if input.Kind == nodeinspect.PrivateKeyAuthentication {
		input.PrivateKey = server.rememberedSecret(input.PrivateKey, profileID+"/node-private-key")
		input.KeyPassphrase = server.rememberedSecret(input.KeyPassphrase, profileID+"/node-key-passphrase")
	}
	input.SudoPassword = server.rememberedSecret(input.SudoPassword, profileID+"/node-sudo-password")

	credentials := nodeinspect.Credentials{Kind: input.Kind, Password: input.Password, PrivateKey: []byte(input.PrivateKey), KeyPassphrase: input.KeyPassphrase, SudoPassword: input.SudoPassword}
	if input.Kind == nodeinspect.PasswordAuthentication && input.Password == "" || input.Kind == nodeinspect.PrivateKeyAuthentication && input.PrivateKey == "" {
		return nodeinspect.Credentials{}, fmt.Errorf("missing node authentication material")
	}
	for _, secret := range []struct{ key, value string }{{"password", input.Password}, {"private-key", input.PrivateKey}, {"key-passphrase", input.KeyPassphrase}, {"sudo-password", input.SudoPassword}} {
		if secret.value == "" {
			continue
		}
		if err := server.vault.Store(profileID+"/node-"+secret.key, secret.value); err != nil {
			return nodeinspect.Credentials{}, err
		}
		expiresAt := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
		if err := server.store.UpsertCredentialReference(ctx, state.CredentialReference{ProfileID: profileID, Kind: "node-" + secret.key, VaultKey: profileID + "/node-" + secret.key, Source: "operator", ExpiresAt: expiresAt, RotationStatus: credentialRotationStatus(expiresAt, time.Now())}); err != nil {
			return nodeinspect.Credentials{}, err
		}
	}
	return credentials, nil
}

func (server *Server) capabilities(response http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedSession(request); !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	writeJSON(response, http.StatusOK, capability.DefaultCatalog())
}

func (server *Server) capabilityPlan(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input capabilityRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_capability_selection")
		return
	}
	profile, err := server.store.GetProfile(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "capability_unavailable")
		return
	}
	overlay, err := capability.DefaultCatalog().RenderOverlay(capability.OverlayInput{Selection: capability.Selection{Mode: input.Mode, DeploymentMode: capability.DeploymentMode(profile.DeploymentMode), CommunityIDs: input.CommunityIDs}, Release: input.Release, RepositoryURL: input.RepositoryURL, Domain: input.Domain, EnvironmentExtension: input.EnvironmentExtension})
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_capability_selection")
		return
	}
	plan, err := server.workflow.PlanChange(request.Context(), profile.ID, "ApplyCapabilities", overlay.Diff, []workflow.Effect{{Code: "gitops.overlay.previewed", MessageKey: "plan.effect.gitops_overlay"}})
	if err != nil {
		writeError(response, http.StatusInternalServerError, "plan_creation_failed")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"plan": plan, "overlay": overlay})
}

func (server *Server) plan(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, "/api/v1/plans/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "approve" || request.Method != http.MethodPost {
		http.NotFound(response, request)
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	// Preserve-data plans with unresolved ownership remain reviewable but cannot
	// be approved. This is a server-side gate; a browser cannot turn an unknown
	// provider resource into a deletion by enabling a button.
	if record, err := server.store.GetDecommissionPlan(request.Context(), parts[0]); err == nil {
		var binding preserveDecommissionBinding
		if json.Unmarshal([]byte(record.Binding), &binding) != nil || !binding.Plan.Approvable() {
			writeError(response, http.StatusConflict, "decommission_ownership_unresolved")
			return
		}
	}
	// Full decommission requires its own typed confirmation bound to the exact
	// profile and digest; ordinary button approval is intentionally insufficient.
	if candidate, err := server.store.GetPlan(request.Context(), parts[0]); err == nil && candidate.Intent == fullDecommissionIntent {
		writeError(response, http.StatusConflict, "full_decommission_typed_confirmation_required")
		return
	}
	run, err := server.workflow.Approve(request.Context(), parts[0])
	if errors.Is(err, workflow.ErrPreconditionChanged) {
		writeError(response, http.StatusConflict, "plan_precondition_changed")
		return
	}
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "plan_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "plan_approval_failed")
		return
	}
	writeJSON(response, http.StatusAccepted, run)
}

func (server *Server) profile(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, "/api/v1/profiles/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(response, request)
		return
	}
	profileID := parts[0]
	if len(parts) == 2 && parts[1] == "forget" && request.Method == http.MethodPost {
		if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
			writeError(response, http.StatusForbidden, "csrf_required")
			return
		}
		var input struct {
			ConfirmProfileID string `json:"confirmProfileId"`
		}
		if !decodeJSON(response, request, 4*1024, &input) || input.ConfirmProfileID != profileID {
			writeError(response, http.StatusBadRequest, "profile_forget_confirmation_required")
			return
		}
		if err := server.store.ForgetProfile(request.Context(), profileID); errors.Is(err, state.ErrNotFound) {
			writeError(response, http.StatusNotFound, "profile_not_found")
			return
		} else if err != nil {
			writeError(response, http.StatusInternalServerError, "profile_forget_failed")
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"profileId": profileID, "forgotten": true, "externalMutation": false})
		return
	}

	if len(parts) == 2 && parts[1] == "journey" && request.Method == http.MethodGet {
		journey, err := server.workflow.Journey(request.Context(), profileID)
		if errors.Is(err, state.ErrNotFound) {
			writeError(response, http.StatusNotFound, "profile_not_found")
			return
		}
		if err != nil {
			writeError(response, http.StatusInternalServerError, "journey_unavailable")
			return
		}
		writeJSON(response, http.StatusOK, journey)
		return
	}
	if len(parts) == 2 && parts[1] == "settings" {
		server.profileSetupSettings(response, request, current, profileID)
		return
	}
	// Recorded, secret-free evidence a returning Operator needs in order to see
	// where an existing cluster stands. Without these, everything the Launcher
	// observed in an earlier session would live only in the browser tab that
	// observed it, and reopening a profile would look like starting over.
	if len(parts) == 2 && parts[1] == "node-trust" && request.Method == http.MethodGet {
		trust, err := server.store.GetNodeTrust(request.Context(), profileID)
		if errors.Is(err, state.ErrNotFound) {
			writeError(response, http.StatusNotFound, "node_trust_not_recorded")
			return
		}
		if err != nil {
			writeError(response, http.StatusInternalServerError, "node_trust_unavailable")
			return
		}
		writeJSON(response, http.StatusOK, trust)
		return
	}
	if len(parts) == 2 && parts[1] == "overlay" && request.Method == http.MethodGet {
		overlay, err := server.store.GetOverlayIdentity(request.Context(), profileID)
		if errors.Is(err, state.ErrNotFound) {
			writeError(response, http.StatusNotFound, "overlay_not_established")
			return
		}
		if err != nil {
			writeError(response, http.StatusInternalServerError, "overlay_unavailable")
			return
		}
		writeJSON(response, http.StatusOK, overlay)
		return
	}
	if len(parts) >= 2 && parts[1] == "credentials" {
		server.profileCredentials(response, request, current, profileID, parts[2:])
		return
	}

	if len(parts) == 1 && request.Method == http.MethodPut {
		if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
			writeError(response, http.StatusForbidden, "csrf_required")
			return
		}
		var input struct {
			Name           string `json:"name"`
			Language       string `json:"language"`
			DeploymentMode string `json:"deploymentMode"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 16*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || !validProfileInput(input.Name, input.Language, input.DeploymentMode) {
			writeError(response, http.StatusBadRequest, "invalid_profile")
			return
		}
		profile, err := server.store.UpdateProfile(request.Context(), profileID, strings.TrimSpace(input.Name), input.Language, input.DeploymentMode)
		if errors.Is(err, state.ErrNotFound) {
			writeError(response, http.StatusNotFound, "profile_not_found")
			return
		}
		if err != nil {
			writeError(response, http.StatusInternalServerError, "profile_update_failed")
			return
		}
		writeJSON(response, http.StatusOK, profile)
		return
	}

	http.NotFound(response, request)
}

// profileSetupSettings reads and writes the non-secret answers an operator gave
// while walking the setup journey, so reopening the console — or restarting the
// launcher — never asks for the same domain or host name twice.
//
// It deliberately does not require an unlocked vault: nothing here is a secret,
// and the browser needs these values to restore the journey before the operator
// has unlocked anything. Secret material continues to travel only through the
// endpoints that write it into the vault.
func (server *Server) profileSetupSettings(response http.ResponseWriter, request *http.Request, current session, profileID string) {
	if _, err := server.store.GetProfile(request.Context(), profileID); errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "settings_unavailable")
		return
	}

	switch request.Method {
	case http.MethodGet:
		settings, err := server.store.GetSetupSettings(request.Context(), profileID)
		if errors.Is(err, state.ErrNotFound) {
			settings = state.SetupSettings{ProfileID: profileID}
		} else if err != nil {
			writeError(response, http.StatusInternalServerError, "settings_unavailable")
			return
		}
		// A confirmed node trust is the authoritative record of which machine
		// this profile talks to. Backfill from it so a profile created before
		// settings existed — or recovered from a bundle — still opens with the
		// host prefilled.
		if settings.NodeHost == "" {
			if trust, trustErr := server.store.GetNodeTrust(request.Context(), profileID); trustErr == nil {
				settings.NodeTargetKind = "remote"
				settings.NodeHost = trust.Host
				settings.NodePort = trust.Port
				settings.NodeUsername = trust.Username
			}
		}
		writeJSON(response, http.StatusOK, settings)
	case http.MethodPut:
		if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
			writeError(response, http.StatusForbidden, "csrf_required")
			return
		}
		var input state.SetupSettings
		decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 64*1024))
		// Unknown fields are rejected rather than ignored: that is what keeps a
		// browser from smuggling a password into this table under a new name.
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeError(response, http.StatusBadRequest, "invalid_settings")
			return
		}
		input.ProfileID = profileID
		input.RecordedAt = time.Now().UTC()
		if err := server.store.RecordSetupSettings(request.Context(), input); err != nil {
			writeError(response, http.StatusInternalServerError, "settings_storage_failed")
			return
		}
		writeJSON(response, http.StatusOK, input)
	default:
		http.NotFound(response, request)
	}
}

type credentialMetadata struct {
	Kind           string `json:"kind"`
	Present        bool   `json:"present"`
	Source         string `json:"source"`
	ExpiresAt      string `json:"expiresAt"`
	RotationStatus string `json:"rotationStatus"`
}

func (server *Server) profileCredentials(response http.ResponseWriter, request *http.Request, current session, profileID string, remainder []string) {
	if _, err := server.store.GetProfile(request.Context(), profileID); errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "credentials_unavailable")
		return
	}
	if len(remainder) == 0 && request.Method == http.MethodGet {
		if server.vault.Status(request.Context()).State != "unlocked" {
			writeError(response, http.StatusLocked, "vault_locked")
			return
		}
		references, err := server.store.ListCredentialReferences(request.Context(), profileID)
		if err != nil {
			writeError(response, http.StatusInternalServerError, "credentials_unavailable")
			return
		}
		metadata := make([]credentialMetadata, 0, len(references))
		for _, reference := range references {
			present, err := server.vault.Contains(reference.VaultKey)
			if err != nil {
				writeError(response, http.StatusLocked, "vault_locked")
				return
			}
			metadata = append(metadata, credentialMetadata{
				Kind:           reference.Kind,
				Present:        present,
				Source:         reference.Source,
				ExpiresAt:      reference.ExpiresAt.UTC().Format(time.RFC3339),
				RotationStatus: credentialRotationStatus(reference.ExpiresAt, time.Now()),
			})
		}
		writeJSON(response, http.StatusOK, metadata)
		return
	}
	if len(remainder) == 1 && request.Method == http.MethodPut {
		if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
			writeError(response, http.StatusForbidden, "csrf_required")
			return
		}
		kind := remainder[0]
		if kind != "git-provider-token" {
			writeError(response, http.StatusBadRequest, "unsupported_credential_kind")
			return
		}
		var input struct {
			Value     string `json:"value"`
			ExpiresAt string `json:"expiresAt"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 64*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || input.Value == "" {
			writeError(response, http.StatusBadRequest, "invalid_credential")
			return
		}
		expiresAt, err := time.Parse(time.RFC3339, input.ExpiresAt)
		if err != nil {
			writeError(response, http.StatusBadRequest, "invalid_credential_expiry")
			return
		}
		vaultKey := profileID + "/" + kind
		if err := server.vault.Store(vaultKey, input.Value); errors.Is(err, vault.ErrLocked) {
			writeError(response, http.StatusLocked, "vault_locked")
			return
		} else if err != nil {
			writeError(response, http.StatusInternalServerError, "credential_storage_failed")
			return
		}
		rotationStatus := credentialRotationStatus(expiresAt, time.Now())
		if err := server.store.UpsertCredentialReference(request.Context(), state.CredentialReference{
			ProfileID:      profileID,
			Kind:           kind,
			VaultKey:       vaultKey,
			Source:         "operator",
			ExpiresAt:      expiresAt,
			RotationStatus: rotationStatus,
		}); err != nil {
			writeError(response, http.StatusInternalServerError, "credential_storage_failed")
			return
		}
		writeJSON(response, http.StatusOK, credentialMetadata{
			Kind:           kind,
			Present:        true,
			Source:         "operator",
			ExpiresAt:      expiresAt.UTC().Format(time.RFC3339),
			RotationStatus: rotationStatus,
		})
		return
	}
	if len(remainder) == 1 && request.Method == http.MethodDelete {
		if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
			writeError(response, http.StatusForbidden, "csrf_required")
			return
		}
		kind := remainder[0]
		vaultKey := profileID + "/" + kind
		if err := server.vault.Delete(vaultKey); errors.Is(err, vault.ErrLocked) {
			writeError(response, http.StatusLocked, "vault_locked")
			return
		} else if errors.Is(err, vault.ErrSecretNotFound) {
			writeError(response, http.StatusNotFound, "credential_not_found")
			return
		} else if err != nil {
			writeError(response, http.StatusInternalServerError, "credential_removal_failed")
			return
		}
		if err := server.store.DeleteCredentialReference(request.Context(), profileID, kind); err != nil {
			writeError(response, http.StatusInternalServerError, "credential_removal_failed")
			return
		}
		response.WriteHeader(http.StatusNoContent)
		return
	}
	http.NotFound(response, request)
}

func credentialRotationStatus(expiresAt, now time.Time) string {
	if !expiresAt.After(now) {
		return "expired"
	}
	if expiresAt.Before(now.Add(30 * 24 * time.Hour)) {
		return "due-soon"
	}
	return "current"
}

func (server *Server) profiles(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}

	switch request.Method {
	case http.MethodGet:
		profiles, err := server.store.ListProfiles(request.Context())
		if err != nil {
			writeError(response, http.StatusInternalServerError, "profiles_unavailable")
			return
		}
		writeJSON(response, http.StatusOK, profiles)
	case http.MethodPost:
		if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
			writeError(response, http.StatusForbidden, "csrf_required")
			return
		}
		var input struct {
			Name           string `json:"name"`
			Language       string `json:"language"`
			DeploymentMode string `json:"deploymentMode"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 16*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || !validProfileInput(input.Name, input.Language, input.DeploymentMode) {
			writeError(response, http.StatusBadRequest, "invalid_profile")
			return
		}
		id, err := randomToken()
		if err != nil {
			writeError(response, http.StatusInternalServerError, "profile_creation_failed")
			return
		}
		profile, err := server.store.CreateProfile(request.Context(), state.Profile{
			ID:             id,
			Name:           strings.TrimSpace(input.Name),
			Language:       input.Language,
			DeploymentMode: input.DeploymentMode,
		})
		if err != nil {
			writeError(response, http.StatusInternalServerError, "profile_creation_failed")
			return
		}
		writeJSON(response, http.StatusCreated, profile)
	default:
		response.Header().Set("Allow", "GET, POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func validProfileInput(name, language, deploymentMode string) bool {
	if strings.TrimSpace(name) == "" || len(name) > 120 {
		return false
	}
	if language != "en" && language != "de" {
		return false
	}
	switch deploymentMode {
	case "hetzner", "local-lan", "local-public":
		return true
	default:
		return false
	}
}

func (server *Server) exchangeSession(response http.ResponseWriter, request *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request")
		return
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	if server.tokenUsed || !sameToken(body.Token, server.launchToken) {
		writeError(response, http.StatusUnauthorized, "invalid_launch_token")
		return
	}

	sessionID, err := randomToken()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "session_creation_failed")
		return
	}
	csrfToken, err := randomToken()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "session_creation_failed")
		return
	}

	server.tokenUsed = true
	expiresAt := time.Now().Add(sessionLifetime)
	server.sessions[sessionID] = session{csrfToken: csrfToken, expiresAt: expiresAt}
	http.SetCookie(response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionLifetime.Seconds()),
		Expires:  expiresAt,
	})
	writeJSON(response, http.StatusOK, map[string]string{"csrfToken": csrfToken})
}

func (server *Server) getSession(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"authenticated": true,
		"csrfToken":     current.csrfToken,
	})
}

func (server *Server) authenticatedSession(request *http.Request) (session, bool) {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil {
		return session{}, false
	}
	server.mu.RLock()
	defer server.mu.RUnlock()
	current, ok := server.sessions[cookie.Value]
	if ok && time.Now().After(current.expiresAt) {
		return session{}, false
	}
	return current, ok
}

func sameToken(candidate, expected string) bool {
	if len(candidate) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(expected)) == 1
}

func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, status int, code string) {
	writeJSON(response, status, map[string]string{"code": code})
}

// refuseGitHubOverlay answers a failed GitHub call with the most specific
// refusal the Operator can act on, and writes the provider's own explanation to
// the launcher's output. The browser keeps seeing a stable code — GitHub's
// wording is diagnostic detail, not something to render into a journey — but
// without it a refusal like "Git Repository is empty." is invisible to
// everyone, and an Operator is told to retry something that cannot succeed.
// Reports whether the request has been answered.
func refuseGitHubOverlay(response http.ResponseWriter, operation, fallback string, err error) bool {
	if err == nil {
		return false
	}
	log.Printf("github overlay: %s: %v", operation, err)
	switch {
	case errors.Is(err, github.ErrRateLimited):
		writeError(response, http.StatusTooManyRequests, "github_rate_limited")
	case errors.Is(err, github.ErrUnauthorized), errors.Is(err, github.ErrInsufficientAuthority):
		writeError(response, http.StatusForbidden, "github_token_insufficient_authority")
	default:
		writeError(response, http.StatusBadGateway, fallback)
	}
	return true
}
