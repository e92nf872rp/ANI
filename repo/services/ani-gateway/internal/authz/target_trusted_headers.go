package authz

import (
	"fmt"
	"net/http"
	"strings"
)

// BuildTargetForwardHeaders strips every client-provided x-ani-* header before
// adding the fixed, non-authoritative-context allowlist from the target policy.
func BuildTargetForwardHeaders(client http.Header, trusted map[string]string) (http.Header, error) {
	forward := client.Clone()
	for key := range forward {
		if strings.HasPrefix(strings.ToLower(key), "x-ani-") {
			delete(forward, key)
		}
	}
	seen := make(map[string]struct{}, len(trusted))
	for key, value := range trusted {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if _, ok := generatedTargetTrustedContextHeaders[normalized]; !ok {
			return nil, fmt.Errorf("trusted context header %q is not registered", key)
		}
		if _, duplicate := seen[normalized]; duplicate {
			return nil, fmt.Errorf("trusted context header %q is duplicated after normalization", key)
		}
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("trusted context header %q is empty", key)
		}
		seen[normalized] = struct{}{}
		forward.Set(normalized, value)
	}
	return forward, nil
}
