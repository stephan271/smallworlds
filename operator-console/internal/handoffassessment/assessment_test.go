package handoffassessment_test

import (
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
	assessment, err := handoffassessment.Evaluate(completeInputs())
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
	assessment, err := handoffassessment.Evaluate(inputs)
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
	assessment, _ := handoffassessment.Evaluate(inputs)
	if assessment.Complete {
		t.Fatal("registration without a disabled grant counted as complete")
	}
	for _, step := range assessment.Steps {
		if step.Name == handoffassessment.StepFirstOwnerRegistered && step.Complete {
			t.Fatal("first-owner step complete without the grant disabled")
		}
	}
}

func TestCompleteAssessmentRejectsInvalidConsoleHost(t *testing.T) {
	inputs := completeInputs()
	inputs.ConsoleHost = "console"
	if _, err := handoffassessment.Evaluate(inputs); err == nil {
		t.Fatal("complete assessment accepted an invalid console host")
	}
}
