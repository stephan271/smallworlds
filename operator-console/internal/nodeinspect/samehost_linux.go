//go:build linux

package nodeinspect

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// InspectSameHost performs only local reads. It does not elevate privileges,
// create paths, install packages, or modify network/kernel configuration.
func InspectSameHost(profileID string) (Report, error) {
	memory, err := localMemoryMi()
	if err != nil {
		return Report{}, err
	}
	disk, err := localDiskGi()
	if err != nil {
		return Report{}, err
	}
	ports, err := listeningPorts()
	if err != nil {
		return Report{}, err
	}
	marker, _ := os.ReadFile("/etc/smallworlds/profile-id")
	owned := strings.TrimSpace(string(marker)) == profileID && profileID != ""
	installation := Installation{Kubernetes: localOwnership("/etc/rancher/k3s", owned), SmallWorldsData: localOwnership("/mnt/smallworlds-data", owned), Interrupted: exists("/etc/smallworlds/bootstrap-interrupted")}
	if owned {
		installation.ProfileID = profileID
	}
	privilege := "none"
	if os.Geteuid() == 0 {
		privilege = "root"
	} else if exists("/usr/bin/sudo") || exists("/bin/sudo") {
		privilege = "sudo"
	}
	return Report{OperatingSystem: "linux", Architecture: runtime.GOARCH, Systemd: exists("/run/systemd/system"), Capacity: Capacity{CPUCores: runtime.NumCPU(), MemoryMi: memory, DiskGi: disk}, Ports: ports, KernelReady: exists("/proc/sys/net/ipv4/ip_forward"), Privilege: privilege, Installation: installation}, nil
}

func localMemoryMi() (int, error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "MemAvailable:" {
			value, err := strconv.Atoi(fields[1])
			if err != nil {
				return 0, err
			}
			return value / 1024, nil
		}
	}
	return 0, fmt.Errorf("MemAvailable is absent from /proc/meminfo")
}

func localDiskGi() (int, error) {
	var filesystem syscall.Statfs_t
	if err := syscall.Statfs("/", &filesystem); err != nil {
		return 0, err
	}
	return int((filesystem.Bavail * uint64(filesystem.Bsize)) / (1024 * 1024 * 1024)), nil
}

func listeningPorts() ([]int, error) {
	ports := map[int]bool{}
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(file)
		first := true
		for scanner.Scan() {
			if first {
				first = false
				continue
			}
			fields := strings.Fields(scanner.Text())
			if len(fields) < 4 || fields[3] != "0A" {
				continue
			}
			address := strings.Split(fields[1], ":")
			if len(address) != 2 {
				continue
			}
			port, err := strconv.ParseInt(address[1], 16, 32)
			if err == nil && port > 0 && port <= 65535 {
				ports[int(port)] = true
			}
		}
		if err := scanner.Err(); err != nil {
			file.Close()
			return nil, err
		}
		file.Close()
	}
	result := make([]int, 0, len(ports))
	for port := range ports {
		result = append(result, port)
	}
	return result, nil
}

func localOwnership(path string, owned bool) Ownership {
	if !exists(path) {
		return Absent
	}
	if owned {
		return ProfileOwned
	}
	return Foreign
}

func exists(path string) bool {
	_, err := os.Stat(filepath.Clean(path))
	return err == nil
}
