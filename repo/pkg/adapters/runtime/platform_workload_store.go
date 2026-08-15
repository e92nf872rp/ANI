package runtime

import (
	"sync"

	"github.com/kubercloud/ani/pkg/ports"
)

type platformWorkloadStore interface {
	get(tenantID, workloadID string) (kubernetesPlatformWorkload, error)
	getRaw(tenantID, workloadID string) (kubernetesPlatformWorkload, bool)
	put(item kubernetesPlatformWorkload) error
	remove(tenantID, workloadID, name, idempotencyKey string)
	intent(tenantID, idempotencyKey string) (platformWorkloadIntent, bool)
	putIntent(tenantID, idempotencyKey string, intent platformWorkloadIntent)
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
	item, ok := s.getRaw(tenantID, workloadID)
	if !ok || item.deleted {
		return kubernetesPlatformWorkload{}, ports.ErrNotFound
	}
	return item, nil
}

func (s *memoryPlatformWorkloadStore) getRaw(tenantID, workloadID string) (kubernetesPlatformWorkload, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[workloadID]
	if !ok || item.record.TenantID != tenantID {
		return kubernetesPlatformWorkload{}, false
	}
	return item, true
}

func (s *memoryPlatformWorkloadStore) put(item kubernetesPlatformWorkload) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[item.record.ID] = item
	if !item.deleted {
		s.names[nameKey(item.record.TenantID, item.record.Name)] = item.record.ID
	}
	return nil
}

func (s *memoryPlatformWorkloadStore) remove(tenantID, workloadID, name, idempotencyKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, workloadID)
	delete(s.names, nameKey(tenantID, name))
	delete(s.intents, intentKey(tenantID, idempotencyKey))
}

func (s *memoryPlatformWorkloadStore) intent(tenantID, idempotencyKey string) (platformWorkloadIntent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	intent, ok := s.intents[intentKey(tenantID, idempotencyKey)]
	return intent, ok
}

func (s *memoryPlatformWorkloadStore) putIntent(tenantID, idempotencyKey string, intent platformWorkloadIntent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.intents[intentKey(tenantID, idempotencyKey)] = intent
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
