package launcher_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/ephemeralcluster"
	"github.com/stephan271/smallworlds/operator-console/internal/hetzner"
	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
)

// TestEphemeralHetznerCluster drives the Hetzner journey against a real project
// and destroys everything it created.
//
// It is opt-in, and deliberately so: it spends real money in someone's Hetzner
// account. It runs only when SMALLWORLDS_EPHEMERAL_CLUSTER=1 is set together
// with a project token and a delegated domain, so no CI run and no `go test
// ./...` can start it by accident.
//
// The guard is the load-bearing part. It refuses a plan over the cost cap before
// anything exists, bounds the whole run, and destroys what was created on
// success, on failure, on panic, and on timeout. Its behaviour is covered
// deterministically in internal/ephemeralcluster; this test is what exercises it
// against a provider.
//
//	SMALLWORLDS_EPHEMERAL_CLUSTER=1 \
//	HCLOUD_TOKEN=... SMALLWORLDS_TEST_DOMAIN=example.org \
//	go test ./internal/launcher/ -run TestEphemeralHetznerCluster -v -timeout 60m
func TestEphemeralHetznerCluster(t *testing.T) {
	if os.Getenv("SMALLWORLDS_EPHEMERAL_CLUSTER") != "1" {
		t.Skip("set SMALLWORLDS_EPHEMERAL_CLUSTER=1 to run the ephemeral-cluster test; it creates real, billable infrastructure")
	}
	token := os.Getenv("HCLOUD_TOKEN")
	domain := os.Getenv("SMALLWORLDS_TEST_DOMAIN")
	if token == "" || domain == "" {
		t.Skip("HCLOUD_TOKEN and SMALLWORLDS_TEST_DOMAIN are required")
	}

	limits := ephemeralcluster.DefaultLimits()
	if override := os.Getenv("SMALLWORLDS_EPHEMERAL_MAX_EUR"); override != "" {
		parsed, err := strconv.ParseFloat(override, 64)
		if err != nil {
			t.Fatalf("SMALLWORLDS_EPHEMERAL_MAX_EUR: %v", err)
		}
		limits.MaxMonthlyEUR = parsed
	}
	if override := os.Getenv("SMALLWORLDS_EPHEMERAL_MAX_DURATION"); override != "" {
		parsed, err := time.ParseDuration(override)
		if err != nil {
			t.Fatalf("SMALLWORLDS_EPHEMERAL_MAX_DURATION: %v", err)
		}
		limits.MaxDuration = parsed
	}

	// A profile suffix keeps every resource this run creates distinguishable
	// from anything already in the project, so cleanup can never target a
	// resource it did not make.
	envExt := ".ephemeral" + strconv.FormatInt(time.Now().UTC().Unix()%100000, 10)

	handler, err := launcher.New(launcher.Config{
		DataDir: t.TempDir(), LaunchToken: "ephemeral",
		HetznerProvider: hetzner.NewClient(hetzner.DefaultAPIBaseURL, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "ephemeral")
	profile := createProfile(t, handler, cookie, csrf, "Ephemeral", "en", "hetzner")
	unlockVaultForRecoveryTest(t, handler, cookie, csrf)

	guard, err := ephemeralcluster.NewGuard(limits, func(ctx context.Context) error {
		return destroyEphemeralInfrastructure(ctx, handler, cookie, csrf, profile.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("envelope: at most %.2f EUR/month, at most %s, resources suffixed %q", limits.MaxMonthlyEUR, limits.MaxDuration, envExt)

	runErr := guard.Run(context.Background(), func(ctx context.Context) error {
		if status, body := postHetzner(t, handler, cookie, csrf, "/api/v1/hetzner/token/validate", map[string]string{"profileId": profile.ID, "token": token}); status != http.StatusOK {
			return errors.New("token validation failed: " + body)
		}
		status, body := postHetzner(t, handler, cookie, csrf, "/api/v1/hetzner/inspect", map[string]string{"profileId": profile.ID, "domain": domain, "envExt": envExt})
		if status != http.StatusOK {
			return errors.New("inspection failed: " + body)
		}
		establishHetznerOverlay(t, handler, cookie, csrf, profile.ID)

		status, planBody := postHetzner(t, handler, cookie, csrf, "/api/v1/hetzner/plan", map[string]any{
			"profileId": profile.ID, "mode": "minimal", "communityIds": []string{},
			"tier": "small", "acmeEmail": "operator@" + domain, "adoptions": []string{},
		})
		if status == http.StatusConflict && strings.Contains(planBody, "hetzner_toolchain_unavailable") {
			// The pinned toolchain has not been published for this platform, so
			// no plan can be applied. Nothing was created, so there is nothing to
			// leak; the run stops here honestly rather than reporting a pass it
			// did not earn.
			t.Skip("no verified pinned toolchain is published; the journey cannot provision on this platform yet")
			return nil
		}
		if status != http.StatusCreated {
			return errors.New("planning failed: " + planBody)
		}
		var plan struct {
			Plan struct {
				ID string `json:"id"`
			} `json:"plan"`
			ChangePlan hetzner.ChangePlan `json:"changePlan"`
			Approvable bool               `json:"approvable"`
		}
		if err := json.Unmarshal([]byte(planBody), &plan); err != nil {
			return err
		}
		if !plan.Approvable {
			return errors.New("plan is blocked: " + planBody)
		}
		// The cost gate, before the first thing that can create anything.
		if err := guard.Admit(plan.ChangePlan.Cost.TotalMonthlyEUR); err != nil {
			return err
		}
		// And the cleanup gate. Approving is the step that starts costing money,
		// so it does not happen while there is no way to stop it costing money.
		// Skipping here leaves the project exactly as it was found.
		if !ephemeralDestroyAvailable {
			t.Skip("no automated destroy path exists yet (issue 23); refusing to create infrastructure that cannot be destroyed automatically")
			return nil
		}
		t.Logf("approving plan %s at %.2f EUR/month", plan.ChangePlan.Digest[:12], plan.ChangePlan.Cost.TotalMonthlyEUR)

		response := request(t, handler, http.MethodPost, "/api/v1/plans/"+plan.Plan.ID+"/approve", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
		approved := string(readAll(t, response))
		if response.StatusCode != http.StatusAccepted {
			return errors.New("approval failed: " + approved)
		}
		var run struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(approved), &run); err != nil {
			return err
		}
		if err := awaitEphemeralRun(ctx, t, handler, cookie, run.ID); err != nil {
			return err
		}

		// The journey's own answer to "is this finished?" — the same assessment
		// the Operator sees, not a bespoke set of assertions.
		assessment := request(t, handler, http.MethodGet, "/api/v1/handoff-assessment?profileId="+profile.ID, nil, cookie, nil)
		body = string(readAll(t, assessment))
		if assessment.StatusCode != http.StatusOK {
			return errors.New("final assessment unavailable: " + body)
		}
		t.Logf("final assessment: %s", body)
		if !strings.Contains(body, `"limitations"`) {
			return errors.New("final assessment does not state the installation's limitations: " + body)
		}
		return nil
	})

	// Cleanup is asserted, not assumed: a run that passed without destroying
	// what it made is a failure, because the bill keeps arriving.
	if !guard.CleanedUp() {
		t.Fatal("the run finished without cleaning up; resources may still exist and still bill")
	}
	if errors.Is(runErr, ephemeralcluster.ErrCleanupFailed) {
		t.Fatalf("CLEANUP FAILED — check the Hetzner project for resources suffixed %q: %v", envExt, runErr)
	}
	if runErr != nil {
		t.Fatalf("ephemeral cluster run: %v", runErr)
	}
}

// awaitEphemeralRun waits for the provisioning run to settle, honouring the
// guard's deadline through the context.
func awaitEphemeralRun(ctx context.Context, t *testing.T, handler http.Handler, cookie *http.Cookie, runID string) error {
	t.Helper()
	for {
		response := request(t, handler, http.MethodGet, "/api/v1/runs/"+runID, nil, cookie, nil)
		body := readAll(t, response)
		var run struct {
			State             string `json:"state"`
			CurrentCheckpoint string `json:"currentCheckpoint"`
		}
		if err := json.Unmarshal(body, &run); err != nil {
			return err
		}
		switch run.State {
		case "verified":
			return nil
		case "failed", "cancelled":
			return errors.New("run " + run.State + " at " + run.CurrentCheckpoint)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(15 * time.Second):
		}
	}
}

// ephemeralDestroyAvailable records whether an automated destroy path exists.
//
// It is false, and the test refuses to approve a plan while it is: creating
// paid infrastructure that only a human can remove is exactly the leak this
// whole envelope exists to prevent. Decommissioning is issue 23; when it lands,
// destroyEphemeralInfrastructure drives it and this becomes true.
const ephemeralDestroyAvailable = false

// destroyEphemeralInfrastructure removes what the run created.
//
// A half-written implementation here would be worse than none — it would look
// like cleanup while leaving resources behind — so until the decommissioning
// journey exists this fails loudly. The failure is unreachable in practice,
// because the run refuses to create anything while ephemeralDestroyAvailable is
// false; it is the belt to that braces.
func destroyEphemeralInfrastructure(context.Context, *launcher.Server, *http.Cookie, string, string) error {
	if !ephemeralDestroyAvailable {
		return errors.New("no automated destroy path exists yet (see issue 23); check the Hetzner project by hand")
	}
	return nil
}
