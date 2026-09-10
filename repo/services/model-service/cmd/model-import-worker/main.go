package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kubercloud/ani/pkg/bootstrap"
	"github.com/kubercloud/ani/pkg/nats"
	"github.com/kubercloud/ani/pkg/ports"
	sharedrepo "github.com/kubercloud/ani/pkg/repo"
	"github.com/kubercloud/ani/services/model-service/internal/config"
	"github.com/kubercloud/ani/services/model-service/internal/importer"
	modelrepo "github.com/kubercloud/ani/services/model-service/internal/repo"
)

const (
	importWorkerConsumer = "model-import-worker"
	importWorkerQueue    = "model-import-workers"
	importWorkerAckWait  = 30 * time.Minute
	importWorkerMaxDeliv = 3
)

const (
	maxImportFilesEnv       = "MODEL_IMPORT_MAX_FILES"
	maxImportTotalBytesEnv  = "MODEL_IMPORT_MAX_TOTAL_BYTES"
	maxImportFileBytesEnv   = "MODEL_IMPORT_MAX_FILE_BYTES"
	maxImportOutputBytesEnv = "MODEL_IMPORT_MAX_OUTPUT_BYTES"
)

// loadArchiveLimits reads optional worker policy overrides. Empty values use
// the workspace-aligned defaults; malformed, negative, oversized, or
// internally inconsistent values fail closed before any dependency is opened.
func loadArchiveLimits() (importer.ArchiveLimits, error) {
	limits := importer.DefaultWorkerArchiveLimits()
	fields := []struct {
		name   string
		target *int64
	}{
		{name: maxImportTotalBytesEnv, target: &limits.MaxTotalBytes},
		{name: maxImportFileBytesEnv, target: &limits.MaxFileBytes},
		{name: maxImportOutputBytesEnv, target: &limits.MaxOutputBytes},
	}
	for _, field := range fields {
		raw, ok := os.LookupEnv(field.name)
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || value <= 0 {
			return importer.ArchiveLimits{}, fmt.Errorf("%s must be a positive integer", field.name)
		}
		*field.target = value
	}
	if raw, ok := os.LookupEnv(maxImportFilesEnv); ok && strings.TrimSpace(raw) != "" {
		value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || value <= 0 || value > int64(^uint(0)>>1) {
			return importer.ArchiveLimits{}, fmt.Errorf("%s must be a positive integer", maxImportFilesEnv)
		}
		limits.MaxFiles = int(value)
	}
	if err := importer.ValidateWorkerArchiveLimits(limits); err != nil {
		return importer.ArchiveLimits{}, fmt.Errorf("invalid archive limits: %w", err)
	}
	return limits, nil
}

func main() {
	archiveLimits, err := loadArchiveLimits()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "model import worker configuration rejected: %v\n", err)
		return
	}
	cfg := config.Load()
	deps := bootstrap.MustConnect(cfg)
	defer deps.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	workerID := strings.TrimSpace(os.Getenv("MODEL_IMPORT_WORKER_ID"))
	worker := importer.NewWorker(
		importer.NewPostgresImportStore(deps.DB, modelrepo.NewPostgresModelRepo(), sharedrepo.NewPostgresAsyncTaskRepo()),
		deps.Ports.ObjectStore,
		map[string]importer.Source{
			"huggingface": importer.NewHuggingFaceSource(),
			"modelscope":  importer.NewModelScopeSource(),
		},
		importer.WorkerConfig{WorkerID: workerID, LeaseDuration: importWorkerAckWait, Limits: archiveLimits},
	)

	handler := func(messageCtx context.Context, message ports.Message) error {
		var payload nats.ModelImportMsg
		if err := json.Unmarshal(message.Data(), &payload); err != nil {
			// Invalid payloads cannot succeed on redelivery. Returning nil acks the
			// poison event; the publisher's durable outbox row remains available
			// for operator inspection.
			return nil
		}
		return worker.Handle(messageCtx, payload)
	}
	subscription, err := deps.Ports.MessageBus.Subscribe(ports.SubscribeOptions{
		Subject:     nats.SubjectModelImport,
		Consumer:    importWorkerConsumer,
		Queue:       importWorkerQueue,
		MaxInflight: 1,
		AckWait:     importWorkerAckWait,
		MaxDeliver:  importWorkerMaxDeliv,
	}, handler)
	if err != nil {
		deps.Logger.Error("model import worker subscription failed", "err", err)
		return
	}
	<-ctx.Done()
	if err := subscription.Drain(context.Background()); err != nil {
		deps.Logger.Error("model import worker drain failed", "err", err)
	}
}
