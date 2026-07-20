// Package nodeinspect contains the safe, read-only Local Cluster Node
// inspection model. It deliberately describes fixed observations rather than
// accepting browser-provided commands.
package nodeinspect

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/crypto/ssh"
)

type TargetKind string

const (
	RemoteTarget   TargetKind = "remote"
	SameHostTarget TargetKind = "same-host"
)

type Target struct {
	Kind     TargetKind `json:"kind"`
	Host     string     `json:"host,omitempty"`
	Port     int        `json:"port,omitempty"`
	Username string     `json:"username,omitempty"`
}

var safeHost = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$`)
var safeUsername = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,31}$`)

func (target Target) Validate(platform string) error {
	switch target.Kind {
	case SameHostTarget:
		if platform != "linux" {
			return fmt.Errorf("same-host inspection is supported only on Linux")
		}
		if target.Host != "" || target.Username != "" || target.Port != 0 {
			return fmt.Errorf("same-host target cannot carry remote connection data")
		}
		return nil
	case RemoteTarget:
		if !safeHost.MatchString(target.Host) || strings.Contains(target.Host, "..") || net.ParseIP(target.Host) == nil && strings.HasPrefix(target.Host, "-") {
			return fmt.Errorf("remote host is invalid")
		}
		if target.Port < 1 || target.Port > 65535 || !safeUsername.MatchString(target.Username) {
			return fmt.Errorf("remote connection details are invalid")
		}
		return nil
	default:
		return fmt.Errorf("target kind is invalid")
	}
}

type Ownership string

const (
	Absent       Ownership = "absent"
	ProfileOwned Ownership = "profile-owned"
	Foreign      Ownership = "foreign"
	Unknown      Ownership = "unknown"
)

type Capacity struct {
	CPUCores int `json:"cpuCores"`
	MemoryMi int `json:"memoryMi"`
	DiskGi   int `json:"diskGi"`
}

type Installation struct {
	Kubernetes      Ownership `json:"kubernetes"`
	SmallWorldsData Ownership `json:"smallworldsData"`
	ProfileID       string    `json:"profileId,omitempty"`
	Interrupted     bool      `json:"interrupted"`
	BootstrapRunID  string    `json:"bootstrapRunId,omitempty"`
	K3SReady        bool      `json:"k3sReady"`
	ArgoCDReady     bool      `json:"argoCdReady"`
	OverlayApplied  bool      `json:"overlayApplied"`
	Complete        bool      `json:"complete"`
}

type Report struct {
	OperatingSystem string       `json:"operatingSystem"`
	Architecture    string       `json:"architecture"`
	Systemd         bool         `json:"systemd"`
	Capacity        Capacity     `json:"capacity"`
	Ports           []int        `json:"ports"`
	KernelReady     bool         `json:"kernelReady"`
	Privilege       string       `json:"privilege"`
	Installation    Installation `json:"installation"`
}

type Requirements struct {
	ProfileID     string
	MemoryMi      int
	DiskGi        int
	RequiredPorts []int
}

type Blocker struct {
	Code string `json:"code"`
}

type Assessment struct {
	Ready     bool      `json:"ready"`
	Resumable bool      `json:"resumable"`
	Blockers  []Blocker `json:"blockers"`
}

func (assessment Assessment) HasBlocker(code string) bool {
	for _, blocker := range assessment.Blockers {
		if blocker.Code == code {
			return true
		}
	}
	return false
}

func Assess(report Report, requirements Requirements) Assessment {
	blockers := make([]Blocker, 0)
	if report.OperatingSystem != "linux" {
		blockers = append(blockers, Blocker{Code: "node.os.unsupported"})
	}
	if !report.Systemd {
		blockers = append(blockers, Blocker{Code: "node.systemd.missing"})
	}
	if !report.KernelReady {
		blockers = append(blockers, Blocker{Code: "node.kernel.unsupported"})
	}
	if report.Privilege != "root" && report.Privilege != "sudo" {
		blockers = append(blockers, Blocker{Code: "node.privilege.unavailable"})
	}
	if report.Capacity.MemoryMi < requirements.MemoryMi {
		blockers = append(blockers, Blocker{Code: "capacity.memory.insufficient"})
	}
	if report.Capacity.DiskGi < requirements.DiskGi {
		blockers = append(blockers, Blocker{Code: "capacity.disk.insufficient"})
	}
	occupied := make(map[int]bool, len(report.Ports))
	for _, port := range report.Ports {
		occupied[port] = true
	}
	for _, port := range requirements.RequiredPorts {
		if occupied[port] {
			blockers = append(blockers, Blocker{Code: fmt.Sprintf("port.%d.occupied", port)})
		}
	}
	installation := report.Installation
	resumable := installation.Interrupted && installation.Kubernetes == ProfileOwned && installation.SmallWorldsData == ProfileOwned && installation.ProfileID == requirements.ProfileID
	if installation.ProfileID != "" && installation.ProfileID != requirements.ProfileID && (installation.Kubernetes == ProfileOwned || installation.SmallWorldsData == ProfileOwned) {
		blockers = append(blockers, Blocker{Code: "installation.profile.mismatch"})
	} else {
		for _, entry := range []struct {
			kind Ownership
			code string
		}{{installation.Kubernetes, "installation.kubernetes"}, {installation.SmallWorldsData, "installation.data"}} {
			switch entry.kind {
			case Foreign:
				blockers = append(blockers, Blocker{Code: entry.code + ".foreign"})
			case Unknown:
				blockers = append(blockers, Blocker{Code: entry.code + ".unknown"})
			case ProfileOwned:
				if !resumable {
					blockers = append(blockers, Blocker{Code: entry.code + ".existing"})
				}
			}
		}
	}
	sort.Slice(blockers, func(left, right int) bool { return blockers[left].Code < blockers[right].Code })
	return Assessment{Ready: len(blockers) == 0, Resumable: resumable && len(blockers) == 0, Blockers: blockers}
}

// HostKeyFingerprint is the canonical SHA-256 presentation shown before the
// Operator grants trust. The raw key is retained only in protected launcher
// state, never in a browser-visible inspection result.
func HostKeyFingerprint(key ssh.PublicKey) string {
	return ssh.FingerprintSHA256(key)
}
