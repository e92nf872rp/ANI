package runtime

import (
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type workloadIdentityScanRow struct {
	expiresAt string
	revokedAt string
}

func (r workloadIdentityScanRow) Scan(dest ...any) error {
	*dest[0].(*string) = "key-a"
	*dest[1].(*string) = "tenant-a"
	*dest[2].(*string) = "instance-a"
	*dest[3].(*string) = "ani_wi_tenant"
	*dest[4].(*[]string) = []string{"scope:instances:read"}
	*dest[5].(*bool) = false
	*dest[6].(*time.Time) = time.Date(2026, 7, 30, 7, 0, 0, 0, time.UTC)
	*dest[7].(*string) = r.expiresAt
	*dest[8].(*string) = r.revokedAt
	return nil
}

func TestScanWorkloadIdentityBindingAcceptsPostgresTimestampText(t *testing.T) {
	var binding ports.WorkloadIdentityBinding
	err := scanWorkloadIdentityBinding(workloadIdentityScanRow{
		expiresAt: "2026-07-30 08:42:26.634266+00",
		revokedAt: "2026-07-30 07:42:26.634266+00",
	}, &binding)
	if err != nil {
		t.Fatalf("scanWorkloadIdentityBinding() error = %v", err)
	}
	if binding.ExpiresAt.IsZero() || binding.RevokedAt.IsZero() {
		t.Fatalf("binding timestamps = expires %v revoked %v, want parsed values", binding.ExpiresAt, binding.RevokedAt)
	}
}
