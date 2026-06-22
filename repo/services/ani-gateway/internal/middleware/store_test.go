package middleware

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestGatewayStoreSetGetTTL(t *testing.T) {
	ctx := context.Background()
	store := newMemoryGatewayStoreForTest()

	if err := store.Set(ctx, "key-a", []byte("value-a"), 20*time.Millisecond); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	got, err := store.Get(ctx, "key-a")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if string(got) != "value-a" {
		t.Fatalf("Get() = %q, want value-a", string(got))
	}

	time.Sleep(40 * time.Millisecond)
	if _, err := store.Get(ctx, "key-a"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("Get() after TTL error = %v, want ErrNotFound", err)
	}
}

func TestGatewayStoreSetNXOnlyStoresFirstValue(t *testing.T) {
	ctx := context.Background()
	store := newMemoryGatewayStoreForTest()

	ok, err := store.SetNX(ctx, "processing", []byte("first"), time.Minute)
	if err != nil {
		t.Fatalf("SetNX(first) error = %v", err)
	}
	if !ok {
		t.Fatalf("SetNX(first) = false, want true")
	}
	ok, err = store.SetNX(ctx, "processing", []byte("second"), time.Minute)
	if err != nil {
		t.Fatalf("SetNX(second) error = %v", err)
	}
	if ok {
		t.Fatalf("SetNX(second) = true, want false")
	}
	got, err := store.Get(ctx, "processing")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if string(got) != "first" {
		t.Fatalf("Get() = %q, want first", string(got))
	}
}

type memoryGatewayStoreForTest struct {
	mu      sync.Mutex
	entries map[string]memoryGatewayStoreEntryForTest
}

type memoryGatewayStoreEntryForTest struct {
	value     []byte
	expiresAt time.Time
}

var _ GatewayStore = (*memoryGatewayStoreForTest)(nil)

func newMemoryGatewayStoreForTest() *memoryGatewayStoreForTest {
	return &memoryGatewayStoreForTest{entries: map[string]memoryGatewayStoreEntryForTest{}}
}

func (s *memoryGatewayStoreForTest) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = memoryGatewayStoreEntryForTest{value: append([]byte(nil), value...), expiresAt: expiresAtForTest(ttl)}
	return nil
}

func (s *memoryGatewayStoreForTest) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	if !ok || entry.expired() {
		delete(s.entries, key)
		return nil, ports.ErrNotFound
	}
	return append([]byte(nil), entry.value...), nil
}

func (s *memoryGatewayStoreForTest) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, key)
	return nil
}

func (s *memoryGatewayStoreForTest) Increment(_ context.Context, key string, ttl time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	if !ok || entry.expired() {
		s.entries[key] = memoryGatewayStoreEntryForTest{value: []byte("1"), expiresAt: expiresAtForTest(ttl)}
		return 1, nil
	}
	s.entries[key] = memoryGatewayStoreEntryForTest{value: []byte("2"), expiresAt: expiresAtForTest(ttl)}
	return 2, nil
}

func (s *memoryGatewayStoreForTest) Exists(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	if !ok || entry.expired() {
		delete(s.entries, key)
		return false, nil
	}
	return true, nil
}

func (s *memoryGatewayStoreForTest) SetNX(_ context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	if ok && !entry.expired() {
		return false, nil
	}
	s.entries[key] = memoryGatewayStoreEntryForTest{value: append([]byte(nil), value...), expiresAt: expiresAtForTest(ttl)}
	return true, nil
}

func expiresAtForTest(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(ttl)
}

func (e memoryGatewayStoreEntryForTest) expired() bool {
	return !e.expiresAt.IsZero() && time.Now().After(e.expiresAt)
}
