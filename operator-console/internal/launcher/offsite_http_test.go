package launcher_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
	"github.com/stephan271/smallworlds/operator-console/internal/offsite"
)

// fakeOffsiteValidationRunner returns fixed offsite evidence so a test controls
// exactly what the validation run observes — proving the verdict comes from the
// evidence, not from the runner reporting success.
type fakeOffsiteValidationRunner struct {
	evidence    offsite.ValidationEvidence
	err         error
	calls       int
	destination offsite.Destination
}

func (runner *fakeOffsiteValidationRunner) RunValidation(_ context.Context, _ string, destination offsite.Destination) (offsite.ValidationEvidence, error) {
	runner.calls++
	runner.destination = destination
	return runner.evidence, runner.err
}

// waitForOffsiteRun polls a run until it leaves the running state.
func waitForOffsiteRun(t *testing.T, handler http.Handler, cookie *http.Cookie, runID string) workflowRunResponse {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response := request(t, handler, http.MethodGet, "/api/v1/runs/"+runID, nil, cookie, nil)
		var run workflowRunResponse
		if err := json.NewDecoder(response.Body).Decode(&run); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if run.State != "running" {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %s did not settle before deadline", runID)
	return workflowRunResponse{}
}

// openOffsiteProposal drives a configured profile through an approved offsite
// plan and a submitted Git proposal, so a validation run has something to check.
func openOffsiteProposal(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, profileID string) {
	t.Helper()
	planID := approvedOffsitePlan(t, handler, cookie, csrf, profileID)
	body, _ := json.Marshal(map[string]string{"profileId": profileID, "planId": planID})
	response := request(t, handler, http.MethodPost, "/api/v1/offsite/propose", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("propose status = %d: %s", response.StatusCode, readAll(t, response))
	}
	response.Body.Close()
}

// approveOffsiteValidation asks for a validation plan and approves it, returning
// the started run's id.
func approveOffsiteValidation(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, profileID string) string {
	t.Helper()
	headers := map[string]string{"X-CSRF-Token": csrf}
	validateBody, _ := json.Marshal(map[string]string{"profileId": profileID})
	response := request(t, handler, http.MethodPost, "/api/v1/offsite/validate", validateBody, cookie, headers)
	planned := readAll(t, response)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("validate status = %d: %s", response.StatusCode, planned)
	}
	var view struct {
		Plan struct {
			ID string `json:"id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(planned, &view); err != nil {
		t.Fatal(err)
	}
	response = request(t, handler, http.MethodPost, "/api/v1/plans/"+view.Plan.ID+"/approve", nil, cookie, headers)
	approved := readAll(t, response)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("validation approve status = %d: %s", response.StatusCode, approved)
	}
	var run struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(approved, &run); err != nil {
		t.Fatal(err)
	}
	if run.ID == "" {
		t.Fatalf("approve did not start a run: %s", approved)
	}
	return run.ID
}

// recordingSecretApplier captures the last Cluster Secret applied so a test can
// prove the credential values reach the authorized secret path — and nowhere
// else.
type recordingSecretApplier struct {
	calls     int
	namespace string
	name      string
	data      map[string]string
	err       error
}

func (applier *recordingSecretApplier) ApplyClusterSecret(_ context.Context, namespace, name string, data map[string]string) error {
	applier.calls++
	if applier.err != nil {
		return applier.err
	}
	applier.namespace, applier.name, applier.data = namespace, name, data
	return nil
}

type fakeOffsiteInspector struct {
	inspection offsite.Inspection
}

func (f fakeOffsiteInspector) Inspect(context.Context, offsite.Destination, offsite.Credentials) (offsite.Inspection, error) {
	return f.inspection, nil
}

const (
	testAccessKeyID     = "003accesskeyid"
	testSecretAccessKey = "K003topsecretvalue"
)

func offsiteInspectBody(profileID string) []byte {
	body, _ := json.Marshal(map[string]string{
		"profileId":       profileID,
		"endpoint":        "https://s3.eu-central-003.backblazeb2.com",
		"region":          "eu-central-003",
		"bucket":          "community-backups",
		"accessKeyId":     testAccessKeyID,
		"secretAccessKey": testSecretAccessKey,
	})
	return body
}

func assertNoOffsiteSecret(t *testing.T, payload []byte) {
	t.Helper()
	if bytes.Contains(payload, []byte(testAccessKeyID)) || bytes.Contains(payload, []byte(testSecretAccessKey)) {
		t.Fatalf("offsite response leaked a credential value: %s", payload)
	}
}

func TestOffsiteInspectCustodiesCredentialsAndPlansVersionedDestination(t *testing.T) {
	handler, err := launcher.New(launcher.Config{
		DataDir:          t.TempDir(),
		LaunchToken:      "offsite",
		OffsiteInspector: fakeOffsiteInspector{inspection: offsite.Inspection{Reachable: true, Versioning: offsite.VersioningEnabled}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "offsite")
	profile := createProfile(t, handler, cookie, csrf, "Home", "en", "local-lan")
	headers := map[string]string{"X-CSRF-Token": csrf}

	// Inspecting before the vault is unlocked cannot custody the credentials.
	response := request(t, handler, http.MethodPost, "/api/v1/offsite/inspect", offsiteInspectBody(profile.ID), cookie, headers)
	if response.StatusCode != http.StatusLocked {
		t.Fatalf("inspect while locked status = %d, want 423", response.StatusCode)
	}
	response.Body.Close()

	unlockVaultForRecoveryTest(t, handler, cookie, csrf)

	response = request(t, handler, http.MethodPost, "/api/v1/offsite/inspect", offsiteInspectBody(profile.ID), cookie, headers)
	inspected := readAll(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("inspect status = %d: %s", response.StatusCode, inspected)
	}
	assertNoOffsiteSecret(t, inspected)
	var view struct {
		Versioning              string `json:"versioning"`
		RequiresAcknowledgement bool   `json:"requiresAcknowledgement"`
		AccessKeyFingerprint    string `json:"accessKeyFingerprint"`
		Destination             struct {
			Bucket string `json:"bucket"`
		} `json:"destination"`
	}
	if err := json.Unmarshal(inspected, &view); err != nil {
		t.Fatal(err)
	}
	if view.Versioning != "enabled" || view.RequiresAcknowledgement {
		t.Fatalf("unexpected versioning verdict: %s", inspected)
	}
	if view.AccessKeyFingerprint == "" || view.AccessKeyFingerprint == testAccessKeyID {
		t.Fatalf("fingerprint not derived: %q", view.AccessKeyFingerprint)
	}
	if view.Destination.Bucket != "community-backups" {
		t.Fatalf("destination not recorded: %s", inspected)
	}

	// The status view is secret-free too.
	response = request(t, handler, http.MethodGet, "/api/v1/offsite?profileId="+profile.ID, nil, cookie, nil)
	status := readAll(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	assertNoOffsiteSecret(t, status)

	// A versioned destination plans without an acknowledgement, and the plan
	// separates the Cluster Secret from the non-secret Git diff, leaking neither.
	planBody, _ := json.Marshal(map[string]any{"profileId": profile.ID, "acknowledged": false})
	response = request(t, handler, http.MethodPost, "/api/v1/offsite/plan", planBody, cookie, headers)
	planned := readAll(t, response)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("plan status = %d: %s", response.StatusCode, planned)
	}
	assertNoOffsiteSecret(t, planned)
	var planView struct {
		GitDiff string `json:"gitDiff"`
		Secret  struct {
			SecretName string   `json:"secretName"`
			Keys       []string `json:"keys"`
		} `json:"secret"`
	}
	if err := json.Unmarshal(planned, &planView); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains([]byte(planView.GitDiff), []byte("community-backups")) {
		t.Fatalf("git diff missing destination: %s", planned)
	}
	if planView.Secret.SecretName == "" || len(planView.Secret.Keys) != 2 {
		t.Fatalf("secret effect not described: %s", planned)
	}
}

func TestOffsitePlanRequiresAcknowledgementForUninspectableVersioning(t *testing.T) {
	// The default inspector cannot confirm versioning (reports unknown), so a plan
	// must be refused until the operator acknowledges the limitation.
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "offsite-ack"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "offsite-ack")
	profile := createProfile(t, handler, cookie, csrf, "Home", "en", "local-lan")
	headers := map[string]string{"X-CSRF-Token": csrf}
	unlockVaultForRecoveryTest(t, handler, cookie, csrf)

	response := request(t, handler, http.MethodPost, "/api/v1/offsite/inspect", offsiteInspectBody(profile.ID), cookie, headers)
	inspected := readAll(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("inspect status = %d: %s", response.StatusCode, inspected)
	}
	var view struct {
		Versioning              string `json:"versioning"`
		RequiresAcknowledgement bool   `json:"requiresAcknowledgement"`
	}
	json.Unmarshal(inspected, &view)
	if view.Versioning != "unknown" || !view.RequiresAcknowledgement {
		t.Fatalf("default inspector should report unknown versioning: %s", inspected)
	}

	unacknowledged, _ := json.Marshal(map[string]any{"profileId": profile.ID, "acknowledged": false})
	response = request(t, handler, http.MethodPost, "/api/v1/offsite/plan", unacknowledged, cookie, headers)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("unacknowledged plan status = %d, want 409", response.StatusCode)
	}
	response.Body.Close()

	acknowledged, _ := json.Marshal(map[string]any{"profileId": profile.ID, "acknowledged": true})
	response = request(t, handler, http.MethodPost, "/api/v1/offsite/plan", acknowledged, cookie, headers)
	planned := readAll(t, response)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("acknowledged plan status = %d: %s", response.StatusCode, planned)
	}
	assertNoOffsiteSecret(t, planned)
}

// establishGenericOverlay wires an approved capability plan into a generic-git
// overlay so the profile has a recorded overlay identity to propose against, and
// stores the Git credentials in the Vault.
func establishGenericOverlay(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, profileID string) {
	t.Helper()
	headers := map[string]string{"X-CSRF-Token": csrf}
	credentialBody, _ := json.Marshal(map[string]string{"profileId": profileID, "repositoryUrl": "https://git.example/overlay.git", "username": "operator", "token": "generic-secret"})
	request(t, handler, http.MethodPost, "/api/v1/generic-git/token/validate", credentialBody, cookie, headers).Body.Close()
	planID := genericCapabilityPlan(t, handler, cookie, csrf, profileID)
	request(t, handler, http.MethodPost, "/api/v1/plans/"+planID+"/approve", nil, cookie, headers).Body.Close()
	establishBody, _ := json.Marshal(map[string]any{"profileId": profileID, "planId": planID, "repositoryUrl": "https://git.example/overlay.git", "mode": "minimal", "communityIds": []string{}, "release": "v1.2.3", "domain": "home.example"})
	response := request(t, handler, http.MethodPost, "/api/v1/generic-git/overlay/establish", establishBody, cookie, headers)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("overlay establish status = %d", response.StatusCode)
	}
	response.Body.Close()
}

// approvedOffsitePlan inspects a destination, plans it, and approves the plan,
// returning the offsite Change Plan's id ready to propose.
func approvedOffsitePlan(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, profileID string) string {
	t.Helper()
	headers := map[string]string{"X-CSRF-Token": csrf}
	response := request(t, handler, http.MethodPost, "/api/v1/offsite/inspect", offsiteInspectBody(profileID), cookie, headers)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("inspect status = %d: %s", response.StatusCode, readAll(t, response))
	}
	response.Body.Close()
	planBody, _ := json.Marshal(map[string]any{"profileId": profileID, "acknowledged": false})
	response = request(t, handler, http.MethodPost, "/api/v1/offsite/plan", planBody, cookie, headers)
	planned := readAll(t, response)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("offsite plan status = %d: %s", response.StatusCode, planned)
	}
	var view struct {
		Plan struct {
			ID string `json:"id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(planned, &view); err != nil {
		t.Fatal(err)
	}
	if view.Plan.ID == "" {
		t.Fatalf("offsite plan lacks an id: %s", planned)
	}
	request(t, handler, http.MethodPost, "/api/v1/plans/"+view.Plan.ID+"/approve", nil, cookie, headers).Body.Close()
	return view.Plan.ID
}

func TestOffsiteProposeAppliesClusterSecretAndOpensGitProposal(t *testing.T) {
	stub := &genericGitStub{contains: true}
	applier := &recordingSecretApplier{}
	handler, err := launcher.New(launcher.Config{
		DataDir:              t.TempDir(),
		LaunchToken:          "offsite-propose",
		GenericGitClient:     stub,
		ClusterSecretApplier: applier,
		OffsiteInspector:     fakeOffsiteInspector{inspection: offsite.Inspection{Reachable: true, Versioning: offsite.VersioningEnabled}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "offsite-propose")
	profile := createProfile(t, handler, cookie, csrf, "Home", "en", "local-lan")
	headers := map[string]string{"X-CSRF-Token": csrf}
	unlockVaultForRecoveryTest(t, handler, cookie, csrf)
	establishGenericOverlay(t, handler, cookie, csrf, profile.ID)
	planID := approvedOffsitePlan(t, handler, cookie, csrf, profile.ID)

	proposeBody, _ := json.Marshal(map[string]string{"profileId": profile.ID, "planId": planID})
	response := request(t, handler, http.MethodPost, "/api/v1/offsite/propose", proposeBody, cookie, headers)
	proposed := readAll(t, response)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("propose status = %d: %s", response.StatusCode, proposed)
	}
	assertNoOffsiteSecret(t, proposed)
	var proposal struct {
		Provider string `json:"provider"`
		Branch   string `json:"branch"`
		Commit   string `json:"commit"`
		Secret   struct {
			SecretName string   `json:"secretName"`
			Keys       []string `json:"keys"`
		} `json:"secret"`
		MergeInstructionKey string `json:"mergeInstructionKey"`
	}
	if err := json.Unmarshal(proposed, &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.Provider != "generic-https" || !strings.HasPrefix(proposal.Branch, "smallworlds/proposal-") || proposal.Commit == "" {
		t.Fatalf("proposal identity incomplete: %s", proposed)
	}
	if proposal.MergeInstructionKey != "generic_git_manual_merge" {
		t.Fatalf("merge is not left as a human step: %s", proposed)
	}

	// The Cluster Secret received the real credential values, under the plan's
	// secret name, in the replicator namespace — and nothing else did.
	if applier.calls != 1 {
		t.Fatalf("cluster secret applier calls = %d, want 1", applier.calls)
	}
	if applier.namespace != offsite.Namespace || applier.name != proposal.Secret.SecretName {
		t.Fatalf("cluster secret target = %s/%s, want %s/%s", applier.namespace, applier.name, offsite.Namespace, proposal.Secret.SecretName)
	}
	if applier.data[offsite.KeyAccessKeyID] != testAccessKeyID || applier.data[offsite.KeySecretAccessKey] != testSecretAccessKey {
		t.Fatal("cluster secret did not receive the custodied credential values")
	}

	// The Git proposal carried only the non-secret destination config.
	if stub.proposalCalls != 1 {
		t.Fatalf("git proposal calls = %d, want 1", stub.proposalCalls)
	}
	if len(stub.proposalFiles) == 0 {
		t.Fatal("git proposal carried no files")
	}
	for path, body := range stub.proposalFiles {
		if !strings.Contains(body, "community-backups") {
			t.Fatalf("proposal file %q lost the destination", path)
		}
		if strings.Contains(body, testAccessKeyID) || strings.Contains(body, testSecretAccessKey) {
			t.Fatalf("proposal file %q leaked a credential value", path)
		}
	}

	// The proposal state and remote commit identity surface in the status view
	// and the Activity Record — secret-free.
	response = request(t, handler, http.MethodGet, "/api/v1/offsite?profileId="+profile.ID, nil, cookie, nil)
	status := readAll(t, response)
	assertNoOffsiteSecret(t, status)
	if !bytes.Contains(status, []byte("proposal-commit")) {
		t.Fatalf("status view omits the proposal commit: %s", status)
	}
	response = request(t, handler, http.MethodGet, "/api/v1/events?profileId="+profile.ID, nil, cookie, nil)
	events := readAll(t, response)
	assertNoOffsiteSecret(t, events)
	if !bytes.Contains(events, []byte("activity.offsite.proposed")) {
		t.Fatalf("activity record omits the offsite proposal: %s", events)
	}
}

func TestOffsiteProposeRefusesWithoutAnApprovedPlanOrSecretPath(t *testing.T) {
	stub := &genericGitStub{contains: true}
	handler, err := launcher.New(launcher.Config{
		DataDir:          t.TempDir(),
		LaunchToken:      "offsite-refuse",
		GenericGitClient: stub,
		// No ClusterSecretApplier: the launcher default refuses honestly.
		OffsiteInspector: fakeOffsiteInspector{inspection: offsite.Inspection{Reachable: true, Versioning: offsite.VersioningEnabled}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "offsite-refuse")
	profile := createProfile(t, handler, cookie, csrf, "Home", "en", "local-lan")
	headers := map[string]string{"X-CSRF-Token": csrf}
	unlockVaultForRecoveryTest(t, handler, cookie, csrf)
	establishGenericOverlay(t, handler, cookie, csrf, profile.ID)

	// Proposing before an offsite plan exists is a conflict.
	before, _ := json.Marshal(map[string]string{"profileId": profile.ID, "planId": "does-not-exist"})
	response := request(t, handler, http.MethodPost, "/api/v1/offsite/propose", before, cookie, headers)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("propose without plan status = %d, want 409", response.StatusCode)
	}
	response.Body.Close()

	planID := approvedOffsitePlan(t, handler, cookie, csrf, profile.ID)
	proposeBody, _ := json.Marshal(map[string]string{"profileId": profile.ID, "planId": planID})
	response = request(t, handler, http.MethodPost, "/api/v1/offsite/propose", proposeBody, cookie, headers)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("propose with no secret path status = %d, want 503", response.StatusCode)
	}
	response.Body.Close()

	// With the secret path unavailable, no Git proposal must be opened — the
	// credentials must land first.
	if stub.proposalCalls != 0 {
		t.Fatalf("git proposal opened despite unavailable secret path: %d", stub.proposalCalls)
	}
}

func TestOffsiteValidateRunClassifiesFromObservedEvidence(t *testing.T) {
	stub := &genericGitStub{contains: true}
	applier := &recordingSecretApplier{}
	runner := &fakeOffsiteValidationRunner{evidence: offsite.ValidationEvidence{
		LocalBackupSucceeded:   true,
		ReplicationSucceeded:   true,
		OffsiteRecoveryPointAt: time.Now().UTC(),
		Versioning:             offsite.VersioningEnabled,
	}}
	handler, err := launcher.New(launcher.Config{
		DataDir:                 t.TempDir(),
		LaunchToken:             "offsite-validate",
		GenericGitClient:        stub,
		ClusterSecretApplier:    applier,
		OffsiteInspector:        fakeOffsiteInspector{inspection: offsite.Inspection{Reachable: true, Versioning: offsite.VersioningEnabled}},
		OffsiteValidationRunner: runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "offsite-validate")
	profile := createProfile(t, handler, cookie, csrf, "Home", "en", "local-lan")
	unlockVaultForRecoveryTest(t, handler, cookie, csrf)
	establishGenericOverlay(t, handler, cookie, csrf, profile.ID)
	openOffsiteProposal(t, handler, cookie, csrf, profile.ID)

	runID := approveOffsiteValidation(t, handler, cookie, csrf, profile.ID)
	verified := waitForOffsiteRun(t, handler, cookie, runID)
	if verified.State != "verified" || verified.CurrentCheckpoint != "validation-complete" {
		t.Fatalf("validation run did not complete: %#v", verified)
	}
	// The verdict is the evidence-derived classification, not a Job exit status.
	if verified.Verification.Code != string(offsite.ValidationVerified) {
		t.Fatalf("run evidence = %q, want %q", verified.Verification.Code, offsite.ValidationVerified)
	}
	if runner.calls != 1 {
		t.Fatalf("validation runner calls = %d, want 1", runner.calls)
	}
	if runner.destination.Bucket != "community-backups" {
		t.Fatalf("runner did not receive the configured destination: %#v", runner.destination)
	}

	// The offsite record carries the authoritative protection verdict.
	response := request(t, handler, http.MethodGet, "/api/v1/offsite?profileId="+profile.ID, nil, cookie, nil)
	status := readAll(t, response)
	assertNoOffsiteSecret(t, status)
	var view struct {
		Validation struct {
			Result         string `json:"result"`
			RemediationKey string `json:"remediationKey"`
			Verified       bool   `json:"verified"`
			RunID          string `json:"runId"`
		} `json:"validation"`
	}
	if err := json.Unmarshal(status, &view); err != nil {
		t.Fatal(err)
	}
	if view.Validation.Result != string(offsite.ValidationVerified) || !view.Validation.Verified {
		t.Fatalf("status validation verdict = %#v", view.Validation)
	}
	if view.Validation.RemediationKey != offsite.ValidationVerified.RemediationKey() || view.Validation.RunID != runID {
		t.Fatalf("status validation route = %#v", view.Validation)
	}

	// The bounded stages and the verdict appear in the Activity Record.
	response = request(t, handler, http.MethodGet, "/api/v1/events?profileId="+profile.ID, nil, cookie, nil)
	events := readAll(t, response)
	for _, marker := range []string{"declared-work-started", "offsite-evidence-observed", "activity.run.verified"} {
		if !bytes.Contains(events, []byte(marker)) {
			t.Fatalf("activity record missing %q: %s", marker, events)
		}
	}
}

func TestOffsiteValidateDistinguishesReplicationFailureFromEvidence(t *testing.T) {
	stub := &genericGitStub{contains: true}
	applier := &recordingSecretApplier{}
	// The runner reports the local backup completed but replication did not — a
	// green local Job must not read as offsite protection.
	runner := &fakeOffsiteValidationRunner{evidence: offsite.ValidationEvidence{
		LocalBackupSucceeded: true,
		ReplicationSucceeded: false,
		Versioning:           offsite.VersioningEnabled,
	}}
	handler, err := launcher.New(launcher.Config{
		DataDir:                 t.TempDir(),
		LaunchToken:             "offsite-replfail",
		GenericGitClient:        stub,
		ClusterSecretApplier:    applier,
		OffsiteInspector:        fakeOffsiteInspector{inspection: offsite.Inspection{Reachable: true, Versioning: offsite.VersioningEnabled}},
		OffsiteValidationRunner: runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "offsite-replfail")
	profile := createProfile(t, handler, cookie, csrf, "Home", "en", "local-lan")
	unlockVaultForRecoveryTest(t, handler, cookie, csrf)
	establishGenericOverlay(t, handler, cookie, csrf, profile.ID)
	openOffsiteProposal(t, handler, cookie, csrf, profile.ID)

	runID := approveOffsiteValidation(t, handler, cookie, csrf, profile.ID)
	settled := waitForOffsiteRun(t, handler, cookie, runID)
	if settled.Verification.Code != string(offsite.ValidationReplicationFailed) {
		t.Fatalf("run evidence = %q, want %q", settled.Verification.Code, offsite.ValidationReplicationFailed)
	}
	response := request(t, handler, http.MethodGet, "/api/v1/offsite?profileId="+profile.ID, nil, cookie, nil)
	status := readAll(t, response)
	var view struct {
		Validation struct {
			Result         string `json:"result"`
			RemediationKey string `json:"remediationKey"`
			Verified       bool   `json:"verified"`
		} `json:"validation"`
	}
	json.Unmarshal(status, &view)
	if view.Validation.Verified || view.Validation.Result != string(offsite.ValidationReplicationFailed) {
		t.Fatalf("replication failure not distinguished: %#v", view.Validation)
	}
	if view.Validation.RemediationKey != offsite.ValidationReplicationFailed.RemediationKey() {
		t.Fatalf("remediation route = %q", view.Validation.RemediationKey)
	}
}

func TestOffsiteValidateRefusesWithoutAProposalAndFailsWhenUnavailable(t *testing.T) {
	stub := &genericGitStub{contains: true}
	applier := &recordingSecretApplier{}
	handler, err := launcher.New(launcher.Config{
		DataDir:              t.TempDir(),
		LaunchToken:          "offsite-noproposal",
		GenericGitClient:     stub,
		ClusterSecretApplier: applier,
		// No OffsiteValidationRunner: the launcher default refuses honestly.
		OffsiteInspector: fakeOffsiteInspector{inspection: offsite.Inspection{Reachable: true, Versioning: offsite.VersioningEnabled}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "offsite-noproposal")
	profile := createProfile(t, handler, cookie, csrf, "Home", "en", "local-lan")
	headers := map[string]string{"X-CSRF-Token": csrf}
	unlockVaultForRecoveryTest(t, handler, cookie, csrf)
	establishGenericOverlay(t, handler, cookie, csrf, profile.ID)

	// A destination configured but not yet proposed cannot be validated.
	approvedOffsitePlan(t, handler, cookie, csrf, profile.ID)
	validateBody, _ := json.Marshal(map[string]string{"profileId": profile.ID})
	response := request(t, handler, http.MethodPost, "/api/v1/offsite/validate", validateBody, cookie, headers)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("validate before proposal status = %d, want 409", response.StatusCode)
	}
	response.Body.Close()

	// Once proposed, the run starts but the unavailable runner fails honestly
	// without inventing a verdict.
	openOffsiteProposal(t, handler, cookie, csrf, profile.ID)
	runID := approveOffsiteValidation(t, handler, cookie, csrf, profile.ID)
	settled := waitForOffsiteRun(t, handler, cookie, runID)
	if settled.State != "failed" || settled.CurrentCheckpoint != "validation-unavailable" {
		t.Fatalf("unavailable validation run = %#v, want failed/validation-unavailable", settled)
	}
	if settled.Verification.Code != "" {
		t.Fatalf("failed run fabricated a verdict: %q", settled.Verification.Code)
	}
	response = request(t, handler, http.MethodGet, "/api/v1/offsite?profileId="+profile.ID, nil, cookie, nil)
	status := readAll(t, response)
	if bytes.Contains(status, []byte("\"validation\"")) {
		t.Fatalf("offsite record recorded a verdict despite no evidence: %s", status)
	}
}
