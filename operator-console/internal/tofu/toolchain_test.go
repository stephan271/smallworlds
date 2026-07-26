package tofu

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/bootstrapassets"
)

// stubSource stands in for the verified asset manager. It records the release
// it was asked for, so the pinned-version binding stays observable.
type stubSource struct {
	statuses         []bootstrapassets.Status
	err              error
	requestedRelease string
	acquireCalls     int
}

func (source *stubSource) Requirements(ctx context.Context, release string) ([]bootstrapassets.Status, error) {
	source.requestedRelease = release
	return source.statuses, source.err
}

func (source *stubSource) Acquire(_ context.Context, release string) ([]bootstrapassets.Status, error) {
	source.requestedRelease, source.acquireCalls = release, source.acquireCalls+1
	return source.statuses, source.err
}

func readyStatuses() []bootstrapassets.Status {
	statuses := make([]bootstrapassets.Status, 0, len(ArtifactIDs()))
	for _, id := range ArtifactIDs() {
		statuses = append(statuses, bootstrapassets.Status{ID: id, Release: Release(), State: bootstrapassets.StateReady, SHA256: strings.Repeat("a", 64)})
	}
	return statuses
}

func TestReleaseBindsThePinnedVersions(t *testing.T) {
	release := Release()
	if !strings.Contains(release, OpenTofuVersion) || !strings.Contains(release, HcloudProviderVersion) {
		t.Fatalf("release %q does not name the pinned versions", release)
	}
	source := &stubSource{statuses: readyStatuses()}
	if _, err := Inspect(t.Context(), source); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if source.requestedRelease != release {
		t.Fatalf("resolved release %q, want %q", source.requestedRelease, release)
	}
}

func TestToolchainReadyOnlyWhenEveryArtifactIsVerified(t *testing.T) {
	ready, err := Acquire(context.Background(), &stubSource{statuses: readyStatuses()})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if !ready.Ready || ready.ReasonKey != "toolchain-verified" {
		t.Fatalf("toolchain %+v", ready)
	}
	partial := readyStatuses()
	partial[0].State = bootstrapassets.StatePartial
	interrupted, err := Acquire(context.Background(), &stubSource{statuses: partial})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if interrupted.Ready || interrupted.ReasonKey != "toolchain-not-yet-acquired" {
		t.Fatalf("partial toolchain reported ready: %+v", interrupted)
	}
	missingProvider, err := Inspect(t.Context(), &stubSource{statuses: readyStatuses()[:1]})
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if missingProvider.Ready {
		t.Fatal("a missing provider plugin must not count as a ready toolchain")
	}
}

func TestUnpublishedToolchainRefusesInsteadOfFallingBack(t *testing.T) {
	toolchain, err := Acquire(context.Background(), &stubSource{err: bootstrapassets.ErrUnknownRelease})
	if !errors.Is(err, ErrToolchainUnavailable) {
		t.Fatalf("acquire returned %v, want ErrToolchainUnavailable", err)
	}
	if toolchain.Ready || toolchain.ReasonKey != "toolchain-artifacts-unavailable" {
		t.Fatalf("toolchain %+v", toolchain)
	}
	// An integrity failure is a different answer from "not published".
	integrity := &stubSource{err: bootstrapassets.ErrIntegrity}
	if _, err := Acquire(context.Background(), integrity); !errors.Is(err, bootstrapassets.ErrIntegrity) || errors.Is(err, ErrToolchainUnavailable) {
		t.Fatalf("integrity failure surfaced as %v", err)
	}
}
