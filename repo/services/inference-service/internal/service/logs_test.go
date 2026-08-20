package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
	"github.com/kubercloud/ani/services/inference-service/internal/runtime"
)

type logsStoreStub struct {
	resource domain.Service
	err      error
}

func (s *logsStoreStub) GetService(context.Context, uuid.UUID, uuid.UUID) (domain.Service, error) {
	if s.err != nil {
		return domain.Service{}, s.err
	}
	return s.resource, nil
}

type logsRuntimeStub struct {
	page  runtime.LogPage
	err   error
	query runtime.LogQuery
}

func (s *logsRuntimeStub) Logs(_ context.Context, query runtime.LogQuery) (runtime.LogPage, error) {
	s.query = query
	return s.page, s.err
}

func TestLogReaderMissingServiceIsNotFound(t *testing.T) {
	reader := NewLogReader(&logsStoreStub{err: repository.ErrNotFound}, &logsRuntimeStub{})
	_, err := reader.List(context.Background(), uuid.New(), uuid.New(), LogQuery{})
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestLogReaderDeletedServiceIsNotFound(t *testing.T) {
	deleted := time.Unix(9, 0).UTC()
	reader := NewLogReader(&logsStoreStub{resource: domain.Service{
		ID: uuid.New(), TenantID: uuid.New(), DeletedAt: &deleted, RuntimeRef: uuid.New(),
	}}, &logsRuntimeStub{page: runtime.LogPage{Items: []runtime.LogEntry{{Message: "should not leak"}}}})
	_, err := reader.List(context.Background(), uuid.New(), uuid.New(), LogQuery{})
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestLogReaderWithoutRuntimeRefReturnsEmpty(t *testing.T) {
	rt := &logsRuntimeStub{page: runtime.LogPage{Items: []runtime.LogEntry{{Message: "hidden"}}}}
	reader := NewLogReader(&logsStoreStub{resource: domain.Service{ID: uuid.New(), TenantID: uuid.New()}}, rt)
	page, err := reader.List(context.Background(), uuid.New(), uuid.New(), LogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 || rt.query.RuntimeRef != uuid.Nil {
		t.Fatalf("page=%+v query=%+v", page, rt.query)
	}
}

func TestLogReaderRedactsRuntimeAndSecrets(t *testing.T) {
	ref := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	resource := domain.Service{
		ID: uuid.New(), TenantID: uuid.New(), RuntimeRef: ref,
		RuntimeEndpoint: "http://inference-hidden.internal.svc:8000",
	}
	rt := &logsRuntimeStub{page: runtime.LogPage{Items: []runtime.LogEntry{
		{Timestamp: time.Unix(1, 0).UTC(), Level: "info", Message: "runtime accepted", Container: "serve", Stream: "stdout"},
		{Timestamp: time.Unix(2, 0).UTC(), Level: "warn", Message: "Authorization: Bearer secret-token"},
		{Timestamp: time.Unix(3, 0).UTC(), Level: "info", Message: "dial " + resource.RuntimeEndpoint},
	}}}
	reader := NewLogReader(&logsStoreStub{resource: resource}, rt)
	page, err := reader.List(context.Background(), resource.TenantID, resource.ID, LogQuery{Limit: 10, Level: "info"})
	if err != nil {
		t.Fatal(err)
	}
	if rt.query.RuntimeRef != ref || rt.query.Limit != 10 || rt.query.Level != "info" {
		t.Fatalf("query = %+v", rt.query)
	}
	if len(page.Items) != 3 {
		t.Fatalf("items = %+v", page.Items)
	}
	if page.Items[0].Message != "runtime accepted" || page.Items[0].Container != "serve" {
		t.Fatalf("first = %+v", page.Items[0])
	}
	if page.Items[1].Message != "[redacted]" || page.Items[2].Message != "[redacted]" {
		t.Fatalf("redaction = %+v", page.Items)
	}
}

func TestLogReaderRejectsInvalidLevel(t *testing.T) {
	reader := NewLogReader(&logsStoreStub{resource: domain.Service{ID: uuid.New()}}, &logsRuntimeStub{})
	_, err := reader.List(context.Background(), uuid.New(), uuid.New(), LogQuery{Level: "fatal"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v", err)
	}
}
