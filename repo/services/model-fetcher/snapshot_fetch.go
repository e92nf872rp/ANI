package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	modelv1 "github.com/kubercloud/ani/pkg/generated/pb/model/v1"
	"github.com/kubercloud/ani/pkg/types"
)

const maxSnapshotManifestBytes int64 = 8 << 20

const maxSnapshotManifestFiles = 10000

func isModelSnapshotObject(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "object" || u.Host != "models" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	return strings.HasSuffix(u.Path, "/snapshot/manifest.json") && !strings.Contains(u.Path, "..")
}

func fetchModelSnapshot(ctx context.Context, cfg FetcherConfig, response *modelv1.GetModelDownloadURLResponse, modelClient modelDownloadURLClient, client *http.Client) error {
	if response == nil || !isModelSnapshotObject(cfg.ObjectRef) || response.GetSizeBytes() != 0 && response.GetSizeBytes() != cfg.ExpectedSize {
		return errors.New("invalid model snapshot descriptor")
	}
	if cfg.ExpectedSize < 0 || cfg.ExpectedSize > maxSnapshotManifestBytes || !validDigest(cfg.ExpectedSHA256) {
		return errors.New("invalid model snapshot descriptor")
	}
	parent := filepath.Dir(cfg.TargetPath)
	if err := os.MkdirAll(parent, 0750); err != nil {
		return errors.New("create model directory")
	}
	manifestName := ".model-fetch-manifest.json"
	if err := Download(ctx, Descriptor{URL: response.GetDownloadUrl(), Filename: manifestName, ExpectedSize: cfg.ExpectedSize, ExpectedSHA256: cfg.ExpectedSHA256, AllowInsecureHTTP: cfg.AllowInsecureHTTP}, client, parent); err != nil {
		return err
	}
	manifestPath := filepath.Join(parent, manifestName)
	defer func() { _ = os.Remove(manifestPath) }()
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil || int64(len(manifestBytes)) > maxSnapshotManifestBytes {
		return errors.New("invalid model snapshot manifest")
	}
	snapshot, err := types.ParseModelSnapshot(manifestBytes)
	if err != nil {
		return errors.New("invalid model snapshot manifest")
	}
	if sum := sha256.Sum256(manifestBytes); !strings.EqualFold(hex.EncodeToString(sum[:]), normalizeDigest(cfg.ExpectedSHA256)) {
		return errors.New("model snapshot checksum mismatch")
	}
	if len(snapshot.Files) > maxSnapshotManifestFiles {
		return errors.New("model snapshot file count exceeds limit")
	}
	if snapshot.TotalSizeBytes > maxDownloadSize {
		return errors.New("model snapshot total size exceeds limit")
	}
	manifestPrefix, err := snapshotObjectKeyPrefix(cfg.ObjectRef)
	if err != nil {
		return err
	}
	if err := ensureSnapshotTarget(cfg.TargetPath); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".model-snapshot-*")
	if err != nil {
		return errors.New("create model snapshot workspace")
	}
	defer func() { _ = os.RemoveAll(staging) }()
	for _, entry := range snapshot.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !safeSnapshotFilePath(entry.Path) || entry.ObjectKey != manifestPrefix+entry.Path {
			return errors.New("invalid model snapshot file path")
		}
		download, err := modelSnapshotFileURL(ctx, cfg, entry, modelClient)
		if err != nil {
			return err
		}
		target := filepath.Join(staging, filepath.FromSlash(entry.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return errors.New("create model snapshot directory")
		}
		if err := Download(ctx, Descriptor{URL: download.GetDownloadUrl(), Filename: filepath.Base(target), ExpectedSize: entry.SizeBytes, ExpectedSHA256: entry.SHA256, AllowInsecureHTTP: cfg.AllowInsecureHTTP}, client, filepath.Dir(target)); err != nil {
			return err
		}
	}
	if err := syncDirectory(staging); err != nil {
		return errors.New("sync model snapshot")
	}
	if _, err := os.Lstat(cfg.TargetPath); err == nil {
		return errors.New("model snapshot destination already exists")
	} else if !os.IsNotExist(err) {
		return errors.New("inspect model snapshot destination")
	}
	if err := os.Rename(staging, cfg.TargetPath); err != nil {
		return errors.New("install model snapshot")
	}
	if err := syncDirectory(parent); err != nil {
		return errors.New("sync model snapshot parent")
	}
	return nil
}

func snapshotObjectKeyPrefix(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !isModelSnapshotObject(raw) {
		return "", errors.New("invalid model snapshot object")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 5 || parts[1] == "" || parts[2] == "" || parts[3] != "snapshot" {
		return "", errors.New("invalid model snapshot object")
	}
	return parts[1] + "/" + parts[2] + "/snapshot/files/", nil
}

func modelSnapshotFileURL(ctx context.Context, cfg FetcherConfig, entry types.ModelSnapshotFile, client modelDownloadURLClient) (*modelv1.GetModelDownloadURLResponse, error) {
	if client == nil {
		return nil, errors.New("model snapshot file lookup unavailable")
	}
	response, err := client.GetModelDownloadURL(ctx, &modelv1.GetModelDownloadURLRequest{TenantId: cfg.TenantID, ModelVersionId: cfg.ModelVersionID, Requester: "init-container", FilePath: entry.Path})
	if err != nil || response == nil || strings.TrimSpace(response.GetDownloadUrl()) == "" || strings.TrimSpace(response.GetStoragePath()) != strings.TrimSpace(cfg.ObjectRef) || response.GetSizeBytes() != entry.SizeBytes || !strings.EqualFold(normalizeDigest(response.GetChecksumSha256()), normalizeDigest(entry.SHA256)) {
		return nil, errors.New("model-service snapshot file lookup failed")
	}
	return response, nil
}

func ensureSnapshotTarget(target string) error {
	if target == "" || !filepath.IsAbs(target) || filepath.Base(target) == "." || filepath.Base(target) == string(filepath.Separator) {
		return errors.New("invalid model snapshot target")
	}
	return nil
}

func safeSnapshotFilePath(value string) bool {
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || strings.Contains(value, "//") || strings.Contains(value, "..") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
