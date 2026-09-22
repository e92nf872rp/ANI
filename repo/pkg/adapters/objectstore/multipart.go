package objectstore

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

const maxMultipartPartNumber = 10000

func (s *MinIOObjectStore) multipartObject(ref ports.ObjectRef) (url.URL, error) {
	return s.objectURL(ref)
}

type initiateMultipartUploadResult struct {
	UploadID string `xml:"UploadId"`
}

type listPartsResult struct {
	IsTruncated          bool `xml:"IsTruncated"`
	NextPartNumberMarker int  `xml:"NextPartNumberMarker"`
	Parts                []struct {
		PartNumber int    `xml:"PartNumber"`
		ETag       string `xml:"ETag"`
		Size       int64  `xml:"Size"`
	} `xml:"Part"`
}

type completeMultipartUploadRequest struct {
	XMLName xml.Name `xml:"CompleteMultipartUpload"`
	Parts   []struct {
		PartNumber int    `xml:"PartNumber"`
		ETag       string `xml:"ETag"`
	} `xml:"Part"`
}

type completeMultipartUploadResult struct {
	ETag string `xml:"ETag"`
}

func (s *MinIOObjectStore) BeginMultipart(ctx context.Context, ref ports.ObjectRef, contentType string) (string, error) {
	target, err := s.multipartObject(ref)
	if err != nil {
		return "", err
	}
	query := url.Values{}
	query.Set("uploads", "")
	target.RawQuery = canonicalQuery(query)
	headers := map[string]string{}
	if value := strings.TrimSpace(contentType); value != "" {
		headers["Content-Type"] = value
	}
	req, err := s.newSignedRequestWithHeaders(ctx, http.MethodPost, target, nil, "", headers)
	if err != nil {
		return "", err
	}
	resp, err := s.doRequest(req)
	if err != nil {
		return "", err
	}
	defer closeBody(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", minIOHTTPError(resp.StatusCode, "begin multipart upload")
	}
	var result initiateMultipartUploadResult
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode multipart upload response: %w", err)
	}
	if strings.TrimSpace(result.UploadID) == "" {
		return "", fmt.Errorf("%w: MinIO returned an empty upload id", ports.ErrFailedPrecondition)
	}
	return strings.TrimSpace(result.UploadID), nil
}

func (s *MinIOObjectStore) ListParts(ctx context.Context, ref ports.ObjectRef, uploadID string) ([]ports.MultipartPart, error) {
	uploadID = strings.TrimSpace(uploadID)
	if uploadID == "" {
		return nil, fmt.Errorf("%w: upload id is required", ports.ErrInvalid)
	}
	var resultParts []ports.MultipartPart
	marker := 0
	for {
		target, err := s.multipartObject(ref)
		if err != nil {
			return nil, err
		}
		query := url.Values{}
		query.Set("uploadId", uploadID)
		query.Set("max-parts", strconv.Itoa(maxMultipartPartNumber))
		if marker > 0 {
			query.Set("part-number-marker", strconv.Itoa(marker))
		}
		target.RawQuery = canonicalQuery(query)
		req, err := s.newSignedRequest(ctx, http.MethodGet, target, nil, "")
		if err != nil {
			return nil, err
		}
		resp, err := s.doRequest(req)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(resp.Body)
		closeBody(resp.Body)
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, minIOHTTPError(resp.StatusCode, "list multipart parts")
		}
		var result listPartsResult
		if err := xml.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("decode multipart parts response: %w", err)
		}
		for _, part := range result.Parts {
			if part.PartNumber < 1 || part.PartNumber > maxMultipartPartNumber || part.Size < 0 || strings.TrimSpace(part.ETag) == "" {
				return nil, fmt.Errorf("%w: invalid multipart part response", ports.ErrFailedPrecondition)
			}
			resultParts = append(resultParts, ports.MultipartPart{Number: part.PartNumber, ETag: strings.Trim(part.ETag, `"`), Size: part.Size})
		}
		if !result.IsTruncated || result.NextPartNumberMarker <= marker {
			break
		}
		marker = result.NextPartNumberMarker
	}
	sort.Slice(resultParts, func(i, j int) bool { return resultParts[i].Number < resultParts[j].Number })
	return resultParts, nil
}

func (s *MinIOObjectStore) UploadPart(ctx context.Context, ref ports.ObjectRef, uploadID string, partNumber int, body io.Reader, size int64) (ports.MultipartPart, error) {
	if strings.TrimSpace(uploadID) == "" || body == nil || partNumber < 1 || partNumber > maxMultipartPartNumber || size <= 0 {
		return ports.MultipartPart{}, fmt.Errorf("%w: invalid multipart part", ports.ErrInvalid)
	}
	target, err := s.multipartObject(ref)
	if err != nil {
		return ports.MultipartPart{}, err
	}
	query := url.Values{}
	query.Set("partNumber", strconv.Itoa(partNumber))
	query.Set("uploadId", strings.TrimSpace(uploadID))
	target.RawQuery = canonicalQuery(query)
	bounded := &exactSizeReader{reader: body, expected: size}
	req, err := s.newSignedRequest(ctx, http.MethodPut, target, bounded, minIOUnsignedPayloadHash)
	if err != nil {
		return ports.MultipartPart{}, err
	}
	req.ContentLength = size
	resp, err := s.doRequest(req)
	if err != nil {
		if bodyErr := bounded.Err(); bodyErr != nil {
			return ports.MultipartPart{}, fmt.Errorf("%w: multipart part size mismatch: %v", ports.ErrInvalid, bodyErr)
		}
		return ports.MultipartPart{}, err
	}
	defer closeBody(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ports.MultipartPart{}, minIOHTTPError(resp.StatusCode, "upload multipart part")
	}
	if err := bounded.Err(); err != nil {
		return ports.MultipartPart{}, fmt.Errorf("%w: multipart part size mismatch: %v", ports.ErrInvalid, err)
	}
	etag := strings.Trim(strings.TrimSpace(resp.Header.Get("ETag")), `"`)
	if etag == "" {
		return ports.MultipartPart{}, fmt.Errorf("%w: MinIO returned an empty part etag", ports.ErrFailedPrecondition)
	}
	return ports.MultipartPart{Number: partNumber, ETag: etag, Size: size}, nil
}

func (s *MinIOObjectStore) CompleteMultipart(ctx context.Context, ref ports.ObjectRef, uploadID string, parts []ports.MultipartPart) (ports.ObjectMetadata, error) {
	if strings.TrimSpace(uploadID) == "" || len(parts) == 0 {
		return ports.ObjectMetadata{}, fmt.Errorf("%w: multipart upload and parts are required", ports.ErrInvalid)
	}
	ordered := append([]ports.MultipartPart(nil), parts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Number < ordered[j].Number })
	request := completeMultipartUploadRequest{Parts: make([]struct {
		PartNumber int    `xml:"PartNumber"`
		ETag       string `xml:"ETag"`
	}, len(ordered))}
	var total int64
	for index, part := range ordered {
		if part.Number < 1 || part.Number > maxMultipartPartNumber || part.Size <= 0 || strings.TrimSpace(part.ETag) == "" || (index > 0 && part.Number == ordered[index-1].Number) {
			return ports.ObjectMetadata{}, fmt.Errorf("%w: invalid multipart part list", ports.ErrInvalid)
		}
		request.Parts[index].PartNumber = part.Number
		request.Parts[index].ETag = `"` + strings.Trim(part.ETag, `"`) + `"`
		total += part.Size
	}
	body, err := xml.Marshal(request)
	if err != nil {
		return ports.ObjectMetadata{}, fmt.Errorf("encode multipart completion: %w", err)
	}
	target, err := s.multipartObject(ref)
	if err != nil {
		return ports.ObjectMetadata{}, err
	}
	query := url.Values{}
	query.Set("uploadId", strings.TrimSpace(uploadID))
	target.RawQuery = canonicalQuery(query)
	req, err := s.newSignedRequestWithHeaders(ctx, http.MethodPost, target, bytes.NewReader(body), sha256Hex(body), map[string]string{"Content-Type": "application/xml"})
	if err != nil {
		return ports.ObjectMetadata{}, err
	}
	req.ContentLength = int64(len(body))
	resp, err := s.doRequest(req)
	if err != nil {
		return ports.ObjectMetadata{}, err
	}
	defer closeBody(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ports.ObjectMetadata{}, minIOHTTPError(resp.StatusCode, "complete multipart upload")
	}
	var result completeMultipartUploadResult
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ports.ObjectMetadata{}, fmt.Errorf("decode multipart completion response: %w", err)
	}
	if strings.TrimSpace(result.ETag) == "" {
		return ports.ObjectMetadata{}, fmt.Errorf("%w: MinIO returned an empty completion etag", ports.ErrFailedPrecondition)
	}
	return ports.ObjectMetadata{Ref: ref, SizeBytes: total, UpdatedAt: s.now().UTC()}, nil
}

func (s *MinIOObjectStore) AbortMultipart(ctx context.Context, ref ports.ObjectRef, uploadID string) error {
	if strings.TrimSpace(uploadID) == "" {
		return fmt.Errorf("%w: upload id is required", ports.ErrInvalid)
	}
	target, err := s.multipartObject(ref)
	if err != nil {
		return err
	}
	query := url.Values{}
	query.Set("uploadId", strings.TrimSpace(uploadID))
	target.RawQuery = canonicalQuery(query)
	req, err := s.newSignedRequest(ctx, http.MethodDelete, target, nil, "")
	if err != nil {
		return err
	}
	resp, err := s.doRequest(req)
	if err != nil {
		return err
	}
	defer closeBody(resp.Body)
	if resp.StatusCode == http.StatusNotFound || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
		return nil
	}
	return minIOHTTPError(resp.StatusCode, "abort multipart upload")
}

var _ ports.MultipartObjectStore = (*MinIOObjectStore)(nil)
