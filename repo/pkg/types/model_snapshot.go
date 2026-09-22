package types

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const ModelSnapshotSchema = "ani.model.snapshot.v1"

type ModelSnapshot struct {
	Schema         string              `json:"schema"`
	Revision       string              `json:"revision"`
	TotalSizeBytes int64               `json:"total_size_bytes"`
	Files          []ModelSnapshotFile `json:"files"`
}

type ModelSnapshotFile struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
	ObjectKey string `json:"object_key"`
}

func ParseModelSnapshot(data []byte) (ModelSnapshot, error) {
	var snapshot ModelSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return ModelSnapshot{}, fmt.Errorf("invalid model snapshot: %w", err)
	}
	if err := snapshot.Validate(); err != nil {
		return ModelSnapshot{}, err
	}
	return snapshot, nil
}

func (s ModelSnapshot) Validate() error {
	if s.Schema != ModelSnapshotSchema || strings.TrimSpace(s.Revision) == "" || s.TotalSizeBytes < 0 {
		return fmt.Errorf("invalid model snapshot header")
	}
	if len(s.Files) == 0 {
		return fmt.Errorf("model snapshot contains no files")
	}
	var total int64
	seen := make(map[string]struct{}, len(s.Files))
	for _, file := range s.Files {
		if file.SizeBytes < 0 || file.Path == "" || !safeSnapshotPath(file.Path) || file.ObjectKey == "" || !safeSnapshotPath(file.ObjectKey) || !strings.Contains(file.ObjectKey, "/snapshot/files/") || !strings.HasSuffix(file.ObjectKey, "/"+file.Path) {
			return fmt.Errorf("invalid model snapshot file path")
		}
		if _, ok := seen[file.Path]; ok {
			return fmt.Errorf("duplicate model snapshot file")
		}
		seen[file.Path] = struct{}{}
		if len(file.SHA256) != 64 {
			return fmt.Errorf("invalid model snapshot checksum")
		}
		if _, err := hex.DecodeString(file.SHA256); err != nil {
			return fmt.Errorf("invalid model snapshot checksum")
		}
		if total > s.TotalSizeBytes-file.SizeBytes {
			return fmt.Errorf("model snapshot size overflow")
		}
		total += file.SizeBytes
	}
	if total != s.TotalSizeBytes {
		return fmt.Errorf("model snapshot total size mismatch")
	}
	return nil
}

func safeSnapshotPath(value string) bool {
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
