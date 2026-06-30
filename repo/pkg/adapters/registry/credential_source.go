package registry

import (
	"github.com/kubercloud/ani/pkg/ports"
)

// AsPullSecretCredentialSource unwraps PersistingImageRegistry and returns a credential source when supported.
func AsPullSecretCredentialSource(registry ports.ImageRegistry) (ports.RegistryPullSecretCredentialSource, bool) {
	switch typed := registry.(type) {
	case ports.RegistryPullSecretCredentialSource:
		return typed, true
	case *PersistingImageRegistry:
		return AsPullSecretCredentialSource(typed.inner)
	default:
		return nil, false
	}
}
