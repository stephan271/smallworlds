//go:build !linux

package nodeinspect

import "fmt"

func InspectSameHost(string, string) (Report, error) {
	return Report{}, fmt.Errorf("same-host inspection is supported only on Linux")
}
