package singleinstance_test

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/singleinstance"
)

func TestRepeatedLaunchRendezvousDoesNotCreateCompetingOwner(t *testing.T) {
	dataDir := t.TempDir()
	first, err := singleinstance.Acquire(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !first.IsOwner() {
		t.Fatal("first launcher did not acquire lifecycle ownership")
	}
	if err := first.Publish("http://127.0.0.1:43123/"); err != nil {
		t.Fatal(err)
	}

	second, err := singleinstance.Acquire(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if second.IsOwner() {
		t.Fatal("repeated launch acquired competing lifecycle ownership")
	}
	if second.ExistingURL() != "http://127.0.0.1:43123/" {
		t.Fatalf("existing URL = %q, want launcher rendezvous URL", second.ExistingURL())
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}

	third, err := singleinstance.Acquire(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if third.IsOwner() {
		t.Fatal("non-owner close removed the active launcher's ownership")
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	last, err := singleinstance.Acquire(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer last.Close()
	if !last.IsOwner() {
		t.Fatal("ownership was not released when the active launcher stopped")
	}
}

func TestCrashedLauncherDoesNotBlockNextLaunch(t *testing.T) {
	if os.Getenv("SMALLWORLDS_CRASH_HELPER") == "1" {
		lease, err := singleinstance.Acquire(os.Getenv("SMALLWORLDS_CRASH_DATA_DIR"))
		if err != nil {
			os.Exit(2)
		}
		if err := lease.Publish("http://127.0.0.1:43124/"); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}

	dataDir := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=TestCrashedLauncherDoesNotBlockNextLaunch")
	command.Env = append(os.Environ(),
		"SMALLWORLDS_CRASH_HELPER=1",
		"SMALLWORLDS_CRASH_DATA_DIR="+dataDir,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("crash helper failed: %v\n%s", err, output)
	}

	lease, err := singleinstance.Acquire(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if !lease.IsOwner() {
		t.Fatal("stale ownership from a crashed launcher blocked the next launch")
	}
}
