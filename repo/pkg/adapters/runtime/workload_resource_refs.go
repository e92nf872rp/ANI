package runtime

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

var workloadResourceKindPriority = map[string]int{
	"VirtualMachine": 10,
	"Deployment":     20,
	"Job":            30,
	"Service":        40,
	"NetworkPolicy":  50,
	"Secret":         90,
}

func primaryWorkloadResourceRef(refs []string) (string, error) {
	if len(refs) == 0 {
		return "", fmt.Errorf("%w: resource refs are required", ports.ErrInvalid)
	}
	best := refs[0]
	bestPriority := workloadResourceKindPriority[kindFromProviderResourceRef(best)]
	if bestPriority == 0 {
		bestPriority = 60
	}
	for _, ref := range refs[1:] {
		priority := workloadResourceKindPriority[kindFromProviderResourceRef(ref)]
		if priority == 0 {
			priority = 60
		}
		if priority < bestPriority {
			best = ref
			bestPriority = priority
		}
	}
	return best, nil
}

func providerResourceRefsForLifecycleDelete(refs []string) []string {
	if len(refs) == 0 {
		return nil
	}
	ordered := append([]string(nil), refs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left := workloadResourceKindPriority[kindFromProviderResourceRef(ordered[i])]
		right := workloadResourceKindPriority[kindFromProviderResourceRef(ordered[j])]
		if left == 0 {
			left = 60
		}
		if right == 0 {
			right = 60
		}
		return left < right
	})
	return ordered
}

func kindFromProviderResourceRef(ref string) string {
	parts := strings.Split(ref, "/")
	if len(parts) != 3 {
		return ""
	}
	return parts[1]
}
