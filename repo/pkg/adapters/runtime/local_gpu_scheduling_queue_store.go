package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/kubercloud/ani/pkg/ports"
)

// LocalGPUSchedulingQueueStore is an in-memory implementation of
// GPUSchedulingQueueStore for dev/local profile. It simulates Volcano Queue
// CRD behavior including tenant isolation, platform-default queue
// protection, and name conflict detection.
type LocalGPUSchedulingQueueStore struct {
	mu          sync.RWMutex
	queues      []localQueueRecord
	initialized bool
}

type localQueueRecord struct {
	id              string
	tenantID        string
	name            string
	weight          int
	reclaimable     bool
	workloadClass   ports.WorkloadClass
	projectID       string
	platformDefault bool
	createdAt       time.Time
	updatedAt       time.Time
}

// NewLocalGPUSchedulingQueueStore creates a local queue store with two
// platform-default queues pre-seeded: ani-inference and ani-training.
func NewLocalGPUSchedulingQueueStore() *LocalGPUSchedulingQueueStore {
	store := &LocalGPUSchedulingQueueStore{}
	store.seedDefaults()
	return store
}

func (s *LocalGPUSchedulingQueueStore) seedDefaults() {
	now := time.Now().UTC()
	s.queues = append(s.queues,
		localQueueRecord{
			id:              uuid.NewString(),
			tenantID:        "",
			name:            "ani-inference",
			weight:          10,
			reclaimable:     false,
			workloadClass:   ports.WorkloadClassInference,
			projectID:       "",
			platformDefault: true,
			createdAt:       now,
			updatedAt:       now,
		},
		localQueueRecord{
			id:              uuid.NewString(),
			tenantID:        "",
			name:            "ani-training",
			weight:          5,
			reclaimable:     true,
			workloadClass:   ports.WorkloadClassTraining,
			projectID:       "",
			platformDefault: true,
			createdAt:       now,
			updatedAt:       now,
		},
	)
	s.initialized = true
}

func (s *LocalGPUSchedulingQueueStore) List(_ context.Context, tenantID string) ([]ports.GPUSchedulingQueue, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]ports.GPUSchedulingQueue, 0, len(s.queues))
	for _, q := range s.queues {
		// Platform-default queues are visible to all tenants; custom queues
		// are filtered by tenant ID.
		if q.platformDefault || q.tenantID == tenantID {
			result = append(result, s.toPort(q))
		}
	}
	return result, nil
}

func (s *LocalGPUSchedulingQueueStore) Get(_ context.Context, tenantID, id string) (ports.GPUSchedulingQueue, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, q := range s.queues {
		if q.id == id && (q.platformDefault || q.tenantID == tenantID) {
			return s.toPort(q), nil
		}
	}
	return ports.GPUSchedulingQueue{}, ports.ErrQueueNotFound
}

func (s *LocalGPUSchedulingQueueStore) Create(_ context.Context, tenantID string, req ports.GPUSchedulingQueueCreateRequest) (ports.GPUSchedulingQueue, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return ports.GPUSchedulingQueue{}, fmt.Errorf("%w: name is required", ports.ErrInvalid)
	}
	if !isValidQueueName(name) {
		return ports.GPUSchedulingQueue{}, fmt.Errorf("%w: invalid queue name", ports.ErrInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Check name conflict: queue names are cluster-scoped in Volcano, so the
	// same name conflicts across all tenants.
	for _, q := range s.queues {
		if q.name == name {
			return ports.GPUSchedulingQueue{}, ports.ErrQueueNameConflict
		}
	}

	now := time.Now().UTC()
	record := localQueueRecord{
		id:              uuid.NewString(),
		tenantID:        tenantID,
		name:            name,
		weight:          req.Weight,
		reclaimable:     req.Reclaimable,
		workloadClass:   req.WorkloadClass,
		projectID:       req.ProjectID,
		platformDefault: false,
		createdAt:       now,
		updatedAt:       now,
	}
	s.queues = append(s.queues, record)
	return s.toPort(record), nil
}

func (s *LocalGPUSchedulingQueueStore) Update(_ context.Context, tenantID, id string, req ports.GPUSchedulingQueueUpdateRequest) (ports.GPUSchedulingQueue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, q := range s.queues {
		if q.id == id {
			if q.platformDefault {
				return ports.GPUSchedulingQueue{}, ports.ErrPlatformDefaultProtected
			}
			if q.tenantID != tenantID {
				return ports.GPUSchedulingQueue{}, ports.ErrQueueNotFound
			}
			if req.Weight != nil {
				s.queues[i].weight = *req.Weight
			}
			if req.Reclaimable != nil {
				s.queues[i].reclaimable = *req.Reclaimable
			}
			if req.WorkloadClass != nil {
				s.queues[i].workloadClass = *req.WorkloadClass
			}
			if req.ProjectID != nil {
				s.queues[i].projectID = *req.ProjectID
			}
			s.queues[i].updatedAt = time.Now().UTC()
			return s.toPort(s.queues[i]), nil
		}
	}
	return ports.GPUSchedulingQueue{}, ports.ErrQueueNotFound
}

func (s *LocalGPUSchedulingQueueStore) Delete(_ context.Context, tenantID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, q := range s.queues {
		if q.id == id {
			if q.platformDefault {
				return ports.ErrPlatformDefaultProtected
			}
			if q.tenantID != tenantID {
				return ports.ErrQueueNotFound
			}
			s.queues = append(s.queues[:i], s.queues[i+1:]...)
			return nil
		}
	}
	return ports.ErrQueueNotFound
}

func (s *LocalGPUSchedulingQueueStore) toPort(q localQueueRecord) ports.GPUSchedulingQueue {
	return ports.GPUSchedulingQueue{
		ID:                q.id,
		Name:              q.name,
		Weight:            q.weight,
		Reclaimable:       q.reclaimable,
		WorkloadClass:     q.workloadClass,
		ProjectID:         q.projectID,
		IsPlatformDefault: q.platformDefault,
		CreatedAt:         q.createdAt,
		UpdatedAt:         q.updatedAt,
	}
}

// isValidQueueName checks K8s resource name convention.
func isValidQueueName(name string) bool {
	if len(name) == 0 || len(name) > 63 {
		return false
	}
	for i, ch := range name {
		isAlnum := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')
		isDash := ch == '-'
		if !isAlnum && !isDash {
			return false
		}
		if isDash && (i == 0 || i == len(name)-1) {
			return false
		}
	}
	return true
}

var _ ports.GPUSchedulingQueueStore = (*LocalGPUSchedulingQueueStore)(nil)
