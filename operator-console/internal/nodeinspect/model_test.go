package nodeinspect_test

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/nodeinspect"
	"golang.org/x/crypto/ssh"
)

func TestTargetValidationRejectsUnsafeRemoteAndUnsupportedSameHost(t *testing.T) {
	for _, target := range []nodeinspect.Target{
		{Kind: nodeinspect.RemoteTarget, Host: "example.com", Port: 22, Username: "operator"},
		{Kind: nodeinspect.SameHostTarget},
	} {
		if err := target.Validate("linux"); err != nil {
			t.Fatalf("valid target %#v: %v", target, err)
		}
	}
	for _, target := range []nodeinspect.Target{
		{Kind: nodeinspect.RemoteTarget, Host: "", Port: 22, Username: "operator"},
		{Kind: nodeinspect.RemoteTarget, Host: "node;rm", Port: 22, Username: "operator"},
		{Kind: nodeinspect.RemoteTarget, Host: "example.com", Port: 0, Username: "operator"},
		{Kind: nodeinspect.SameHostTarget},
	} {
		platform := "linux"
		if target.Kind == nodeinspect.SameHostTarget {
			platform = "windows"
		}
		if err := target.Validate(platform); err == nil {
			t.Fatalf("unsafe target accepted: %#v", target)
		}
	}
}

func TestHostKeyFingerprintUsesSSHCanonicalSHA256Format(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := nodeinspect.HostKeyFingerprint(publicKey)
	if len(fingerprint) < len("SHA256:x") || fingerprint[:7] != "SHA256:" {
		t.Fatalf("fingerprint = %q", fingerprint)
	}
}

func TestAssessmentExplainsInsufficientCapacityAndUnsafeExistingInstallations(t *testing.T) {
	report := nodeinspect.Report{
		OperatingSystem: "linux", Architecture: "amd64", Systemd: true,
		Capacity:     nodeinspect.Capacity{CPUCores: 2, MemoryMi: 1024, DiskGi: 10},
		Ports:        []int{80, 6443},
		Installation: nodeinspect.Installation{Kubernetes: nodeinspect.Foreign, SmallWorldsData: nodeinspect.Unknown},
	}
	assessment := nodeinspect.Assess(report, nodeinspect.Requirements{MemoryMi: 2048, DiskGi: 30, RequiredPorts: []int{80, 443, 6443}})
	if assessment.Ready || len(assessment.Blockers) < 4 {
		t.Fatalf("assessment = %#v", assessment)
	}
	for _, code := range []string{"capacity.memory.insufficient", "capacity.disk.insufficient", "port.80.occupied", "installation.kubernetes.foreign", "installation.data.unknown"} {
		if !assessment.HasBlocker(code) {
			t.Errorf("assessment lacks %s: %#v", code, assessment.Blockers)
		}
	}
}

func TestAssessmentOffersOnlyRecognizedSameProfileInstallationAsResumable(t *testing.T) {
	report := nodeinspect.Report{OperatingSystem: "linux", Architecture: "amd64", Systemd: true, KernelReady: true, Privilege: "sudo", Capacity: nodeinspect.Capacity{CPUCores: 4, MemoryMi: 4096, DiskGi: 80}, Installation: nodeinspect.Installation{Kubernetes: nodeinspect.ProfileOwned, SmallWorldsData: nodeinspect.ProfileOwned, ProfileID: "profile-a", Interrupted: true}}
	assessment := nodeinspect.Assess(report, nodeinspect.Requirements{MemoryMi: 512, DiskGi: 10, ProfileID: "profile-a"})
	if !assessment.Resumable || !assessment.Ready {
		t.Fatalf("same-profile interruption should be resumable: %#v", assessment)
	}
	report.Installation.ProfileID = "another-profile"
	assessment = nodeinspect.Assess(report, nodeinspect.Requirements{MemoryMi: 512, DiskGi: 10, ProfileID: "profile-a"})
	if assessment.Resumable || assessment.Ready || !assessment.HasBlocker("installation.profile.mismatch") {
		t.Fatalf("other profile was accepted: %#v", assessment)
	}
}
