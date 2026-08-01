package runtime

import (
	"context"
	"encoding/base64"
	"fmt"
	pathpkg "path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// SandboxCodeRunner executes sandbox code against a concrete provider (optional).
// Local profile leaves this nil and returns accepted without stdout.
type SandboxCodeRunner func(ctx context.Context, request ports.SandboxCodeRunRequest, instance ports.SandboxInstanceStatus) (ports.SandboxCodeRunResult, error)

type LocalSandboxRuntime struct {
	now            func() time.Time
	sequence       atomic.Uint64
	mu             sync.RWMutex
	instances      map[string]ports.SandboxInstanceStatus
	tokens         map[string]ports.SandboxTokenResult
	ports          map[string]map[int]ports.SandboxPortResult
	portKeys       map[string]ports.SandboxPortResult
	closeKeys      map[string]ports.SandboxPortResult
	files          map[string]map[string]ports.SandboxFileResult
	fileKeys       map[string]ports.SandboxFileResult
	rmKeys         map[string]struct{}
	checkpoints    map[string]map[string]ports.SandboxCheckpointResult
	checkpointKeys map[string]ports.SandboxCheckpointResult
	restoreKeys    map[string]ports.SandboxCheckpointResult
	cloneKeys      map[string]ports.SandboxCheckpointResult
	codeRunKeys    map[string]ports.SandboxCodeRunResult
	codeRunner     SandboxCodeRunner
}

type SandboxRuntimeOption func(*LocalSandboxRuntime)

func WithSandboxRuntimeClock(now func() time.Time) SandboxRuntimeOption {
	return func(runtime *LocalSandboxRuntime) {
		if now != nil {
			runtime.now = now
		}
	}
}

func WithSandboxCodeRunner(runner SandboxCodeRunner) SandboxRuntimeOption {
	return func(runtime *LocalSandboxRuntime) {
		runtime.codeRunner = runner
	}
}

func NewLocalSandboxRuntime(options ...SandboxRuntimeOption) *LocalSandboxRuntime {
	runtime := &LocalSandboxRuntime{
		now:            time.Now,
		instances:      make(map[string]ports.SandboxInstanceStatus),
		tokens:         make(map[string]ports.SandboxTokenResult),
		ports:          make(map[string]map[int]ports.SandboxPortResult),
		portKeys:       make(map[string]ports.SandboxPortResult),
		closeKeys:      make(map[string]ports.SandboxPortResult),
		files:          make(map[string]map[string]ports.SandboxFileResult),
		fileKeys:       make(map[string]ports.SandboxFileResult),
		rmKeys:         make(map[string]struct{}),
		checkpoints:    make(map[string]map[string]ports.SandboxCheckpointResult),
		checkpointKeys: make(map[string]ports.SandboxCheckpointResult),
		restoreKeys:    make(map[string]ports.SandboxCheckpointResult),
		cloneKeys:      make(map[string]ports.SandboxCheckpointResult),
		codeRunKeys:    make(map[string]ports.SandboxCodeRunResult),
	}
	for _, option := range options {
		option(runtime)
	}
	return runtime
}

func (r *LocalSandboxRuntime) Create(_ context.Context, request ports.SandboxCreateRequest) (ports.SandboxInstanceStatus, error) {
	if strings.TrimSpace(request.TenantID) == "" {
		return ports.SandboxInstanceStatus{}, fmt.Errorf("%w: tenantID is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.Name) == "" {
		return ports.SandboxInstanceStatus{}, fmt.Errorf("%w: sandbox name is required", ports.ErrInvalid)
	}
	config := normalizeSandboxConfig(request.Config)
	if err := validateSandboxConfig(config); err != nil {
		return ports.SandboxInstanceStatus{}, err
	}
	state := ports.SandboxStatePending
	if request.AutoStart {
		state = ports.SandboxStateRunning
	}
	now := firstNonZeroTime(request.CreatedAt, r.now().UTC())
	instance := ports.SandboxInstanceStatus{
		TenantID:     request.TenantID,
		InstanceID:   "sandbox_" + strconv.FormatUint(r.sequence.Add(1), 10),
		Name:         request.Name,
		Kind:         ports.WorkloadKindSandbox,
		Provider:     "local_sandbox_runtime",
		State:        state,
		TemplateID:   config.TemplateID,
		SessionState: string(state),
		Config:       config,
		DevProfile: ports.DevProfileInfo{
			Mode:         "local",
			Provider:     "local-sandbox-runtime",
			RealProvider: false,
			Reason:       "local profile records Kata sandbox intent; it is not a real Kata provider execution",
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.instances[sandboxKey(instance.TenantID, instance.InstanceID)] = instance
	return instance, nil
}

func (r *LocalSandboxRuntime) CreateToken(_ context.Context, request ports.SandboxTokenRequest) (ports.SandboxTokenResult, error) {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.InstanceID) == "" {
		return ports.SandboxTokenResult{}, fmt.Errorf("%w: tenantID and instanceID are required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return ports.SandboxTokenResult{}, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	expiresIn := request.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 15 * time.Minute
	}
	if expiresIn > time.Hour {
		return ports.SandboxTokenResult{}, fmt.Errorf("%w: expires_in must be <= 1h", ports.ErrInvalid)
	}
	scopes, err := normalizeSandboxTokenScopes(request.Scopes)
	if err != nil {
		return ports.SandboxTokenResult{}, err
	}
	now := firstNonZeroTime(request.RequestedAt, r.now().UTC())
	key := sandboxTokenKey(request.TenantID, request.InstanceID, request.IdempotencyKey)

	r.mu.Lock()
	defer r.mu.Unlock()
	instance, ok := r.instances[sandboxKey(request.TenantID, request.InstanceID)]
	if !ok {
		return ports.SandboxTokenResult{}, ports.ErrNotFound
	}
	if instance.State != ports.SandboxStateRunning {
		return ports.SandboxTokenResult{}, fmt.Errorf("%w: sandbox token requires running sandbox", ports.ErrFailedPrecondition)
	}
	if existing, ok := r.tokens[key]; ok {
		if now.Before(existing.ExpiresAt) {
			return existing, nil
		}
		return ports.SandboxTokenResult{}, fmt.Errorf("%w: IdempotencyResultExpired", ports.ErrConflict)
	}
	result := ports.SandboxTokenResult{
		Token:     "sandbox_token_" + strconv.FormatUint(r.sequence.Add(1), 10),
		ExpiresAt: now.Add(expiresIn),
		Scopes:    scopes,
	}
	r.tokens[key] = result
	return result, nil
}

func (r *LocalSandboxRuntime) CreatePort(_ context.Context, request ports.SandboxPortRequest) (ports.SandboxPortResult, error) {
	if err := validateSandboxPortIdentity(request.TenantID, request.InstanceID, request.IdempotencyKey, request.Port); err != nil {
		return ports.SandboxPortResult{}, err
	}
	protocol := strings.ToLower(strings.TrimSpace(request.Protocol))
	if protocol == "" {
		protocol = "http"
	}
	if protocol != "http" && protocol != "tcp" {
		return ports.SandboxPortResult{}, fmt.Errorf("%w: protocol must be http or tcp", ports.ErrInvalid)
	}
	now := firstNonZeroTime(request.RequestedAt, r.now().UTC())
	instanceKey := sandboxKey(request.TenantID, request.InstanceID)
	idempotencyKey := sandboxTokenKey(request.TenantID, request.InstanceID, request.IdempotencyKey)

	r.mu.Lock()
	defer r.mu.Unlock()
	instance, ok := r.instances[instanceKey]
	if !ok {
		return ports.SandboxPortResult{}, ports.ErrNotFound
	}
	if instance.State != ports.SandboxStateRunning {
		return ports.SandboxPortResult{}, fmt.Errorf("%w: sandbox port requires running sandbox", ports.ErrFailedPrecondition)
	}
	if existing, ok := r.portKeys[idempotencyKey]; ok {
		return existing, nil
	}
	if r.ports[instanceKey] == nil {
		r.ports[instanceKey] = make(map[int]ports.SandboxPortResult)
	}
	if existing, ok := r.ports[instanceKey][request.Port]; ok {
		r.portKeys[idempotencyKey] = existing
		return existing, nil
	}
	result := ports.SandboxPortResult{
		Port:       request.Port,
		Name:       strings.TrimSpace(request.Name),
		Protocol:   protocol,
		Status:     "available",
		PreviewURL: fmt.Sprintf("http://127.0.0.1/sandboxes/%s/ports/%d", request.InstanceID, request.Port),
		ExpiresAt:  now.Add(time.Hour),
	}
	r.ports[instanceKey][request.Port] = result
	r.portKeys[idempotencyKey] = result
	return result, nil
}

func (r *LocalSandboxRuntime) DeletePort(_ context.Context, request ports.SandboxPortDeleteRequest) (ports.SandboxPortResult, error) {
	if err := validateSandboxPortIdentity(request.TenantID, request.InstanceID, request.IdempotencyKey, request.Port); err != nil {
		return ports.SandboxPortResult{}, err
	}
	idempotencyKey := sandboxTokenKey(request.TenantID, request.InstanceID, request.IdempotencyKey)
	instanceKey := sandboxKey(request.TenantID, request.InstanceID)

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.closeKeys[idempotencyKey]; ok {
		return existing, nil
	}
	instance, ok := r.instances[instanceKey]
	if !ok {
		return ports.SandboxPortResult{}, ports.ErrNotFound
	}
	if instance.State != ports.SandboxStateRunning {
		return ports.SandboxPortResult{}, fmt.Errorf("%w: sandbox port requires running sandbox", ports.ErrFailedPrecondition)
	}
	openPorts := r.ports[instanceKey]
	if openPorts == nil {
		return ports.SandboxPortResult{}, ports.ErrNotFound
	}
	result, ok := openPorts[request.Port]
	if !ok {
		return ports.SandboxPortResult{}, ports.ErrNotFound
	}
	delete(openPorts, request.Port)
	result.Status = "closing"
	r.closeKeys[idempotencyKey] = result
	return result, nil
}

func (r *LocalSandboxRuntime) ListFiles(_ context.Context, request ports.SandboxFileListRequest) (ports.SandboxFileListResult, error) {
	dir, err := normalizeSandboxFilePath(request.Path, true)
	if err != nil {
		return ports.SandboxFileListResult{}, err
	}
	instanceKey := sandboxKey(request.TenantID, request.InstanceID)

	r.mu.RLock()
	defer r.mu.RUnlock()
	instance, ok := r.instances[instanceKey]
	if !ok {
		return ports.SandboxFileListResult{}, ports.ErrNotFound
	}
	if instance.State != ports.SandboxStateRunning {
		return ports.SandboxFileListResult{}, fmt.Errorf("%w: sandbox files require running sandbox", ports.ErrFailedPrecondition)
	}
	records := r.files[instanceKey]
	items := make([]ports.SandboxFileResult, 0, len(records))
	for filePath, record := range records {
		if sandboxFileMatchesDir(filePath, dir) {
			items = append(items, record)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	limit := request.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	start := 0
	if strings.TrimSpace(request.Cursor) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(request.Cursor))
		if err != nil || parsed < 0 {
			return ports.SandboxFileListResult{}, fmt.Errorf("%w: cursor is invalid", ports.ErrInvalid)
		}
		start = parsed
	}
	if start > len(items) {
		start = len(items)
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	nextCursor := ""
	if end < len(items) {
		nextCursor = strconv.Itoa(end)
	}
	return ports.SandboxFileListResult{
		Items:      append([]ports.SandboxFileResult(nil), items[start:end]...),
		Total:      len(items),
		NextCursor: nextCursor,
	}, nil
}

func (r *LocalSandboxRuntime) WriteFile(_ context.Context, request ports.SandboxFileWriteRequest) (ports.SandboxFileResult, error) {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.InstanceID) == "" {
		return ports.SandboxFileResult{}, fmt.Errorf("%w: tenantID and instanceID are required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return ports.SandboxFileResult{}, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	filePath, err := normalizeSandboxFilePath(request.Path, false)
	if err != nil {
		return ports.SandboxFileResult{}, err
	}
	hasInline := strings.TrimSpace(request.ContentBase64) != ""
	hasUpload := strings.TrimSpace(request.UploadID) != ""
	if hasInline == hasUpload {
		return ports.SandboxFileResult{}, fmt.Errorf("%w: exactly one of content_base64 or upload_id is required", ports.ErrInvalid)
	}
	if hasUpload {
		return ports.SandboxFileResult{}, fmt.Errorf("%w: upload_id is not supported by local sandbox runtime", ports.ErrUnsupported)
	}
	content, err := base64.StdEncoding.DecodeString(strings.TrimSpace(request.ContentBase64))
	if err != nil {
		return ports.SandboxFileResult{}, fmt.Errorf("%w: content_base64 is invalid", ports.ErrInvalid)
	}
	now := firstNonZeroTime(request.RequestedAt, r.now().UTC())
	instanceKey := sandboxKey(request.TenantID, request.InstanceID)
	idempotencyKey := sandboxTokenKey(request.TenantID, request.InstanceID, request.IdempotencyKey)

	r.mu.Lock()
	defer r.mu.Unlock()
	instance, ok := r.instances[instanceKey]
	if !ok {
		return ports.SandboxFileResult{}, ports.ErrNotFound
	}
	if instance.State != ports.SandboxStateRunning {
		return ports.SandboxFileResult{}, fmt.Errorf("%w: sandbox files require running sandbox", ports.ErrFailedPrecondition)
	}
	if existing, ok := r.fileKeys[idempotencyKey]; ok {
		return existing, nil
	}
	if r.files[instanceKey] == nil {
		r.files[instanceKey] = make(map[string]ports.SandboxFileResult)
	}
	if _, ok := r.files[instanceKey][filePath]; ok && !request.Overwrite {
		return ports.SandboxFileResult{}, fmt.Errorf("%w: sandbox file already exists", ports.ErrConflict)
	}
	result := ports.SandboxFileResult{
		Path:      filePath,
		Kind:      "file",
		SizeBytes: int64(len(content)),
		UpdatedAt: now,
	}
	r.files[instanceKey][filePath] = result
	r.fileKeys[idempotencyKey] = result
	return result, nil
}

func (r *LocalSandboxRuntime) DeleteFile(_ context.Context, request ports.SandboxFileDeleteRequest) error {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.InstanceID) == "" {
		return fmt.Errorf("%w: tenantID and instanceID are required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	filePath, err := normalizeSandboxFilePath(request.Path, false)
	if err != nil {
		return err
	}
	instanceKey := sandboxKey(request.TenantID, request.InstanceID)
	idempotencyKey := sandboxTokenKey(request.TenantID, request.InstanceID, request.IdempotencyKey)

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.rmKeys[idempotencyKey]; ok {
		return nil
	}
	instance, ok := r.instances[instanceKey]
	if !ok {
		return ports.ErrNotFound
	}
	if instance.State != ports.SandboxStateRunning {
		return fmt.Errorf("%w: sandbox files require running sandbox", ports.ErrFailedPrecondition)
	}
	if r.files[instanceKey] == nil {
		return ports.ErrNotFound
	}
	if _, ok := r.files[instanceKey][filePath]; !ok {
		return ports.ErrNotFound
	}
	delete(r.files[instanceKey], filePath)
	r.rmKeys[idempotencyKey] = struct{}{}
	return nil
}

func (r *LocalSandboxRuntime) CreateCheckpoint(_ context.Context, request ports.SandboxCheckpointCreateRequest) (ports.SandboxCheckpointResult, error) {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.InstanceID) == "" {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: tenantID and instanceID are required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: checkpoint name is required", ports.ErrInvalid)
	}
	now := firstNonZeroTime(request.RequestedAt, r.now().UTC())
	instanceKey := sandboxKey(request.TenantID, request.InstanceID)
	idempotencyKey := sandboxTokenKey(request.TenantID, request.InstanceID, request.IdempotencyKey)

	r.mu.Lock()
	defer r.mu.Unlock()
	instance, ok := r.instances[instanceKey]
	if !ok {
		return ports.SandboxCheckpointResult{}, ports.ErrNotFound
	}
	if instance.State != ports.SandboxStateRunning {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: sandbox checkpoint requires running sandbox", ports.ErrFailedPrecondition)
	}
	if existing, ok := r.checkpointKeys[idempotencyKey]; ok {
		return existing, nil
	}
	if r.checkpoints[instanceKey] == nil {
		r.checkpoints[instanceKey] = make(map[string]ports.SandboxCheckpointResult)
	}
	result := ports.SandboxCheckpointResult{
		ID:         "sandbox_checkpoint_" + strconv.FormatUint(r.sequence.Add(1), 10),
		Name:       name,
		Status:     "available",
		KeepMemory: request.KeepMemory,
		CreatedAt:  now,
		SizeBytes:  int64(len(r.files[instanceKey]) * 4096),
	}
	r.checkpoints[instanceKey][result.ID] = result
	r.checkpointKeys[idempotencyKey] = result
	return result, nil
}

func (r *LocalSandboxRuntime) ListCheckpoints(_ context.Context, request ports.SandboxCheckpointListRequest) (ports.SandboxCheckpointListResult, error) {
	instanceKey := sandboxKey(request.TenantID, request.InstanceID)

	r.mu.RLock()
	defer r.mu.RUnlock()
	instance, ok := r.instances[instanceKey]
	if !ok {
		return ports.SandboxCheckpointListResult{}, ports.ErrNotFound
	}
	if instance.State != ports.SandboxStateRunning {
		return ports.SandboxCheckpointListResult{}, fmt.Errorf("%w: sandbox checkpoint requires running sandbox", ports.ErrFailedPrecondition)
	}
	items := make([]ports.SandboxCheckpointResult, 0, len(r.checkpoints[instanceKey]))
	for _, checkpoint := range r.checkpoints[instanceKey] {
		items = append(items, checkpoint)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	limit := request.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	start := 0
	if strings.TrimSpace(request.Cursor) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(request.Cursor))
		if err != nil || parsed < 0 {
			return ports.SandboxCheckpointListResult{}, fmt.Errorf("%w: cursor is invalid", ports.ErrInvalid)
		}
		start = parsed
	}
	if start > len(items) {
		start = len(items)
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	nextCursor := ""
	if end < len(items) {
		nextCursor = strconv.Itoa(end)
	}
	return ports.SandboxCheckpointListResult{
		Items:      append([]ports.SandboxCheckpointResult(nil), items[start:end]...),
		Total:      len(items),
		NextCursor: nextCursor,
	}, nil
}

func (r *LocalSandboxRuntime) RestoreCheckpoint(_ context.Context, request ports.SandboxCheckpointRestoreRequest) (ports.SandboxCheckpointResult, error) {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.InstanceID) == "" || strings.TrimSpace(request.CheckpointID) == "" {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: tenantID, instanceID, and checkpointID are required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	instanceKey := sandboxKey(request.TenantID, request.InstanceID)
	idempotencyKey := sandboxTokenKey(request.TenantID, request.InstanceID, request.IdempotencyKey)

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.restoreKeys[idempotencyKey]; ok {
		return existing, nil
	}
	instance, ok := r.instances[instanceKey]
	if !ok {
		return ports.SandboxCheckpointResult{}, ports.ErrNotFound
	}
	if instance.State != ports.SandboxStateRunning {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: sandbox checkpoint requires running sandbox", ports.ErrFailedPrecondition)
	}
	checkpoint, ok := r.checkpoints[instanceKey][strings.TrimSpace(request.CheckpointID)]
	if !ok {
		return ports.SandboxCheckpointResult{}, ports.ErrNotFound
	}
	if checkpoint.Status != "available" {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: checkpoint is not available", ports.ErrFailedPrecondition)
	}
	r.restoreKeys[idempotencyKey] = checkpoint
	return checkpoint, nil
}

func (r *LocalSandboxRuntime) CloneCheckpoint(_ context.Context, request ports.SandboxCheckpointCloneRequest) (ports.SandboxCheckpointResult, error) {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.InstanceID) == "" || strings.TrimSpace(request.CheckpointID) == "" {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: tenantID, instanceID, and checkpointID are required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.Name) == "" {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: clone name is required", ports.ErrInvalid)
	}
	instanceKey := sandboxKey(request.TenantID, request.InstanceID)
	idempotencyKey := sandboxTokenKey(request.TenantID, request.InstanceID, request.IdempotencyKey)

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.cloneKeys[idempotencyKey]; ok {
		return existing, nil
	}
	instance, ok := r.instances[instanceKey]
	if !ok {
		return ports.SandboxCheckpointResult{}, ports.ErrNotFound
	}
	if instance.State != ports.SandboxStateRunning {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: sandbox checkpoint requires running sandbox", ports.ErrFailedPrecondition)
	}
	checkpoint, ok := r.checkpoints[instanceKey][strings.TrimSpace(request.CheckpointID)]
	if !ok {
		return ports.SandboxCheckpointResult{}, ports.ErrNotFound
	}
	if checkpoint.Status != "available" {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: checkpoint is not available", ports.ErrFailedPrecondition)
	}
	checkpoint.Name = strings.TrimSpace(request.Name)
	r.cloneKeys[idempotencyKey] = checkpoint
	return checkpoint, nil
}

func (r *LocalSandboxRuntime) CreateCodeRun(ctx context.Context, request ports.SandboxCodeRunRequest) (ports.SandboxCodeRunResult, error) {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.InstanceID) == "" {
		return ports.SandboxCodeRunResult{}, fmt.Errorf("%w: tenantID and instanceID are required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return ports.SandboxCodeRunResult{}, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.Code) == "" {
		return ports.SandboxCodeRunResult{}, fmt.Errorf("%w: code is required", ports.ErrInvalid)
	}
	language := strings.ToLower(strings.TrimSpace(request.Language))
	if language != "python" && language != "javascript" {
		return ports.SandboxCodeRunResult{}, fmt.Errorf("%w: language must be python or javascript", ports.ErrInvalid)
	}
	timeout := request.TimeoutSeconds
	if timeout <= 0 {
		timeout = 60
	}
	if timeout > 300 {
		return ports.SandboxCodeRunResult{}, fmt.Errorf("%w: timeout_seconds must be <= 300", ports.ErrInvalid)
	}
	request.Language = language
	request.TimeoutSeconds = timeout
	instanceKey := sandboxKey(request.TenantID, request.InstanceID)
	idempotencyKey := sandboxTokenKey(request.TenantID, request.InstanceID, request.IdempotencyKey)
	now := firstNonZeroTime(request.RequestedAt, r.now().UTC())

	r.mu.Lock()
	instance, ok := r.instances[instanceKey]
	if !ok {
		r.mu.Unlock()
		return ports.SandboxCodeRunResult{}, ports.ErrNotFound
	}
	if instance.State != ports.SandboxStateRunning {
		r.mu.Unlock()
		return ports.SandboxCodeRunResult{}, fmt.Errorf("%w: sandbox code run requires running sandbox", ports.ErrFailedPrecondition)
	}
	if existing, ok := r.codeRunKeys[idempotencyKey]; ok {
		r.mu.Unlock()
		return existing, nil
	}
	runner := r.codeRunner
	runID := "sandbox_code_run_" + strconv.FormatUint(r.sequence.Add(1), 10)
	r.mu.Unlock()

	if runner == nil {
		result := ports.SandboxCodeRunResult{
			ID:        runID,
			Status:    "accepted",
			Language:  language,
			CreatedAt: now,
		}
		r.mu.Lock()
		if existing, ok := r.codeRunKeys[idempotencyKey]; ok {
			r.mu.Unlock()
			return existing, nil
		}
		r.codeRunKeys[idempotencyKey] = result
		r.mu.Unlock()
		return result, nil
	}

	result, err := runner(ctx, request, instance)
	if err != nil {
		return ports.SandboxCodeRunResult{}, err
	}
	if strings.TrimSpace(result.ID) == "" {
		result.ID = runID
	}
	if strings.TrimSpace(result.Language) == "" {
		result.Language = language
	}
	if result.CreatedAt.IsZero() {
		result.CreatedAt = now
	}
	r.mu.Lock()
	if existing, ok := r.codeRunKeys[idempotencyKey]; ok {
		r.mu.Unlock()
		return existing, nil
	}
	r.codeRunKeys[idempotencyKey] = result
	r.mu.Unlock()
	return result, nil
}

func (r *LocalSandboxRuntime) Get(_ context.Context, request ports.SandboxGetRequest) (ports.SandboxInstanceStatus, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	instance, ok := r.instances[sandboxKey(request.TenantID, request.InstanceID)]
	if !ok {
		return ports.SandboxInstanceStatus{}, ports.ErrNotFound
	}
	return instance, nil
}

func (r *LocalSandboxRuntime) List(_ context.Context, request ports.SandboxListRequest) ([]ports.SandboxInstanceStatus, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]ports.SandboxInstanceStatus, 0, len(r.instances))
	for _, instance := range r.instances {
		if request.TenantID != "" && instance.TenantID != request.TenantID {
			continue
		}
		items = append(items, instance)
	}
	return items, nil
}

func (r *LocalSandboxRuntime) upsertInstance(instance ports.SandboxInstanceStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.instances[sandboxKey(instance.TenantID, instance.InstanceID)] = instance
}

func (r *LocalSandboxRuntime) ApplyLifecycle(_ context.Context, request ports.SandboxLifecycleRequest) (ports.SandboxInstanceStatus, error) {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.InstanceID) == "" {
		return ports.SandboxInstanceStatus{}, fmt.Errorf("%w: tenantID and instanceID are required", ports.ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := sandboxKey(request.TenantID, request.InstanceID)
	instance, ok := r.instances[key]
	if !ok {
		return ports.SandboxInstanceStatus{}, ports.ErrNotFound
	}
	switch request.Action {
	case ports.WorkloadLifecyclePause:
		if instance.State != ports.SandboxStateRunning {
			return ports.SandboxInstanceStatus{}, fmt.Errorf("%w: pause requires running sandbox", ports.ErrConflict)
		}
		instance.State = ports.SandboxStatePaused
	case ports.WorkloadLifecycleResume:
		if instance.State != ports.SandboxStatePaused {
			return ports.SandboxInstanceStatus{}, fmt.Errorf("%w: resume requires paused sandbox", ports.ErrConflict)
		}
		instance.State = ports.SandboxStateRunning
	case ports.WorkloadLifecycleExtend:
		if request.Duration <= 0 {
			return ports.SandboxInstanceStatus{}, fmt.Errorf("%w: duration must be positive", ports.ErrInvalid)
		}
		instance.Config.SessionTimeout += request.Duration
	case ports.WorkloadLifecycleTouchIdle:
		// UpdatedAt is the local profile's last-activity marker.
	case ports.WorkloadLifecycleDelete:
		instance.State = ports.SandboxStateStopped
		instance.SessionState = string(instance.State)
		instance.UpdatedAt = firstNonZeroTime(request.RequestedAt, r.now().UTC())
		delete(r.instances, key)
		return instance, nil
	default:
		return ports.SandboxInstanceStatus{}, fmt.Errorf("%w: unsupported sandbox lifecycle action %q", ports.ErrUnsupported, request.Action)
	}
	instance.SessionState = string(instance.State)
	instance.UpdatedAt = firstNonZeroTime(request.RequestedAt, r.now().UTC())
	r.instances[key] = instance
	return instance, nil
}

func normalizeSandboxConfig(config ports.SandboxConfig) ports.SandboxConfig {
	if strings.TrimSpace(config.RuntimeClass) == "" {
		config.RuntimeClass = "sandbox-kata"
	}
	if config.SessionTimeout <= 0 {
		config.SessionTimeout = 30 * time.Minute
	}
	if config.IdleTimeout <= 0 {
		config.IdleTimeout = 10 * time.Minute
	}
	if strings.TrimSpace(config.OnTimeout) == "" {
		config.OnTimeout = "pause"
	}
	if strings.TrimSpace(string(config.NetworkEgressPolicy)) == "" {
		config.NetworkEgressPolicy = ports.SandboxNetworkEgressDenyAll
	}
	return config
}

func validateSandboxConfig(config ports.SandboxConfig) error {
	switch config.NetworkEgressPolicy {
	case ports.SandboxNetworkEgressDenyAll, ports.SandboxNetworkEgressAllowlist, ports.SandboxNetworkEgressInternet:
	default:
		return fmt.Errorf("%w: unsupported sandbox network egress policy %q", ports.ErrInvalid, config.NetworkEgressPolicy)
	}
	return nil
}

func sandboxKey(tenantID string, instanceID string) string {
	return tenantID + "/" + instanceID
}

func sandboxTokenKey(tenantID string, instanceID string, idempotencyKey string) string {
	return sandboxKey(tenantID, instanceID) + "/" + strings.TrimSpace(idempotencyKey)
}

func validateSandboxPortIdentity(tenantID string, instanceID string, idempotencyKey string, port int) error {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(instanceID) == "" {
		return fmt.Errorf("%w: tenantID and instanceID are required", ports.ErrInvalid)
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("%w: port must be between 1 and 65535", ports.ErrInvalid)
	}
	return nil
}

func normalizeSandboxFilePath(raw string, allowCurrentDir bool) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		if allowCurrentDir {
			return ".", nil
		}
		return "", fmt.Errorf("%w: path is required", ports.ErrInvalid)
	}
	if strings.ContainsRune(value, '\x00') {
		return "", fmt.Errorf("%w: path must not contain NUL", ports.ErrInvalid)
	}
	if strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("%w: absolute sandbox file paths are not allowed", ports.ErrInvalid)
	}
	clean := pathpkg.Clean(value)
	if clean == "." {
		if allowCurrentDir {
			return clean, nil
		}
		return "", fmt.Errorf("%w: path must point to a file", ports.ErrInvalid)
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: sandbox file path must stay within workspace", ports.ErrInvalid)
	}
	return clean, nil
}

func sandboxFileMatchesDir(filePath string, dir string) bool {
	if dir == "." {
		return true
	}
	return filePath == dir || strings.HasPrefix(filePath, dir+"/")
}

func normalizeSandboxTokenScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return []string{"connect"}, nil
	}
	allowed := map[string]struct{}{"connect": {}, "exec": {}, "files": {}, "ports": {}}
	out := make([]string, 0, len(scopes))
	seen := map[string]struct{}{}
	for _, scope := range scopes {
		scope = strings.ToLower(strings.TrimSpace(scope))
		if _, ok := allowed[scope]; !ok {
			return nil, fmt.Errorf("%w: unsupported sandbox token scope %q", ports.ErrInvalid, scope)
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		out = append(out, scope)
	}
	if len(out) == 0 {
		return []string{"connect"}, nil
	}
	return out, nil
}

var _ ports.SandboxRuntime = (*LocalSandboxRuntime)(nil)
