package enrollment_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/enrollment"
)

func TestPlanDerivesShortLivedLauncherAndStableGateway(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	reference, err := enrollment.Plan("smallworlds.internal", now)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	launcher := reference.Launcher
	if launcher.Role != enrollment.LauncherHostRole || !launcher.SingleUse || launcher.Stable {
		t.Fatalf("launcher credential not short-lived single-use: %#v", launcher)
	}
	if launcher.ExpiresAt == nil || !launcher.ExpiresAt.After(launcher.IssuedAt) {
		t.Fatal("launcher credential missing a bounded expiry")
	}
	if launcher.Hostname != "launcher.smallworlds.internal" {
		t.Fatalf("launcher hostname = %q", launcher.Hostname)
	}

	gateway := reference.Gateway
	if gateway.Role != enrollment.GatewayRole || gateway.SingleUse || !gateway.Stable || gateway.ExpiresAt != nil {
		t.Fatalf("gateway identity not stable/durable: %#v", gateway)
	}
	if gateway.Hostname != "gateway.smallworlds.internal" {
		t.Fatalf("gateway hostname = %q", gateway.Hostname)
	}

	// The two identities are distinct.
	if launcher.Hostname == gateway.Hostname {
		t.Fatal("launcher and gateway share an identity")
	}

	encoded, _ := reference.Marshal()
	if strings.Contains(encoded, "secret") || strings.Contains(encoded, "KEY") {
		t.Fatalf("reference leaked secret material: %s", encoded)
	}
}

func TestConsumeLauncherIsSingleUse(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	reference, err := enrollment.Plan("smallworlds.internal", now)
	if err != nil {
		t.Fatal(err)
	}
	if !reference.LauncherValid(now) {
		t.Fatal("fresh launcher credential should be valid")
	}
	consumed, err := reference.ConsumeLauncher(now)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if !consumed.Launcher.Used || consumed.LauncherValid(now) {
		t.Fatal("consumed launcher credential still valid")
	}
	if _, err := consumed.ConsumeLauncher(now); !errors.Is(err, enrollment.ErrLauncherAlreadyUsed) {
		t.Fatalf("second consume error = %v, want already-used", err)
	}
	// The stable gateway identity is untouched by launcher consumption.
	if consumed.Gateway.Used {
		t.Fatal("gateway identity consumed alongside launcher")
	}
}

func TestConsumeLauncherRejectsExpiredCredential(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	reference, err := enrollment.Plan("smallworlds.internal", now)
	if err != nil {
		t.Fatal(err)
	}
	later := now.Add(30 * time.Minute)
	if reference.LauncherValid(later) {
		t.Fatal("launcher credential should have expired")
	}
	if _, err := reference.ConsumeLauncher(later); !errors.Is(err, enrollment.ErrLauncherExpired) {
		t.Fatalf("expired consume error = %v, want expired", err)
	}
}

func TestValidateRejectsStableSingleUseOrLongLivedLauncher(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*enrollment.Reference){
		"stable launcher":     func(reference *enrollment.Reference) { reference.Launcher.Stable = true },
		"reusable launcher":   func(reference *enrollment.Reference) { reference.Launcher.SingleUse = false },
		"expiring gateway":    func(reference *enrollment.Reference) { at := now.Add(time.Hour); reference.Gateway.ExpiresAt = &at },
		"single-use gateway":  func(reference *enrollment.Reference) { reference.Gateway.SingleUse = true },
		"long-lived launcher": func(reference *enrollment.Reference) { at := now.Add(time.Hour); reference.Launcher.ExpiresAt = &at },
	} {
		t.Run(name, func(t *testing.T) {
			reference, err := enrollment.Plan("smallworlds.internal", now)
			if err != nil {
				t.Fatal(err)
			}
			mutate(&reference)
			if err := reference.Validate(); err == nil {
				t.Fatal("invalid enrollment reference accepted")
			}
		})
	}
}

func TestGenerateCredentialSecretIsRandom(t *testing.T) {
	first, err := enrollment.GenerateCredentialSecret()
	if err != nil || first == "" {
		t.Fatalf("secret = %q, err = %v", first, err)
	}
	second, _ := enrollment.GenerateCredentialSecret()
	if first == second {
		t.Fatal("credential secret is not random")
	}
}
