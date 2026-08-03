package ports

import "errors"

var (
	ErrNotConfigured      = errors.New("capability adapter is not configured")
	ErrUnsupported        = errors.New("capability operation is unsupported by this adapter")
	ErrNotFound           = errors.New("capability resource not found")
	ErrConflict           = errors.New("capability resource conflict")
	ErrInvalid            = errors.New("capability request is invalid")
	ErrFailedPrecondition = errors.New("capability precondition failed")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrTenantNotFound     = errors.New("tenant not found")
	ErrUnavailable        = errors.New("capability dependency is unavailable")
)
