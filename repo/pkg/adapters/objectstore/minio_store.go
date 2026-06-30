package objectstore

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

func (s *MinIOObjectStore) PutObject(ctx context.Context, input ports.PutObjectInput) (ports.ObjectMetadata, error) {
	if input.Body == nil {
		return ports.ObjectMetadata{}, fmt.Errorf("%w: object body is required", ports.ErrInvalid)
	}
	data, err := io.ReadAll(input.Body)
	if err != nil {
		return ports.ObjectMetadata{}, err
	}
	target, err := s.objectURL(input.Ref)
	if err != nil {
		return ports.ObjectMetadata{}, err
	}
	contentHash := sha256Hex(data)
	req, err := s.newSignedRequest(ctx, http.MethodPut, target, bytes.NewReader(data), contentHash)
	if err != nil {
		return ports.ObjectMetadata{}, err
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
	return ports.ObjectMetadata{
		Ref:         input.Ref,
		ContentType: input.ContentType,
		SizeBytes:   int64(len(data)),
		Checksum:    input.Checksum,
		UpdatedAt:   s.now().UTC(),
	}, nil
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
	req, err := s.newSignedRequest(ctx, http.MethodHead, target, nil, "")
	if err != nil {
		return ports.ObjectMetadata{}, err
	}
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
	return s.metadataFromResponse(ref, resp), nil
}

func (s *MinIOObjectStore) SignedUploadURL(ctx context.Context, ref ports.ObjectRef, ttl time.Duration) (ports.SignedURL, error) {
	return s.presign(ctx, http.MethodPut, ref, ttl)
}

func (s *MinIOObjectStore) SignedDownloadURL(ctx context.Context, ref ports.ObjectRef, ttl time.Duration) (ports.SignedURL, error) {
	return s.presign(ctx, http.MethodGet, ref, ttl)
}

func (s *MinIOObjectStore) PublicObjectURL(ctx context.Context, ref ports.ObjectRef) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	target, err := s.objectURLForEndpoint(ref, s.publicEndpoint)
	if err != nil {
		return "", err
	}
	return target.String(), nil
}

func (s *MinIOObjectStore) presign(ctx context.Context, method string, ref ports.ObjectRef, ttl time.Duration) (ports.SignedURL, error) {
	if err := ctx.Err(); err != nil {
		return ports.SignedURL{}, err
	}
	if ttl <= 0 {
		return ports.SignedURL{}, fmt.Errorf("%w: signed URL ttl must be positive", ports.ErrInvalid)
	}
	target, err := s.objectURLForEndpoint(ref, s.publicEndpoint)
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
	query.Set("X-Amz-SignedHeaders", "host")
	if s.sessionToken != "" {
		query.Set("X-Amz-Security-Token", s.sessionToken)
	}
	target.RawQuery = canonicalQuery(query)
	canonicalRequest := strings.Join([]string{
		method,
		target.EscapedPath(),
		target.RawQuery,
		"host:" + target.Host + "\n",
		"host",
		minIOUnsignedPayloadHash,
	}, "\n")
	signature := s.signature(now, canonicalRequest)
	query.Set("X-Amz-Signature", signature)
	target.RawQuery = canonicalQuery(query)
	return ports.SignedURL{
		URL:       target.String(),
		ExpiresAt: now.Add(ttl),
		Headers:   map[string]string{},
	}, nil
}

func (s *MinIOObjectStore) newSignedRequest(ctx context.Context, method string, target url.URL, body io.Reader, payloadHash string) (*http.Request, error) {
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
	if s.sessionToken != "" {
		signedHeaders = append(signedHeaders, "x-amz-security-token")
	}
	sort.Strings(signedHeaders)

	var canonicalHeaders strings.Builder
	for _, header := range signedHeaders {
		value := req.Host
		if value == "" && req.URL != nil {
			value = req.URL.Host
		}
		if header != "host" {
			value = req.Header.Get(http.CanonicalHeaderKey(header))
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
	return ports.ObjectMetadata{
		Ref:         ref,
		ContentType: resp.Header.Get("Content-Type"),
		SizeBytes:   resp.ContentLength,
		Checksum:    strings.Trim(resp.Header.Get("ETag"), `"`),
		UpdatedAt:   updatedAt,
	}
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
