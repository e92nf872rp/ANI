package ports

import "errors"

var (
	ErrNotConfigured      = errors.New("capability adapter is not configured")
	ErrUnsupported        = errors.New("capability operation is unsupported by this adapter")
	ErrNotFound           = errors.New("capability resource not found")
	ErrConflict           = errors.New("capability resource conflict")
	ErrInvalid            = errors.New("capability request is invalid")
	ErrFailedPrecondition = errors.New("capability precondition failed")
	ErrPayloadTooLarge    = errors.New("capability payload is too large")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrTenantNotFound     = errors.New("tenant not found")
	ErrTenantPlanNotFound = errors.New("tenant plan not found")
	ErrUnavailable        = errors.New("capability dependency is unavailable")

	// Quota sentinel errors.
	ErrQuotaExceeded              = errors.New("quota exceeded")
	ErrQuotaResourceNotRegistered = errors.New("quota resource type not registered")
	ErrQuotaIdempotencyConflict   = errors.New("quota idempotency key conflict")
	ErrQuotaNotFound              = errors.New("quota not found")
	ErrQuotaAlreadyExists         = errors.New("quota already exists")
	ErrQuotaUpdateUncertain       = errors.New("quota update uncertain: transaction commit status unknown")
	ErrReservationNotFound        = errors.New("resource reservation not found")

	// Tenant user admin sentinel errors (users / user_roles / roles).
	ErrUserNotFound           = errors.New("user not found")
	ErrUserAlreadyTenantAdmin = errors.New("user already tenant admin")
	ErrTenantOwnerRoleLocked  = errors.New("tenant owner role locked")
	ErrLastTenantOwner        = errors.New("last tenant owner")
	ErrTransferTargetInvalid  = errors.New("transfer target invalid")
	ErrRoleChangeInvalid      = errors.New("role change invalid")
	ErrPasswordSameAsOld      = errors.New("password same as old")
	// Metadata transaction sentinel errors.
	ErrMetadataTenantTxBegin    = errors.New("metadata tenant tx begin")
	ErrMetadataTenantTxCommit   = errors.New("metadata tenant tx commit")
	ErrMetadataPlatformTxBegin  = errors.New("metadata platform tx begin")
	ErrMetadataPlatformTxCommit = errors.New("metadata platform tx commit")

	// Reservation sentinel errors (BOSS reservation management).
	ErrReservationExceedsQuota = errors.New("allocated_gpu_count exceeds total quota")
	ErrReservedInsufficient    = errors.New("reserved quota insufficient: allocated - used - reserved < request")
)
