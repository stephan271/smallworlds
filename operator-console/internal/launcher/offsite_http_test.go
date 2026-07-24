package launcher_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
	"github.com/stephan271/smallworlds/operator-console/internal/offsite"
)

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
