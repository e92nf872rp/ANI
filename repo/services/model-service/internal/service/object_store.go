package service

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/types"
)

type ModelObjectRef = types.ModelObjectRef
type ModelObjectMetadata = types.ModelObjectMetadata
type ModelSignedURL = types.ModelSignedURL
type ModelObjectStore = types.ModelObjectStore
type ModelObjectStoreContentVerifier = types.ModelObjectStoreContentVerifier

func parseModelObjectPath(raw string) (ModelObjectRef, error) {
	value := strings.TrimSpace(raw)
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "object" || u.Host != "models" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" {
		return ModelObjectRef{}, fmt.Errorf("invalid model object path")
	}
	if strings.Contains(u.EscapedPath(), "..") || strings.Contains(u.Path, "..") {
		return ModelObjectRef{}, fmt.Errorf("invalid model object path")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 5 {
		return ModelObjectRef{}, fmt.Errorf("model object path must contain tenant, model, version, document, and file")
	}
	tenantID, modelID := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	if _, err := uuid.Parse(tenantID); err != nil {
		return ModelObjectRef{}, fmt.Errorf("invalid model object tenant")
	}
	if _, err := uuid.Parse(modelID); err != nil {
		return ModelObjectRef{}, fmt.Errorf("invalid model object model")
	}
	for _, part := range parts[2:] {
		if part == "" || part == "." || part == ".." {
			return ModelObjectRef{}, fmt.Errorf("invalid model object path")
		}
	}
	return ModelObjectRef{
		TenantID: tenantID, ModelID: modelID, BucketClass: "model", Version: parts[2],
		ObjectKey: path.Join(parts[1], parts[2], parts[3], parts[4]),
	}, nil
}

func modelObjectStoragePath(tenantID, modelID, version, documentID, fileName string) string {
	return "object://models/" + tenantID + "/" + modelID + "/" + version + "/" + documentID + "/" + fileName
}

func validateModelUploadFileName(fileName string) error {
	name := strings.TrimSpace(fileName)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return fmt.Errorf("file_name must be a single safe file name")
	}
	return nil
}
