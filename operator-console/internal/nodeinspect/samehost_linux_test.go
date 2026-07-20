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
