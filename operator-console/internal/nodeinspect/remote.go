package nodeinspect

import (
	"bufio"
	"context"
	"fmt"
	"strconv"
	"strings"
)

// inspectionCommand is fixed in the Launcher binary. It contains no browser
// input and emits a deliberately small key/value contract rather than shell
// output or logs. Profile ownership is evaluated by Go after parsing markers.
const inspectionCommand = `LANG=C
echo os="$(uname -s 2>/dev/null || true)"
echo arch="$(uname -m 2>/dev/null || true)"
echo systemd="$(test -d /run/systemd/system && echo 1 || echo 0)"
echo cpu="$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 0)"
echo memory_mi="$(awk '/MemAvailable:/ {print int($2/1024)}' /proc/meminfo 2>/dev/null || echo 0)"
echo disk_gi="$(df -Pk / 2>/dev/null | awk 'NR==2 {print int($4/1048576)}' || echo 0)"
echo ports="$(ss -H -ltn 2>/dev/null | awk '{sub(/^.*:/,"",$4); print $4}' | tr '\n' ',' || true)"
echo kernel_ready="$(test -e /proc/sys/net/ipv4/ip_forward && echo 1 || echo 0)"
if test "$(id -u 2>/dev/null)" = 0; then echo privilege=root; elif sudo -n true >/dev/null 2>&1; then echo privilege=sudo; else echo privilege=none; fi
echo kubernetes="$(test -d /etc/rancher/k3s && echo present || echo absent)"
echo data="$(test -d /mnt/smallworlds-data && echo present || echo absent)"
echo profile_marker="$(cat /etc/smallworlds/profile-id 2>/dev/null | head -n 1 || true)"
echo interrupted="$(test -f /etc/smallworlds/bootstrap-interrupted && echo 1 || echo 0)"`

func InspectRemote(ctx context.Context, target Target, credentials Credentials, fingerprint, profileID string, requirements Requirements) (Report, Assessment, error) {
	client, err := DialTrusted(ctx, target, credentials, fingerprint)
	if err != nil {
		return Report{}, Assessment{}, err
	}
	defer client.Close()
	if err := ValidateSudoCredential(client, credentials.SudoPassword); err != nil {
		return Report{}, Assessment{}, err
	}
	session, err := client.NewSession()
	if err != nil {
		return Report{}, Assessment{}, fmt.Errorf("start fixed SSH inspection session: %w", err)
	}
	defer session.Close()
	output, err := session.Output(inspectionCommand)
	if err != nil {
		return Report{}, Assessment{}, fmt.Errorf("run fixed SSH inspection: %w", err)
	}
	report, err := ParseRemoteReport(string(output), profileID)
	if err != nil {
		return Report{}, Assessment{}, err
	}
	return report, Assess(report, requirements), nil
}

func ParseRemoteReport(output, profileID string) (Report, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "=", 2)
		if len(parts) != 2 {
			continue
		}
		if _, known := map[string]bool{"os": true, "arch": true, "systemd": true, "cpu": true, "memory_mi": true, "disk_gi": true, "ports": true, "kernel_ready": true, "privilege": true, "kubernetes": true, "data": true, "profile_marker": true, "interrupted": true}[parts[0]]; known {
			values[parts[0]] = strings.TrimSpace(parts[1])
		}
	}
	if err := scanner.Err(); err != nil {
		return Report{}, err
	}
	parseInt := func(key string) (int, error) {
		value, err := strconv.Atoi(values[key])
		if err != nil || value < 0 {
			return 0, fmt.Errorf("invalid remote inspection %s", key)
		}
		return value, nil
	}
	cpu, err := parseInt("cpu")
	if err != nil {
		return Report{}, err
	}
	memory, err := parseInt("memory_mi")
	if err != nil {
		return Report{}, err
	}
	disk, err := parseInt("disk_gi")
	if err != nil {
		return Report{}, err
	}
	ports := []int{}
	for _, value := range strings.Split(strings.TrimSuffix(values["ports"], ","), ",") {
		if value == "" {
			continue
		}
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			continue
		}
		ports = append(ports, port)
	}
	marker := values["profile_marker"]
	owned := marker != "" && marker == profileID
	installation := Installation{Kubernetes: ownership(values["kubernetes"], owned), SmallWorldsData: ownership(values["data"], owned), Interrupted: values["interrupted"] == "1"}
	if owned {
		installation.ProfileID = marker
	}
	return Report{OperatingSystem: strings.ToLower(values["os"]), Architecture: values["arch"], Systemd: values["systemd"] == "1", Capacity: Capacity{CPUCores: cpu, MemoryMi: memory, DiskGi: disk}, Ports: ports, KernelReady: values["kernel_ready"] == "1", Privilege: values["privilege"], Installation: installation}, nil
}

func ownership(value string, owned bool) Ownership {
	if value != "present" {
		return Absent
	}
	if owned {
		return ProfileOwned
	}
	return Foreign
}
