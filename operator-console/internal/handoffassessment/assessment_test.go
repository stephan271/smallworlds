package handoffassessment_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/handoffassessment"
)

func completeInputs() handoffassessment.Inputs {
	return handoffassessment.Inputs{
		DeviceTrustInstalled: true, PrivateNetworkReady: true, LauncherEnrolled: true,
		GatewayIdentityReady: true, GatewayAccessEnforced: true, HandoffVerified: true,
		TemporaryAccessClosed: true, OwnerRegistered: true, BootstrapGrantDisabled: true,
		ConsoleHost: "console.smallworlds.internal",
	}
}

func TestCompleteAssessmentProvidesConsoleHandoffURL(t *testing.T) {
	assessment, err := handoffassessment.Evaluate(handoffassessment.LANOnly, completeInputs())
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !assessment.Complete {
		t.Fatal("complete inputs did not produce a complete assessment")
	}
	if assessment.ConsoleHandoffURL != "https://console.smallworlds.internal" {
		t.Fatalf("console handoff URL = %q", assessment.ConsoleHandoffURL)
	}
	if len(assessment.Limitations) == 0 {
		t.Fatal("LAN-only limitations missing")
	}
	if len(assessment.Steps) != 8 {
		t.Fatalf("steps = %d, want 8", len(assessment.Steps))
	}
}

func TestIncompleteAssessmentWithholdsURLButStatesLimitations(t *testing.T) {
	inputs := completeInputs()
	inputs.TemporaryAccessClosed = false
	assessment, err := handoffassessment.Evaluate(handoffassessment.LANOnly, inputs)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if assessment.Complete {
		t.Fatal("assessment complete despite an incomplete step")
	}
	if assessment.ConsoleHandoffURL != "" {
		t.Fatalf("console URL provided before completion: %q", assessment.ConsoleHandoffURL)
	}
	if len(assessment.Limitations) == 0 {
		t.Fatal("limitations must be stated even when incomplete")
	}
}

func TestFirstOwnerStepRequiresBothRegistrationAndDisabledGrant(t *testing.T) {
	inputs := completeInputs()
	inputs.BootstrapGrantDisabled = false
	assessment, _ := handoffassessment.Evaluate(handoffassessment.LANOnly, inputs)
	if assessment.Complete {
		t.Fatal("registration without a disabled grant counted as complete")
	}
	for _, step := range assessment.Steps {
		if step.Name == handoffassessment.StepFirstOwnerRegistered && step.Complete {
			t.Fatal("first-owner step complete without the grant disabled")
		}
	}
}

// A publicly addressed installation has no private root to install, so listing
// the Cluster CA trust step would leave a finished handoff permanently
// incomplete.
func TestPubliclyAddressedAssessmentOmitsTheClusterCATrustStep(t *testing.T) {
	inputs := completeInputs()
	inputs.DeviceTrustInstalled = false
	assessment, err := handoffassessment.Evaluate(handoffassessment.PubliclyAddressed, inputs)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !assessment.Complete {
		t.Fatalf("a finished publicly addressed handoff was reported incomplete: %+v", assessment.Steps)
	}
	if len(assessment.Steps) != 7 {
		t.Fatalf("steps = %d, want 7 without the Cluster CA trust step", len(assessment.Steps))
	}
	for _, step := range assessment.Steps {
		if step.Name == handoffassessment.StepClusterCATrust {
			t.Fatal("a publicly addressed installation has no Cluster CA trust step")
		}
	}
	if assessment.ConsoleHandoffURL != "https://console.smallworlds.internal" {
		t.Fatalf("console handoff URL = %q", assessment.ConsoleHandoffURL)
	}
}

// The limitations are the part an Operator acts on, so each mode must state its
// own — and a LAN-only caveat shown to a publicly addressed installation (or the
// reverse) would be actively misleading.
func TestLimitationsAreModeSpecific(t *testing.T) {
	lan, err := handoffassessment.Evaluate(handoffassessment.LANOnly, completeInputs())
	if err != nil {
		t.Fatal(err)
	}
	public, err := handoffassessment.Evaluate(handoffassessment.PubliclyAddressed, completeInputs())
	if err != nil {
		t.Fatal(err)
	}
	if len(lan.Limitations) == 0 || len(public.Limitations) == 0 {
		t.Fatal("both modes must state limitations")
	}
	for _, limitation := range lan.Limitations {
		for _, other := range public.Limitations {
			if limitation == other {
				t.Fatalf("limitation %q is stated for both modes", limitation)
			}
		}
	}
	// A publicly addressed installation costs money until it is decommissioned,
	// and that is the caveat an Operator most needs at handoff.
	joined := strings.Join(public.Limitations, "\n")
	if !strings.Contains(joined, "charges") {
		t.Fatalf("recurring provider charges are not stated:\n%s", joined)
	}
	if !strings.Contains(joined, "no public route") {
		t.Fatalf("the private-only operator interfaces are not stated:\n%s", joined)
	}
}

// The mode is never guessed: defaulting it would state the wrong limitations.
func TestEvaluateRejectsAnUnknownMode(t *testing.T) {
	if _, err := handoffassessment.Evaluate(handoffassessment.Mode("somewhere-else"), completeInputs()); !errors.Is(err, handoffassessment.ErrInvalidMode) {
		t.Fatalf("err = %v, want ErrInvalidMode", err)
	}
}

func TestCompleteAssessmentRejectsInvalidConsoleHost(t *testing.T) {
	inputs := completeInputs()
	inputs.ConsoleHost = "console"
	if _, err := handoffassessment.Evaluate(handoffassessment.LANOnly, inputs); err == nil {
		t.Fatal("complete assessment accepted an invalid console host")
	}
}
