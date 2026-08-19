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
	ErrUnavailable        = errors.New("capability dependency is unavailable")

	// Quota sentinel errors.
	ErrQuotaExceeded              = errors.New("quota exceeded")
	ErrQuotaResourceNotRegistered = errors.New("quota resource type not registered")
	ErrQuotaIdempotencyConflict   = errors.New("quota idempotency key conflict")
	ErrQuotaNotFound              = errors.New("quota not found")
	ErrQuotaAlreadyExists         = errors.New("quota already exists")
	ErrQuotaUpdateUncertain       = errors.New("quota update uncertain: transaction commit status unknown")
	ErrReservationNotFound        = errors.New("resource reservation not found")

	// Metadata transaction sentinel errors.
	ErrMetadataTenantTxBegin    = errors.New("metadata tenant tx begin")
	ErrMetadataTenantTxCommit   = errors.New("metadata tenant tx commit")
	ErrMetadataPlatformTxBegin  = errors.New("metadata platform tx begin")
	ErrMetadataPlatformTxCommit = errors.New("metadata platform tx commit")
)
