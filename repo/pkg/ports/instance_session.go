package ports

import (
	"context"
	"errors"
	"time"
)

var (
	ErrSessionCapacity = errors.New("instance session capacity exhausted")
	ErrSessionInternal = errors.New("instance session dependency failed")
)

type InstanceExecSessionCreateRequest struct {
	RequestID      string
	IdempotencyKey string
	TenantID       string
	SubjectID      string
	InstanceID     string
	WorkloadName   string
	WorkloadKind   WorkloadKind
	Container      string
	Command        []string
	TTY            bool
	Rows           int
	Cols           int
}

type InstanceExecSessionRecord struct {
	ID         string
	InstanceID string
	WSURL      string
	Token      string
	ExpiresAt  time.Time
	DevProfile DevProfileInfo
}

type InstanceConsoleSessionCreateRequest struct {
	RequestID      string
	IdempotencyKey string
	TenantID       string
	SubjectID      string
	InstanceID     string
	WorkloadName   string
	WorkloadKind   WorkloadKind
	Protocol       string
}

type InstanceConsoleSessionRecord struct {
	SessionID  string
	InstanceID string
	Protocol   string
	ConnectURL string
	URL        string
	ExpiresAt  time.Time
	DevProfile DevProfileInfo
}

// InstanceSessionIssuer signs short-lived realtime sessions without exposing
// transport-specific gRPC or provider runtime types to product handlers.
type InstanceSessionIssuer interface {
	CreateExecSession(context.Context, InstanceExecSessionCreateRequest) (InstanceExecSessionRecord, error)
	CreateConsoleSession(context.Context, InstanceConsoleSessionCreateRequest) (InstanceConsoleSessionRecord, error)
}
