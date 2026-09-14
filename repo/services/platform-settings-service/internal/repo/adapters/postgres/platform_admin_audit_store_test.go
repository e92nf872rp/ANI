package postgres

import (
	"testing"

	"github.com/kubercloud/ani/services/platform-settings-service/internal/repo/ports"
)

func TestPostgresPlatformAdminAuditStore_ImplementsPort(t *testing.T) {
	t.Parallel()
	var _ ports.PlatformAdminAuditStore = (*PostgresPlatformAdminAuditStore)(nil)
}
