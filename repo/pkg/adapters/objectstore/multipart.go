package objectstore

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/kubercloud/ani/pkg/adapters/resilience"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/minio/minio-go/v7"
)

const maxMultipartPartNumber = 10000
const maxMultipartPartSize = int64(5 * 1024 * 1024 * 1024)

func (s *MinIOObjectStore) BeginMultipart(ctx context.Context, ref ports.ObjectRef, contentType string) (string, error) {
	bucket, object, err := s.multipartObject(ref)
	if err != nil {
		return "", err
	}
	var uploadID string
	err = s.multipartCall(ctx, "begin multipart upload", func(callCtx context.Context) error {
		uploadID, err = s.core.NewMultipartUpload(callCtx, bucket, object, minio.PutObjectOptions{ContentType: strings.TrimSpace(contentType)})
		return err
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(uploadID) == "" {
		return "", fmt.Errorf("%w: MinIO returned an empty upload id", ports.ErrFailedPrecondition)
	}
	return uploadID, nil
}

func (s *MinIOObjectStore) ListParts(ctx context.Context, ref ports.ObjectRef, uploadID string) ([]ports.MultipartPart, error) {
	bucket, object, err := s.multipartObject(ref)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(uploadID) == "" {
		return nil, fmt.Errorf("%w: upload id is required", ports.ErrInvalid)
	}
	parts := make([]ports.MultipartPart, 0)
	marker := 0
	for {
		var result minio.ListObjectPartsResult
		err = s.multipartCall(ctx, "list multipart parts", func(callCtx context.Context) error {
			result, err = s.core.ListObjectParts(callCtx, bucket, object, uploadID, marker, 1000)
			return err
		})
		if err != nil {
			return nil, err
		}
		if result.UploadID != "" && result.UploadID != uploadID {
			return nil, fmt.Errorf("%w: MinIO returned a different upload id", ports.ErrFailedPrecondition)
		}
		for _, part := range result.ObjectParts {
			etag := normalizeMultipartETag(part.ETag)
			if part.PartNumber <= 0 || part.PartNumber > maxMultipartPartNumber || part.Size < 0 || part.Size > maxMultipartPartSize || etag == "" {
				return nil, fmt.Errorf("%w: MinIO returned invalid multipart part", ports.ErrFailedPrecondition)
			}
			parts = append(parts, ports.MultipartPart{Number: part.PartNumber, ETag: etag, Size: part.Size})
		}
		if !result.IsTruncated {
			break
		}
		next := result.NextPartNumberMarker
		if next <= marker {
			return nil, fmt.Errorf("%w: MinIO returned a non-advancing multipart marker", ports.ErrFailedPrecondition)
		}
		marker = next
	}
	for i := 1; i < len(parts); i++ {
		if parts[i-1].Number >= parts[i].Number {
			return nil, fmt.Errorf("%w: MinIO returned duplicate or unordered multipart parts", ports.ErrFailedPrecondition)
		}
	}
	return parts, nil
}

func (s *MinIOObjectStore) UploadPart(ctx context.Context, ref ports.ObjectRef, uploadID string, partNumber int, body io.Reader, size int64) (ports.MultipartPart, error) {
	bucket, object, err := s.multipartObject(ref)
	if err != nil {
		return ports.MultipartPart{}, err
	}
	if strings.TrimSpace(uploadID) == "" || partNumber <= 0 || partNumber > maxMultipartPartNumber || size <= 0 || size > maxMultipartPartSize || body == nil {
		return ports.MultipartPart{}, fmt.Errorf("%w: upload part requires upload id, valid part number, body, and positive size", ports.ErrInvalid)
	}
	stream := &multipartPartReader{reader: body, remaining: size}
	var uploaded minio.ObjectPart
	err = s.multipartCall(ctx, "upload multipart part", func(callCtx context.Context) error {
		uploaded, err = s.core.PutObjectPart(callCtx, bucket, object, uploadID, partNumber, stream, size, minio.PutObjectPartOptions{DisableContentSha256: false})
		return err
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ports.MultipartPart{}, ctxErr
	}
	if stream.remaining != 0 {
		return ports.MultipartPart{}, fmt.Errorf("%w: multipart part body shorter than declared size", ports.ErrInvalid)
	}
	if err != nil {
		return ports.MultipartPart{}, err
	}
	var extra [1]byte
	n, readErr := body.Read(extra[:])
	if n != 0 {
		return ports.MultipartPart{}, fmt.Errorf("%w: multipart part body exceeds declared size", ports.ErrInvalid)
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return ports.MultipartPart{}, fmt.Errorf("%w: multipart part body could not be checked", ports.ErrInvalid)
	}
	etag := normalizeMultipartETag(uploaded.ETag)
	if etag == "" {
		return ports.MultipartPart{}, fmt.Errorf("%w: MinIO returned an empty part etag", ports.ErrFailedPrecondition)
	}
	return ports.MultipartPart{Number: partNumber, ETag: etag, Size: size}, nil
}

func (s *MinIOObjectStore) CompleteMultipart(ctx context.Context, ref ports.ObjectRef, uploadID string, parts []ports.MultipartPart) (ports.ObjectMetadata, error) {
	bucket, object, err := s.multipartObject(ref)
	if err != nil {
		return ports.ObjectMetadata{}, err
	}
	if strings.TrimSpace(uploadID) == "" {
		return ports.ObjectMetadata{}, fmt.Errorf("%w: upload id is required", ports.ErrInvalid)
	}
	validated := append([]ports.MultipartPart(nil), parts...)
	if err := validateMultipartParts(validated); err != nil {
		return ports.ObjectMetadata{}, err
	}
	completeParts := make([]minio.CompletePart, len(validated))
	var size int64
	for i, part := range validated {
		if part.Size > math.MaxInt64-size {
			return ports.ObjectMetadata{}, fmt.Errorf("%w: multipart size overflows int64", ports.ErrInvalid)
		}
		size += part.Size
		completeParts[i] = minio.CompletePart{PartNumber: part.Number, ETag: normalizeMultipartETag(part.ETag)}
	}
	var info minio.UploadInfo
	err = s.multipartCall(ctx, "complete multipart upload", func(callCtx context.Context) error {
		info, err = s.core.CompleteMultipartUpload(callCtx, bucket, object, uploadID, completeParts, minio.PutObjectOptions{})
		return err
	})
	if err != nil {
		return ports.ObjectMetadata{}, err
	}
	checksum := multipartChecksum(info.ChecksumSHA256)
	// An S3 ETag is not a SHA-256 digest for multipart objects (and is often
	// an MD5-of-parts value). Leave Checksum empty when MinIO did not return a
	// real SHA-256 so callers fall back to content verification instead of
	// treating the ETag as authoritative model metadata.
	updatedAt := info.LastModified.UTC()
	if updatedAt.IsZero() {
		updatedAt = s.now().UTC()
	}
	return ports.ObjectMetadata{Ref: ref, SizeBytes: size, Checksum: checksum, UpdatedAt: updatedAt}, nil
}

func (s *MinIOObjectStore) AbortMultipart(ctx context.Context, ref ports.ObjectRef, uploadID string) error {
	bucket, object, err := s.multipartObject(ref)
	if err != nil {
		return err
	}
	if strings.TrimSpace(uploadID) == "" {
		return fmt.Errorf("%w: upload id is required", ports.ErrInvalid)
	}
	return s.multipartCall(ctx, "abort multipart upload", func(callCtx context.Context) error {
		return s.core.AbortMultipartUpload(callCtx, bucket, object, uploadID)
	})
}

func (s *MinIOObjectStore) multipartObject(ref ports.ObjectRef) (string, string, error) {
	target, err := s.objectURL(ref) // Reuse the existing tenant and object-key validation.
	if err != nil {
		return "", "", err
	}
	bucket, err := s.bucketName(ref.BucketClass)
	if err != nil {
		return "", "", err
	}
	prefix := "/" + bucket + "/"
	object := strings.TrimPrefix(target.Path, prefix)
	if object == "" {
		return "", "", fmt.Errorf("%w: object key is required", ports.ErrInvalid)
	}
	return bucket, object, nil
}

func (s *MinIOObjectStore) multipartCall(ctx context.Context, operation string, fn func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := resilience.Do(ctx, s.policy, fn)
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	response := minio.ToErrorResponse(err)
	if response.StatusCode > 0 {
		return minIOHTTPError(response.StatusCode, operation)
	}
	return fmt.Errorf("MinIO %s failed", operation)
}

func validateMultipartParts(parts []ports.MultipartPart) error {
	if len(parts) == 0 {
		return fmt.Errorf("%w: at least one multipart part is required", ports.ErrInvalid)
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].Number < parts[j].Number })
	for i, part := range parts {
		if part.Number <= 0 || part.Number > maxMultipartPartNumber || part.Size <= 0 || part.Size > maxMultipartPartSize || normalizeMultipartETag(part.ETag) == "" {
			return fmt.Errorf("%w: multipart parts must have valid number, etag, and positive size", ports.ErrInvalid)
		}
		if i > 0 && parts[i-1].Number == part.Number {
			return fmt.Errorf("%w: multipart parts contain duplicate numbers", ports.ErrInvalid)
		}
	}
	return nil
}

func normalizeMultipartETag(etag string) string {
	return strings.Trim(strings.TrimSpace(etag), `"`)
}

type multipartPartReader struct {
	reader    io.Reader
	remaining int64
}

func (r *multipartPartReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	return n, err
}

func multipartChecksum(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err == nil && len(decoded) == 32 {
		return hex.EncodeToString(decoded)
	}
	if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) == 32 {
		return hex.EncodeToString(decoded)
	}
	return ""
}
