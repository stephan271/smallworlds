package nodeinspect_test

import (
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/nodeinspect"
)

func TestParseRemoteReportTreatsUnmarkedInstallationsAsForeignWithoutLeakingOutput(t *testing.T) {
	report, err := nodeinspect.ParseRemoteReport("os=Linux\narch=x86_64\nmachine_id=machine-a\nsystemd=1\ncpu=4\nmemory_mi=4096\ndisk_gi=100\nports=22,6443,\nkernel_ready=1\nprivilege=sudo\nkubernetes=present\ndata=present\nprofile_marker=unknown-profile\ninterrupted=1\nnoise=ssh-secret\n", "profile-a")
	if err != nil {
		t.Fatal(err)
	}
	if report.NodeIdentity != nodeinspect.HashNodeIdentity("machine-a") || report.Installation.Kubernetes != nodeinspect.Foreign || report.Installation.SmallWorldsData != nodeinspect.Foreign || len(report.Ports) != 2 || report.OperatingSystem != "linux" {
		t.Fatalf("report = %#v", report)
	}
	assessment := nodeinspect.Assess(report, nodeinspect.Requirements{ProfileID: "profile-a", MemoryMi: 512, DiskGi: 5})
	if assessment.Ready || !assessment.HasBlocker("installation.kubernetes.foreign") {
		t.Fatalf("assessment = %#v", assessment)
	}
}

func TestParseRemoteReportRecognizesOnlyTheSelectedProfileMarker(t *testing.T) {
	report, err := nodeinspect.ParseRemoteReport("os=linux\narch=amd64\nmachine_id=machine-a\nsystemd=1\ncpu=4\nmemory_mi=4096\ndisk_gi=100\nports=\nkernel_ready=1\nprivilege=root\nkubernetes=present\ndata=present\nprofile_marker=profile-a\ninterrupted=1\nbootstrap_run_id=run-7\nk3s_ready=1\nargocd_ready=1\noverlay_applied=1\nbootstrap_complete=1\n", "profile-a")
	if err != nil {
		t.Fatal(err)
	}
	assessment := nodeinspect.Assess(report, nodeinspect.Requirements{ProfileID: "profile-a", MemoryMi: 512, DiskGi: 5})
	if !assessment.Ready || !assessment.Resumable {
		t.Fatalf("assessment = %#v", assessment)
	}
	if report.Installation.BootstrapRunID != "run-7" || !report.Installation.K3SReady || !report.Installation.ArgoCDReady || !report.Installation.OverlayApplied || !report.Installation.Complete {
		t.Fatalf("bootstrap markers = %#v", report.Installation)
	}
}
