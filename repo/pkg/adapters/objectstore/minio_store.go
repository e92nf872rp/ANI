package objectstore

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/adapters/resilience"
	"github.com/kubercloud/ani/pkg/ports"
)

const (
	defaultMinIORegion       = "us-east-1"
	minIOSigV4Service        = "s3"
	minIOUnsignedPayloadHash = "UNSIGNED-PAYLOAD"
)

var s3BucketNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

type MinIOObjectStoreConfig struct {
	Endpoint        string
	Endpoints       []string
	PublicEndpoint  string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Region          string
	Secure          bool
	BucketPrefix    string
	HTTPClient      *http.Client
	RequestTimeout  time.Duration
	Now             func() time.Time
}

type MinIOObjectStore struct {
	endpoint        *url.URL
	endpoints       []*url.URL
	publicEndpoint  *url.URL
	accessKeyID     string
	secretAccessKey string
	sessionToken    string
	region          string
	bucketPrefix    string
	client          *http.Client
	policy          resilience.Policy
	now             func() time.Time
}

var _ ports.ObjectStore = (*MinIOObjectStore)(nil)
var _ ports.ObjectStoreContentVerifier = (*MinIOObjectStore)(nil)

func NewMinIOObjectStore(config MinIOObjectStoreConfig) (*MinIOObjectStore, error) {
	endpoints, err := parseMinIOEndpoints(config.Endpoint, config.Endpoints, config.Secure)
	if err != nil {
		return nil, err
	}
	endpoint := endpoints[0]
	publicEndpoint := endpoint
	if strings.TrimSpace(config.PublicEndpoint) != "" {
		publicEndpoint, err = parseMinIOEndpoint(config.PublicEndpoint, config.Secure)
		if err != nil {
			return nil, err
		}
	}
	accessKeyID := strings.TrimSpace(config.AccessKeyID)
	secretAccessKey := strings.TrimSpace(config.SecretAccessKey)
	if accessKeyID == "" || secretAccessKey == "" {
		return nil, fmt.Errorf("%w: MinIO access key and secret key are required", ports.ErrInvalid)
	}
	region := strings.TrimSpace(config.Region)
	if region == "" {
		region = defaultMinIORegion
	}
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &MinIOObjectStore{
		endpoint:        endpoint,
		endpoints:       endpoints,
		publicEndpoint:  publicEndpoint,
		accessKeyID:     accessKeyID,
		secretAccessKey: secretAccessKey,
		sessionToken:    strings.TrimSpace(config.SessionToken),
		region:          region,
		bucketPrefix:    strings.TrimSpace(config.BucketPrefix),
		client:          client,
		policy:          resilience.Policy{Timeout: config.RequestTimeout},
		now:             now,
	}, nil
}

func (s *MinIOObjectStore) Health(ctx context.Context) error {
	target := *s.endpoint
	target.Path = "/"
	target.RawPath = ""
	target.RawQuery = ""
	req, err := s.newSignedRequest(ctx, http.MethodGet, target, nil, "")
	if err != nil {
		return err
	}
	resp, err := s.doRequest(req)
	if err != nil {
		return err
	}
	defer closeBody(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return minIOHTTPError(resp.StatusCode, "health")
	}
	return nil
}

func (s *MinIOObjectStore) EnsureBucket(ctx context.Context, class ports.BucketClass) error {
	bucket, err := s.bucketName(class)
	if err != nil {
		return err
	}
	headReq, err := s.newSignedRequest(ctx, http.MethodHead, s.bucketURL(bucket), nil, "")
	if err != nil {
		return err
	}
	headResp, err := s.doRequest(headReq)
	if err != nil {
		return err
	}
	closeBody(headResp.Body)
	if headResp.StatusCode >= 200 && headResp.StatusCode < 300 {
		return nil
	}
	if headResp.StatusCode != http.StatusNotFound {
		return minIOHTTPError(headResp.StatusCode, "check bucket")
	}

	putReq, err := s.newSignedRequest(ctx, http.MethodPut, s.bucketURL(bucket), nil, "")
	if err != nil {
		return err
	}
	putResp, err := s.doRequest(putReq)
	if err != nil {
		return err
	}
	defer closeBody(putResp.Body)
	if putResp.StatusCode == http.StatusConflict || (putResp.StatusCode >= 200 && putResp.StatusCode < 300) {
		return nil
	}
	return minIOHTTPError(putResp.StatusCode, "create bucket")
}

// BucketUsage aggregates live object count and size from the S3-compatible
// backend via ListObjectsV2, scoped to the tenant prefix so shared buckets
// only report the requesting tenant's objects.
func (s *MinIOObjectStore) BucketUsage(ctx context.Context, class ports.BucketClass, tenantID string) (ports.BucketUsage, error) {
	if strings.TrimSpace(tenantID) == "" {
		return ports.BucketUsage{}, fmt.Errorf("%w: tenant_id is required for bucket usage", ports.ErrInvalid)
	}
	bucket, err := s.bucketName(class)
	if err != nil {
		return ports.BucketUsage{}, err
	}
	var usage ports.BucketUsage
	continuationToken := ""
	for {
		target := s.bucketURL(bucket)
		query := url.Values{}
		query.Set("list-type", "2")
		query.Set("max-keys", "1000")
		query.Set("prefix", strings.Trim(tenantID, "/")+"/")
		if continuationToken != "" {
			query.Set("continuation-token", continuationToken)
		}
		target.RawQuery = query.Encode()
		req, err := s.newSignedRequest(ctx, http.MethodGet, target, nil, "")
		if err != nil {
			return ports.BucketUsage{}, err
		}
		resp, err := s.doRequest(req)
		if err != nil {
			return ports.BucketUsage{}, err
		}
		body, readErr := io.ReadAll(resp.Body)
		closeBody(resp.Body)
		if readErr != nil {
			return ports.BucketUsage{}, readErr
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return ports.BucketUsage{}, minIOHTTPError(resp.StatusCode, "list bucket usage")
		}
		var result minIOListBucketResult
		if err := xml.Unmarshal(body, &result); err != nil {
			return ports.BucketUsage{}, fmt.Errorf("decode list objects response: %w", err)
		}
		for _, object := range result.Contents {
			usage.ObjectCount++
			usage.SizeBytes += object.Size
		}
		if !result.IsTruncated || strings.TrimSpace(result.NextContinuationToken) == "" {
			return usage, nil
		}
		continuationToken = result.NextContinuationToken
	}
}

type minIOListBucketResult struct {
	XMLName               xml.Name `xml:"ListBucketResult"`
	IsTruncated           bool     `xml:"IsTruncated"`
	NextContinuationToken string   `xml:"NextContinuationToken"`
	Contents              []struct {
		Size int64 `xml:"Size"`
	} `xml:"Contents"`
}

func (s *MinIOObjectStore) PutObject(ctx context.Context, input ports.PutObjectInput) (ports.ObjectMetadata, error) {
	if input.Body == nil {
		return ports.ObjectMetadata{}, fmt.Errorf("%w: object body is required", ports.ErrInvalid)
	}
	if input.SizeBytes < 0 {
		return ports.ObjectMetadata{}, fmt.Errorf("%w: object size must not be negative", ports.ErrInvalid)
	}
	target, err := s.objectURL(input.Ref)
	if err != nil {
		return ports.ObjectMetadata{}, err
	}
	checksum := strings.TrimSpace(input.Checksum)
	contentHash := minIOUnsignedPayloadHash
	expectedChecksum := ""
	requestHeaders := map[string]string(nil)
	if checksum != "" {
		canonicalChecksum, ok := canonicalSHA256Checksum(checksum)
		if !ok {
			return ports.ObjectMetadata{}, fmt.Errorf("%w: object checksum must be a SHA-256 digest", ports.ErrInvalid)
		}
		expectedChecksum = canonicalChecksum
		contentHash = canonicalChecksum
		// Persist the trusted digest as user metadata. It is covered by the
		// SigV4 signature so an object store or intermediary cannot silently
		// replace the value while retaining a successful upload response.
		requestHeaders = map[string]string{"x-amz-meta-sha256": canonicalChecksum}
	}
	var uploadBody io.Reader
	var boundedBody *exactSizeReader
	var countedBody *countingReader
	if input.SizeBytes > 0 {
		boundedBody = &exactSizeReader{reader: input.Body, expected: input.SizeBytes}
		uploadBody = boundedBody
	} else {
		// SizeBytes=0 was historically an unspecified size in this port. Keep
		// that compatibility while still streaming and counting the body; the
		// remote-import worker always supplies a positive archive size and is
		// therefore strictly bounded by exactSizeReader.
		countedBody = &countingReader{reader: input.Body}
		uploadBody = countedBody
	}
	var digest hash.Hash
	if expectedChecksum != "" {
		digest = sha256.New()
		// SigV4 metadata binds the declared value, while this tee computes an
		// independent proof of the bytes actually consumed by the HTTP client.
		// A caller cannot make a mismatched body look valid by forging metadata.
		uploadBody = io.TeeReader(uploadBody, digest)
	}
	req, err := s.newSignedRequestWithHeaders(ctx, http.MethodPut, target, uploadBody, contentHash, requestHeaders)
	if err != nil {
		return ports.ObjectMetadata{}, err
	}
	if input.SizeBytes > 0 {
		req.ContentLength = input.SizeBytes
	}
	if contentType := strings.TrimSpace(input.ContentType); contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := s.doRequest(req)
	if err != nil {
		return ports.ObjectMetadata{}, err
	}
	defer closeBody(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ports.ObjectMetadata{}, minIOHTTPError(resp.StatusCode, "put object")
	}
	if boundedBody != nil {
		if err := boundedBody.Err(); err != nil {
			return ports.ObjectMetadata{}, fmt.Errorf("%w: object body size mismatch: %v", ports.ErrInvalid, err)
		}
	}
	if digest != nil && hex.EncodeToString(digest.Sum(nil)) != expectedChecksum {
		// Do not leave an object with a forged checksum metadata value behind.
		// Cleanup is best effort; the operation remains failed either way and
		// no model version may be registered from this object.
		_ = s.DeleteObject(ctx, input.Ref)
		return ports.ObjectMetadata{}, fmt.Errorf("%w: object checksum does not match declared SHA-256", ports.ErrInvalid)
	}
	uploadedSize := input.SizeBytes
	if countedBody != nil {
		uploadedSize = countedBody.read
	}
	return ports.ObjectMetadata{
		Ref:         input.Ref,
		ContentType: input.ContentType,
		SizeBytes:   uploadedSize,
		Checksum:    checksum,
		UpdatedAt:   s.now().UTC(),
	}, nil
}

// VerifyObject streams an object back from MinIO and verifies its actual
// bytes. This is required after a presigned PUT because a signed metadata
// header authenticates the declared value, not the payload that a client sent.
func (s *MinIOObjectStore) VerifyObject(ctx context.Context, ref ports.ObjectRef, expectedSize int64, expectedChecksum string) error {
	if expectedSize <= 0 {
		return fmt.Errorf("%w: expected object size must be positive", ports.ErrInvalid)
	}
	expected, ok := canonicalSHA256Checksum(expectedChecksum)
	if !ok {
		return fmt.Errorf("%w: expected object checksum must be a SHA-256 digest", ports.ErrInvalid)
	}
	target, err := s.objectURL(ref)
	if err != nil {
		return err
	}
	req, err := s.newSignedRequest(ctx, http.MethodGet, target, nil, "")
	if err != nil {
		return err
	}
	resp, err := s.doRequest(req)
	if err != nil {
		return err
	}
	defer closeBody(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return ports.ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return minIOHTTPError(resp.StatusCode, "verify object")
	}
	if resp.ContentLength >= 0 && resp.ContentLength != expectedSize {
		return fmt.Errorf("%w: object size does not match expected size", ports.ErrInvalid)
	}
	digest := sha256.New()
	read, err := io.CopyN(digest, resp.Body, expectedSize+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: read object for verification", ports.ErrInvalid)
	}
	if read != expectedSize {
		return fmt.Errorf("%w: object size does not match expected size", ports.ErrInvalid)
	}
	// CopyN can return exactly N without observing EOF, so probe one byte to
	// reject a payload longer than the declared object size.
	var extra [1]byte
	if n, probeErr := resp.Body.Read(extra[:]); n != 0 || (probeErr != nil && !errors.Is(probeErr, io.EOF)) {
		return fmt.Errorf("%w: object size does not match expected size", ports.ErrInvalid)
	}
	if hex.EncodeToString(digest.Sum(nil)) != expected {
		return fmt.Errorf("%w: object checksum does not match expected SHA-256", ports.ErrInvalid)
	}
	return nil
}

type countingReader struct {
	reader io.Reader
	read   int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.read += int64(n)
	return n, err
}

// exactSizeReader bounds a streamed upload to the declared size and records
// short/long reads without buffering the object in memory. The transport gets
// at most expected bytes; an extra byte is consumed only to detect overflow.
type exactSizeReader struct {
	reader   io.Reader
	expected int64
	read     int64
	err      error
	checked  bool
}

func (r *exactSizeReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if r.read >= r.expected {
		if !r.checked {
			r.checked = true
			var one [1]byte
			n, err := r.reader.Read(one[:])
			if n > 0 {
				r.err = fmt.Errorf("body exceeds declared size %d", r.expected)
				return 0, r.err
			}
			if err != nil && !errors.Is(err, io.EOF) {
				r.err = err
				return 0, r.err
			}
		}
		return 0, io.EOF
	}
	remaining := r.expected - r.read
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := r.reader.Read(p)
	if n > 0 {
		r.read += int64(n)
	}
	if err != nil && !errors.Is(err, io.EOF) && r.read < r.expected {
		r.err = err
		return n, err
	}
	if errors.Is(err, io.EOF) && r.read < r.expected {
		r.err = fmt.Errorf("body ended after %d bytes, want %d", r.read, r.expected)
		return n, r.err
	}
	return n, nil
}

func (r *exactSizeReader) Err() error {
	if r.err != nil {
		return r.err
	}
	if r.read != r.expected {
		return fmt.Errorf("body read %d bytes, want %d", r.read, r.expected)
	}
	return nil
}

func (s *MinIOObjectStore) GetObject(ctx context.Context, ref ports.ObjectRef) (io.ReadCloser, ports.ObjectMetadata, error) {
	target, err := s.objectURL(ref)
	if err != nil {
		return nil, ports.ObjectMetadata{}, err
	}
	req, err := s.newSignedRequest(ctx, http.MethodGet, target, nil, "")
	if err != nil {
		return nil, ports.ObjectMetadata{}, err
	}
	resp, err := s.doRequest(req)
	if err != nil {
		return nil, ports.ObjectMetadata{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		closeBody(resp.Body)
		return nil, ports.ObjectMetadata{}, minIOHTTPError(resp.StatusCode, "get object")
	}
	return resp.Body, s.metadataFromResponse(ref, resp), nil
}

func (s *MinIOObjectStore) DeleteObject(ctx context.Context, ref ports.ObjectRef) error {
	target, err := s.objectURL(ref)
	if err != nil {
		return err
	}
	req, err := s.newSignedRequest(ctx, http.MethodDelete, target, nil, "")
	if err != nil {
		return err
	}
	resp, err := s.doRequest(req)
	if err != nil {
		return err
	}
	defer closeBody(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return ports.ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return minIOHTTPError(resp.StatusCode, "delete object")
	}
	return nil
}

func (s *MinIOObjectStore) StatObject(ctx context.Context, ref ports.ObjectRef) (ports.ObjectMetadata, error) {
	target, err := s.objectURL(ref)
	if err != nil {
		return ports.ObjectMetadata{}, err
	}
	req, err := s.newSignedRequestWithHeaders(ctx, http.MethodHead, target, nil, "", map[string]string{"x-amz-checksum-mode": "ENABLED"})
	if err != nil {
		return ports.ObjectMetadata{}, err
	}
	// S3 exposes the object checksum on HEAD only when checksum mode is
	// requested. The request helper includes this header in SigV4
	// SignedHeaders so the checksum negotiation cannot be stripped or altered.
	resp, err := s.doRequest(req)
	if err != nil {
		return ports.ObjectMetadata{}, err
	}
	defer closeBody(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return ports.ObjectMetadata{}, ports.ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ports.ObjectMetadata{}, minIOHTTPError(resp.StatusCode, "stat object")
	}
	metadata := s.metadataFromResponse(ref, resp)
	// A HEAD response without a real SHA-256 checksum must not expose ETag as
	// a model checksum: ETag may be MD5 or a multipart composite, and an
	// attacker-controlled 64-hex ETag must not satisfy the model proof.
	metadata.Checksum = responseSHA256Checksum(resp.Header)
	return metadata, nil
}

func (s *MinIOObjectStore) SignedUploadURL(ctx context.Context, ref ports.ObjectRef, ttl time.Duration) (ports.SignedURL, error) {
	return s.presignWithEndpoint(ctx, http.MethodPut, ref, ttl, s.publicEndpoint)
}

func (s *MinIOObjectStore) SignedUploadURLWithHeaders(ctx context.Context, ref ports.ObjectRef, ttl time.Duration, headers map[string]string) (ports.SignedURL, error) {
	return s.presignWithHeadersAndEndpoint(ctx, http.MethodPut, ref, ttl, s.publicEndpoint, headers)
}

func (s *MinIOObjectStore) SignedDownloadURL(ctx context.Context, ref ports.ObjectRef, ttl time.Duration) (ports.SignedURL, error) {
	return s.presignWithEndpoint(ctx, http.MethodGet, ref, ttl, s.endpoint)
}

func (s *MinIOObjectStore) presignWithEndpoint(ctx context.Context, method string, ref ports.ObjectRef, ttl time.Duration, endpoint *url.URL) (ports.SignedURL, error) {
	return s.presignWithHeadersAndEndpoint(ctx, method, ref, ttl, endpoint, nil)
}

func (s *MinIOObjectStore) presignWithHeadersAndEndpoint(ctx context.Context, method string, ref ports.ObjectRef, ttl time.Duration, endpoint *url.URL, headers map[string]string) (ports.SignedURL, error) {
	if err := ctx.Err(); err != nil {
		return ports.SignedURL{}, err
	}
	if ttl <= 0 {
		return ports.SignedURL{}, fmt.Errorf("%w: signed URL ttl must be positive", ports.ErrInvalid)
	}
	target, err := s.objectURLForEndpoint(ref, endpoint)
	if err != nil {
		return ports.SignedURL{}, err
	}
	normalizedHeaders, signedHeaderNames, clientHeaders, err := canonicalPresignHeaders(target.Host, headers)
	if err != nil {
		return ports.SignedURL{}, err
	}
	now := s.now().UTC()
	expirySeconds := int(ttl.Seconds())
	query := target.Query()
	query.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	query.Set("X-Amz-Credential", s.credentialScope(now))
	query.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	query.Set("X-Amz-Expires", strconv.Itoa(expirySeconds))
	query.Set("X-Amz-SignedHeaders", strings.Join(signedHeaderNames, ";"))
	if s.sessionToken != "" {
		query.Set("X-Amz-Security-Token", s.sessionToken)
	}
	target.RawQuery = canonicalQuery(query)
	canonicalRequest := strings.Join([]string{method, target.EscapedPath(), target.RawQuery, canonicalPresignHeaderBlock(target.Host, normalizedHeaders), strings.Join(signedHeaderNames, ";"), minIOUnsignedPayloadHash}, "\n")
	signature := s.signature(now, canonicalRequest)
	query.Set("X-Amz-Signature", signature)
	target.RawQuery = canonicalQuery(query)
	return ports.SignedURL{
		URL:       target.String(),
		ExpiresAt: now.Add(ttl),
		Headers:   clientHeaders,
	}, nil
}

func canonicalPresignHeaders(host string, headers map[string]string) (map[string]string, []string, map[string]string, error) {
	normalized := map[string]string{"host": host}
	for name, value := range headers {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || strings.ContainsAny(name, " \t\r\n:") || name == "host" {
			return nil, nil, nil, fmt.Errorf("%w: invalid signed upload header", ports.ErrInvalid)
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, nil, nil, fmt.Errorf("%w: invalid signed upload header value", ports.ErrInvalid)
		}
		if _, exists := normalized[name]; exists {
			return nil, nil, nil, fmt.Errorf("%w: duplicate signed upload header", ports.ErrInvalid)
		}
		normalized[name] = strings.TrimSpace(value)
	}
	names := make([]string, 0, len(normalized))
	for name := range normalized {
		names = append(names, name)
	}
	sort.Strings(names)
	// The returned map is client-facing; do not expose host or any mutable
	// caller-owned map. Host is supplied by the URL itself.
	clientHeaders := make(map[string]string, len(normalized)-1)
	for _, name := range names {
		if name != "host" {
			clientHeaders[name] = normalized[name]
		}
	}
	return normalized, names, clientHeaders, nil
}

func canonicalPresignHeaderBlock(host string, headers map[string]string) string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var block strings.Builder
	for _, name := range names {
		value := headers[name]
		if name == "host" {
			value = host
		}
		block.WriteString(name)
		block.WriteByte(':')
		block.WriteString(strings.TrimSpace(value))
		block.WriteByte('\n')
	}
	return block.String()
}

func (s *MinIOObjectStore) newSignedRequest(ctx context.Context, method string, target url.URL, body io.Reader, payloadHash string) (*http.Request, error) {
	return s.newSignedRequestWithHeaders(ctx, method, target, body, payloadHash, nil)
}

func (s *MinIOObjectStore) newSignedRequestWithHeaders(ctx context.Context, method string, target url.URL, body io.Reader, payloadHash string, headers map[string]string) (*http.Request, error) {
	if payloadHash == "" {
		payloadHash = sha256Hex(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	req.Header.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if s.sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", s.sessionToken)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	s.signRequest(req, now, payloadHash)
	return req, nil
}

func (s *MinIOObjectStore) doRequest(req *http.Request) (*http.Response, error) {
	var lastErr error
	for index, endpoint := range s.endpoints {
		candidate, err := s.requestForEndpoint(req, endpoint)
		if err != nil {
			return nil, err
		}
		resp, err := s.doRequestOnce(candidate)
		if err != nil {
			lastErr = err
			if index < len(s.endpoints)-1 && resilience.Retryable(err) {
				continue
			}
			return nil, err
		}
		if index < len(s.endpoints)-1 && minIORetryableStatus(resp.StatusCode) {
			// A streamed body is not replayable unless the caller supplied a
			// GetBody function. Never retry a consumed upload against another
			// endpoint: doing so would silently write a truncated object.
			if req.Body != nil && req.GetBody == nil {
				return resp, nil
			}
			lastErr = minIOHTTPError(resp.StatusCode, strings.TrimSpace(req.Method+" "+req.URL.Path))
			closeBody(resp.Body)
			continue
		}
		return resp, nil
	}
	return nil, lastErr
}

func (s *MinIOObjectStore) doRequestOnce(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	err := resilience.Do(req.Context(), s.policy, func(callCtx context.Context) error {
		var err error
		resp, err = s.client.Do(req.Clone(callCtx))
		return err
	})
	return resp, err
}

func (s *MinIOObjectStore) requestForEndpoint(req *http.Request, endpoint *url.URL) (*http.Request, error) {
	candidate := req.Clone(req.Context())
	target := *endpoint
	target.Path = req.URL.Path
	target.RawPath = req.URL.RawPath
	target.RawQuery = req.URL.RawQuery
	candidate.URL = &target
	candidate.Host = target.Host
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		candidate.Body = body
	}
	payloadHash := candidate.Header.Get("X-Amz-Content-Sha256")
	now := s.now().UTC()
	candidate.Header.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	candidate.Header.Del("Authorization")
	s.signRequest(candidate, now, payloadHash)
	return candidate, nil
}

func (s *MinIOObjectStore) signRequest(req *http.Request, now time.Time, payloadHash string) {
	signedHeaders := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	seen := map[string]struct{}{}
	for _, header := range signedHeaders {
		seen[header] = struct{}{}
	}
	for name, values := range req.Header {
		lower := strings.ToLower(strings.TrimSpace(name))
		if !strings.HasPrefix(lower, "x-amz-") || len(values) == 0 || strings.TrimSpace(values[0]) == "" {
			continue
		}
		if _, ok := seen[lower]; ok {
			continue
		}
		signedHeaders = append(signedHeaders, lower)
		seen[lower] = struct{}{}
	}
	sort.Strings(signedHeaders)

	var canonicalHeaders strings.Builder
	for _, header := range signedHeaders {
		value := req.Host
		if value == "" && req.URL != nil {
			value = req.URL.Host
		}
		if header != "host" {
			value = req.Header.Get(header)
		}
		canonicalHeaders.WriteString(header)
		canonicalHeaders.WriteByte(':')
		canonicalHeaders.WriteString(strings.TrimSpace(value))
		canonicalHeaders.WriteByte('\n')
	}
	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		canonicalQuery(req.URL.Query()),
		canonicalHeaders.String(),
		strings.Join(signedHeaders, ";"),
		payloadHash,
	}, "\n")
	authorization := "AWS4-HMAC-SHA256 Credential=" + s.credentialScope(now) +
		", SignedHeaders=" + strings.Join(signedHeaders, ";") +
		", Signature=" + s.signature(now, canonicalRequest)
	req.Header.Set("Authorization", authorization)
}

func (s *MinIOObjectStore) signature(now time.Time, canonicalRequest string) string {
	date := now.Format("20060102")
	scope := date + "/" + s.region + "/" + minIOSigV4Service + "/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		now.Format("20060102T150405Z"),
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	key := hmacSHA256([]byte("AWS4"+s.secretAccessKey), date)
	key = hmacSHA256(key, s.region)
	key = hmacSHA256(key, minIOSigV4Service)
	key = hmacSHA256(key, "aws4_request")
	return hex.EncodeToString(hmacSHA256(key, stringToSign))
}

func (s *MinIOObjectStore) credentialScope(now time.Time) string {
	return s.accessKeyID + "/" + now.Format("20060102") + "/" + s.region + "/" + minIOSigV4Service + "/aws4_request"
}

func (s *MinIOObjectStore) bucketURL(bucket string) url.URL {
	target := *s.endpoint
	target.Path = "/" + bucket
	target.RawPath = ""
	target.RawQuery = ""
	return target
}

func (s *MinIOObjectStore) objectURL(ref ports.ObjectRef) (url.URL, error) {
	return s.objectURLForEndpoint(ref, s.endpoint)
}

func (s *MinIOObjectStore) objectURLForEndpoint(ref ports.ObjectRef, endpoint *url.URL) (url.URL, error) {
	bucket, err := s.bucketName(ref.BucketClass)
	if err != nil {
		return url.URL{}, err
	}
	tenantID := strings.TrimSpace(ref.TenantID)
	objectKey := strings.TrimSpace(ref.ObjectKey)
	if tenantID == "" || objectKey == "" {
		return url.URL{}, fmt.Errorf("%w: tenant_id and object key are required", ports.ErrInvalid)
	}
	target := *endpoint
	target.Path = "/" + bucket + "/" + strings.Trim(tenantID, "/") + "/" + strings.TrimLeft(objectKey, "/")
	target.RawPath = ""
	target.RawQuery = ""
	return target, nil
}

func (s *MinIOObjectStore) bucketName(class ports.BucketClass) (string, error) {
	name := strings.TrimSpace(s.bucketPrefix + string(class))
	if name == "" {
		return "", fmt.Errorf("%w: bucket class is required", ports.ErrInvalid)
	}
	if !s3BucketNamePattern.MatchString(name) || strings.Contains(name, "..") || strings.Contains(name, ".-") || strings.Contains(name, "-.") {
		return "", fmt.Errorf("%w: bucket name %q is not S3 compatible", ports.ErrInvalid, name)
	}
	return name, nil
}

func (s *MinIOObjectStore) metadataFromResponse(ref ports.ObjectRef, resp *http.Response) ports.ObjectMetadata {
	updatedAt := s.now().UTC()
	if lastModified := strings.TrimSpace(resp.Header.Get("Last-Modified")); lastModified != "" {
		if parsed, err := http.ParseTime(lastModified); err == nil {
			updatedAt = parsed.UTC()
		}
	}
	checksum := responseSHA256Checksum(resp.Header)
	if checksum == "" && !hasSHA256ChecksumHeader(resp.Header) {
		checksum = strings.Trim(resp.Header.Get("ETag"), `"`)
	}
	return ports.ObjectMetadata{
		Ref:         ref,
		ContentType: resp.Header.Get("Content-Type"),
		SizeBytes:   resp.ContentLength,
		Checksum:    checksum,
		UpdatedAt:   updatedAt,
	}
}

// responseSHA256Checksum returns the canonical hex SHA-256 for S3 checksum
// responses. The standard x-amz-checksum-sha256 header is base64 encoded;
// MinIO deployments may instead expose the same value as user metadata. A
// 64-character hex value is accepted for that metadata form.
func responseSHA256Checksum(headers http.Header) string {
	for _, name := range []string{"X-Amz-Checksum-Sha256", "X-Amz-Meta-Checksum-Sha256", "X-Amz-Meta-Sha256"} {
		raw := strings.TrimSpace(headers.Get(name))
		if raw == "" {
			continue
		}
		// The model upload contract uses the human-readable sha256:<hex>
		// representation for x-amz-meta-sha256. Normalize that form before
		// handling standard base64 or bare-hex checksum headers.
		if strings.HasPrefix(strings.ToLower(raw), "sha256:") {
			raw = strings.TrimSpace(raw[len("sha256:"):])
		}
		if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil && len(decoded) == sha256.Size {
			return hex.EncodeToString(decoded)
		}
		if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) == sha256.Size {
			return hex.EncodeToString(decoded)
		}
	}
	return ""
}

func canonicalSHA256Checksum(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(raw), "sha256:") {
		raw = strings.TrimSpace(raw[len("sha256:"):])
	}
	decoded, err := hex.DecodeString(raw)
	if err != nil || len(decoded) != sha256.Size {
		return "", false
	}
	return hex.EncodeToString(decoded), true
}

func hasSHA256ChecksumHeader(headers http.Header) bool {
	for _, name := range []string{"X-Amz-Checksum-Sha256", "X-Amz-Meta-Checksum-Sha256", "X-Amz-Meta-Sha256"} {
		if strings.TrimSpace(headers.Get(name)) != "" {
			return true
		}
	}
	return false
}

func parseMinIOEndpoint(raw string, secure bool) (*url.URL, error) {
	endpoint := strings.TrimSpace(raw)
	if endpoint == "" {
		return nil, fmt.Errorf("%w: MinIO endpoint is required", ports.ErrInvalid)
	}
	if !strings.Contains(endpoint, "://") {
		scheme := "http"
		if secure {
			scheme = "https"
		}
		endpoint = scheme + "://" + endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid MinIO endpoint: %v", ports.ErrInvalid, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("%w: MinIO endpoint scheme must be http or https", ports.ErrInvalid)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("%w: MinIO endpoint host is required", ports.ErrInvalid)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return nil, fmt.Errorf("%w: MinIO endpoint must not include a path", ports.ErrInvalid)
	}
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func parseMinIOEndpoints(primary string, values []string, secure bool) ([]*url.URL, error) {
	rawValues := append([]string{}, values...)
	if strings.TrimSpace(primary) != "" {
		rawValues = append([]string{primary}, rawValues...)
	}
	if len(rawValues) == 0 {
		return nil, fmt.Errorf("%w: MinIO endpoint is required", ports.ErrInvalid)
	}
	endpoints := make([]*url.URL, 0, len(rawValues))
	seen := map[string]struct{}{}
	for _, raw := range rawValues {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		parsed, err := parseMinIOEndpoint(trimmed, secure)
		if err != nil {
			return nil, err
		}
		key := parsed.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		endpoints = append(endpoints, parsed)
	}
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("%w: MinIO endpoint is required", ports.ErrInvalid)
	}
	return endpoints, nil
}

func minIOHTTPError(statusCode int, operation string) error {
	switch statusCode {
	case http.StatusNotFound:
		return ports.ErrNotFound
	case http.StatusConflict:
		return ports.ErrConflict
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: MinIO %s returned HTTP %d", ports.ErrFailedPrecondition, operation, statusCode)
	default:
		return fmt.Errorf("MinIO %s returned HTTP %d", operation, statusCode)
	}
}

func minIORetryableStatus(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests || statusCode >= 500
}

func canonicalQuery(values url.Values) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var parts []string
	for _, key := range keys {
		items := append([]string(nil), values[key]...)
		sort.Strings(items)
		for _, value := range items {
			parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(value))
		}
	}
	return strings.ReplaceAll(strings.Join(parts, "&"), "+", "%20")
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(data))
	return mac.Sum(nil)
}

func closeBody(body io.Closer) {
	if body != nil {
		_ = body.Close()
	}
}
