//go:build linux

package nodeinspect_test

import (
	"runtime"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/nodeinspect"
)

func TestInspectSameHostIsReadOnlyAndReturnsLinuxEvidence(t *testing.T) {
	report, err := nodeinspect.InspectSameHost("not-this-host-profile", t.TempDir()+"/not-created-yet")
	if err != nil {
		t.Fatal(err)
	}
	if report.OperatingSystem != "linux" || report.Architecture != runtime.GOARCH || report.Capacity.CPUCores < 1 || report.Capacity.MemoryMi < 1 || report.Capacity.DiskGi < 0 {
		t.Fatalf("same-host report = %#v", report)
	}
}

// The ownership verdict has to follow the directory the operator chose. It used
// to be read from a fixed path, so removing the chosen directory could never
// clear the blocker and the journey stalled with nothing left to try.
func TestInspectSameHostJudgesTheChosenDataDirectory(t *testing.T) {
	present := t.TempDir()
	report, err := nodeinspect.InspectSameHost("not-this-host-profile", present)
	if err != nil {
		t.Fatal(err)
	}
	if report.Installation.SmallWorldsData != nodeinspect.Foreign {
		t.Fatalf("existing chosen directory ownership = %q, want foreign", report.Installation.SmallWorldsData)
	}
	report, err = nodeinspect.InspectSameHost("not-this-host-profile", present+"/removed")
	if err != nil {
		t.Fatal(err)
	}
	if report.Installation.SmallWorldsData != nodeinspect.Absent {
		t.Fatalf("removed chosen directory ownership = %q, want absent", report.Installation.SmallWorldsData)
	}
}
