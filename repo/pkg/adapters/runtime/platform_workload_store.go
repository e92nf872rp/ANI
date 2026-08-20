package runtime

import (
	"errors"
	"fmt"
	"sync"

	"github.com/kubercloud/ani/pkg/ports"
)

var errPlatformWorkloadIntentReplay = errors.New("platform workload intent already reserved")

func platformWorkloadIntentConflict() error {
	return fmt.Errorf("%w: idempotency key reused for a different platform workload", ports.ErrConflict)
}

type platformWorkloadStore interface {
	get(tenantID, workloadID string) (kubernetesPlatformWorkload, error)
	getRaw(tenantID, workloadID string) (kubernetesPlatformWorkload, bool, error)
	put(item kubernetesPlatformWorkload) error
	putWithIntent(item kubernetesPlatformWorkload, idempotencyKey string, intent platformWorkloadIntent) error
	remove(tenantID, workloadID, name, idempotencyKey string)
	intent(tenantID, idempotencyKey string) (platformWorkloadIntent, bool, error)
	putIntent(tenantID, idempotencyKey string, intent platformWorkloadIntent) error
	nameID(tenantID, name string) (string, bool)
	deleteName(tenantID, name string)
}

type memoryPlatformWorkloadStore struct {
	mu      sync.Mutex
	items   map[string]kubernetesPlatformWorkload
	intents map[string]platformWorkloadIntent
	names   map[string]string
}

func newMemoryPlatformWorkloadStore() *memoryPlatformWorkloadStore {
	return &memoryPlatformWorkloadStore{
		items:   map[string]kubernetesPlatformWorkload{},
		intents: map[string]platformWorkloadIntent{},
		names:   map[string]string{},
	}
}

func (s *memoryPlatformWorkloadStore) get(tenantID, workloadID string) (kubernetesPlatformWorkload, error) {
	item, ok, err := s.getRaw(tenantID, workloadID)
	if err != nil {
		return kubernetesPlatformWorkload{}, err
	}
	if !ok || item.deleted {
		return kubernetesPlatformWorkload{}, ports.ErrNotFound
	}
	return item, nil
}

func (s *memoryPlatformWorkloadStore) getRaw(tenantID, workloadID string) (kubernetesPlatformWorkload, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[workloadID]
	if !ok || item.record.TenantID != tenantID {
		return kubernetesPlatformWorkload{}, false, nil
	}
	return item, true, nil
}

func (s *memoryPlatformWorkloadStore) put(item kubernetesPlatformWorkload) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putLocked(item)
	return nil
}

func (s *memoryPlatformWorkloadStore) putLocked(item kubernetesPlatformWorkload) {
	s.items[item.record.ID] = item
	if !item.deleted {
		s.names[nameKey(item.record.TenantID, item.record.Name)] = item.record.ID
	}
}

func (s *memoryPlatformWorkloadStore) putWithIntent(item kubernetesPlatformWorkload, idempotencyKey string, intent platformWorkloadIntent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := intentKey(item.record.TenantID, idempotencyKey)
	if existing, ok := s.intents[key]; ok {
		if existing.fingerprint != intent.fingerprint {
			return platformWorkloadIntentConflict()
		}
		return errPlatformWorkloadIntentReplay
	}
	s.putLocked(item)
	return s.putIntentLocked(item.record.TenantID, idempotencyKey, intent)
}

func (s *memoryPlatformWorkloadStore) remove(tenantID, workloadID, name, idempotencyKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, workloadID)
	delete(s.names, nameKey(tenantID, name))
	delete(s.intents, intentKey(tenantID, idempotencyKey))
}

func (s *memoryPlatformWorkloadStore) intent(tenantID, idempotencyKey string) (platformWorkloadIntent, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	intent, ok := s.intents[intentKey(tenantID, idempotencyKey)]
	return intent, ok, nil
}

func (s *memoryPlatformWorkloadStore) putIntent(tenantID, idempotencyKey string, intent platformWorkloadIntent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.putIntentLocked(tenantID, idempotencyKey, intent)
}

func (s *memoryPlatformWorkloadStore) putIntentLocked(tenantID, idempotencyKey string, intent platformWorkloadIntent) error {
	key := intentKey(tenantID, idempotencyKey)
	if intent.status == "" {
		intent.status = platformWorkloadIntentPending
	}
	if existing, ok := s.intents[key]; ok {
		if existing.fingerprint != intent.fingerprint {
			return platformWorkloadIntentConflict()
		}
		existing.status = intent.status
		s.intents[key] = existing
		return nil
	}
	s.intents[key] = intent
	return nil
}

func (s *memoryPlatformWorkloadStore) nameID(tenantID, name string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.names[nameKey(tenantID, name)]
	return id, ok
}

func (s *memoryPlatformWorkloadStore) deleteName(tenantID, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.names, nameKey(tenantID, name))
}
