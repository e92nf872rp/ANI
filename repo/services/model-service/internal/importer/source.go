// Package importer contains the public, network-facing part of model import.
//
// Sources deliberately expose only repository metadata and byte streams.  No
// credential or source URL supplied by a caller is accepted by this package;
// the two adapters build URLs from an allowlisted public host.
package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ErrSourceRevisionRejected marks a deterministic revision policy failure.
// Worker callers persist these failures as terminal instead of redelivering a
// request that cannot succeed (for example, a provider response without an
// immutable commit). The wrapped error never contains provider response
// bodies, URLs, or credentials.
var ErrSourceRevisionRejected = errors.New("source revision rejected")

func sourceRevisionRejected(message string) error {
	return fmt.Errorf("%w: %s", ErrSourceRevisionRejected, message)
}

// sourceHTTPError keeps the status class available to the worker without
// exposing the provider's URL or response body through an error string.
// Redirect is tracked separately because an API redirect is a policy failure,
// while a transient 429/5xx should remain retryable for revision resolution.
type sourceHTTPError struct {
	status   int
	redirect bool
}

func (e *sourceHTTPError) Error() string {
	if e != nil && e.redirect {
		return "source redirect rejected"
	}
	return "source returned non-success status"
}

// classifyRevisionLookupError converts an HTTP/policy response that will not
// become valid by retrying into the stable revision-rejected sentinel. Rate
// limiting, request timeouts and server errors are deliberately left as the
// original transient error so the worker can redeliver them.
func classifyRevisionLookupError(err error) error {
	if err == nil {
		return nil
	}
	var httpErr *sourceHTTPError
	if !errors.As(err, &httpErr) {
		return err
	}
	if httpErr.redirect || (httpErr.status >= http.StatusBadRequest &&
		httpErr.status < http.StatusInternalServerError &&
		httpErr.status != http.StatusRequestTimeout &&
		httpErr.status != http.StatusTooManyRequests &&
		httpErr.status != http.StatusTooEarly) {
		return sourceRevisionRejected("source revision lookup was rejected")
	}
	return err
}

const (
	maxSourceResponseBytes = int64(16 << 20)
	maxSourceFileBytes     = int64(math.MaxInt64)
	sourceHTTPTimeout      = 30 * time.Minute
)

// Repository identifies a public repository revision. A caller may supply a
// mutable branch/tag initially; a worker resolves it through RevisionResolver
// and persists the immutable commit before it invokes List/Open. It does not
// contain an access token: this first version imports public repositories only.
type Repository struct {
	Source   string
	RepoID   string
	Revision string
}

// RemoteFile is a regular file returned by a source tree API.  Type may be
// empty for APIs that omit the field; known directory/link values are always
// rejected or filtered before a file reaches an archive builder.
type RemoteFile struct {
	Path string
	Size int64
	OID  string
	Type string
}

// Source resolves a public repository tree and opens individual immutable
// files. API/tree requests must not follow redirects; a source may follow a
// bounded, explicit HTTPS CDN redirect chain for immutable file bytes. No
// implementation accepts or forwards caller credentials.
type Source interface {
	List(context.Context, Repository) ([]RemoteFile, error)
	Open(context.Context, Repository, string) (io.ReadCloser, int64, error)
}

// RangeSource opens one immutable byte range without buffering the response.
// Implementations require a fixed revision and reject providers that ignore a
// partial request. The returned reader reports truncation or extra bytes while
// it is consumed, so callers cannot silently resume from a corrupt payload.
type RangeSource interface {
	OpenRange(context.Context, Repository, string, int64, int64) (io.ReadCloser, error)
}

// RevisionResolver is implemented by sources whose branch/tag API can be
// resolved to an immutable snapshot. Workers persist the returned revision
// before calling List/Open, so redelivery uses the same snapshot.
type RevisionResolver interface {
	ResolveRevision(context.Context, Repository) (string, error)
}

// ValidateRepository applies the common source/repository/revision safety
// rules. Source-specific adapters call it before constructing any URL.
func ValidateRepository(repository Repository) error {
	if repository.Source != "huggingface" && repository.Source != "modelscope" {
		return errors.New("unsupported model source")
	}
	if err := validatePath(repository.RepoID, true, "repository"); err != nil {
		return err
	}
	if err := validatePath(repository.Revision, true, "revision"); err != nil {
		return err
	}
	return nil
}

func validatePath(raw string, allowSlash bool, label string) error {
	if raw == "" || strings.TrimSpace(raw) != raw || len(raw) > 256 {
		return errors.New("invalid " + label)
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil || decoded != raw {
		// Reject encoded separators/dots as well as malformed escapes.  URL
		// escaping is performed by the adapter only after this check.
		return errors.New("invalid " + label)
	}
	if strings.HasPrefix(raw, "/") || strings.HasSuffix(raw, "/") || strings.Contains(raw, "\\") || strings.Contains(raw, "%") {
		return errors.New("invalid " + label)
	}
	if strings.ContainsRune(raw, '\x00') {
		return errors.New("invalid " + label)
	}
	for _, r := range raw {
		if unicode.IsControl(r) || r == unicode.ReplacementChar {
			return errors.New("invalid " + label)
		}
	}
	if !allowSlash && strings.Contains(raw, "/") {
		return errors.New("invalid " + label)
	}
	for _, part := range strings.Split(raw, "/") {
		if part == "" || part == "." || part == ".." {
			return errors.New("invalid " + label)
		}
	}
	return nil
}

func validateRemoteFilePath(raw string) error {
	return validatePath(raw, true, "remote file path")
}

func sourceHTTPClient(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	clone := *client
	if clone.Timeout <= 0 || clone.Timeout > sourceHTTPTimeout {
		clone.Timeout = sourceHTTPTimeout
	}
	// Repository credentials are never part of the public source contract.
	// Disable an injected CookieJar as well as redirect headers so a caller's
	// ambient session cookie cannot be sent to a Hub/CDN endpoint.
	clone.Jar = nil
	// Public source downloads must be directly addressed. A redirect can
	// otherwise move a request to a private host or an untrusted CDN.
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &clone
}

func sourceRedirectPortAllowed(target *url.URL) bool {
	if target == nil {
		return false
	}
	port := target.Port()
	return port == "" || port == "443"
}

func scrubSourceRedirectHeaders(request *http.Request) {
	if request == nil {
		return
	}
	for _, header := range []string{"Authorization", "Cookie", "Proxy-Authorization", "Referer"} {
		request.Header.Del(header)
	}
}

func preserveSourceRangeHeader(request *http.Request, via []*http.Request) {
	if request == nil || request.Header == nil {
		return
	}
	for index := len(via) - 1; index >= 0; index-- {
		if via[index] == nil {
			continue
		}
		if value := via[index].Header.Get("Range"); value != "" {
			request.Header.Set("Range", value)
			return
		}
	}
}

func sourceEndpoint(raw string, allowedHosts ...string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid source endpoint")
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Port() != "" && parsed.Port() != "443" {
		return nil, errors.New("source endpoint port is not allowlisted")
	}
	for _, allowed := range allowedHosts {
		if host == allowed {
			return parsed, nil
		}
	}
	return nil, errors.New("source endpoint host is not allowlisted")
}

func appendURLPath(base *url.URL, segments ...string) *url.URL {
	clone := *base
	path := strings.TrimSuffix(clone.Path, "/")
	for _, segment := range segments {
		for _, part := range strings.Split(segment, "/") {
			// URL.Path is the decoded representation; URL.String performs
			// exactly one escaping pass. Escaping here would produce `%25`
			// for every percent escape in the final request.
			path += "/" + part
		}
	}
	clone.Path = path
	clone.RawPath = ""
	return &clone
}

func sourceGET(ctx context.Context, client *http.Client, requestURL *url.URL) (*http.Response, error) {
	if requestURL == nil || requestURL.Scheme != "https" || requestURL.User != nil {
		return nil, errors.New("invalid source URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, errors.New("create source request")
	}
	request.Header.Set("Accept", "application/json")
	if client == nil {
		client = sourceHTTPClient(nil)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("source request failed")
	}
	if response.StatusCode >= http.StatusMultipleChoices && response.StatusCode < http.StatusBadRequest {
		_ = response.Body.Close()
		return nil, &sourceHTTPError{status: response.StatusCode, redirect: true}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_ = response.Body.Close()
		return nil, &sourceHTTPError{status: response.StatusCode}
	}
	return response, nil
}

func sourceRangeGET(ctx context.Context, client *http.Client, requestURL *url.URL, offset, length int64) (*http.Response, error) {
	end, err := sourceRangeEnd(offset, length)
	if err != nil {
		return nil, err
	}
	if requestURL == nil || requestURL.Scheme != "https" || requestURL.User != nil {
		return nil, errors.New("invalid source URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, errors.New("create source request")
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, end))
	if client == nil {
		client = sourceHTTPClient(nil)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("source request failed")
	}
	if response.StatusCode >= http.StatusMultipleChoices && response.StatusCode < http.StatusBadRequest {
		_ = response.Body.Close()
		return nil, &sourceHTTPError{status: response.StatusCode, redirect: true}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_ = response.Body.Close()
		return nil, &sourceHTTPError{status: response.StatusCode}
	}
	if response.StatusCode != http.StatusPartialContent {
		_ = response.Body.Close()
		return nil, errors.New("source range request was not honored")
	}
	start, responseEnd, total, err := parseSourceContentRange(response.Header.Get("Content-Range"))
	if err != nil || start != offset || responseEnd != end || total <= responseEnd {
		_ = response.Body.Close()
		return nil, errors.New("source range response has invalid content range")
	}
	if response.Body == nil {
		return nil, errors.New("source range response has no body")
	}
	return response, nil
}

func sourceRangeEnd(offset, length int64) (int64, error) {
	if offset < 0 || length <= 0 || offset > math.MaxInt64-(length-1) {
		return 0, errors.New("invalid source range")
	}
	return offset + length - 1, nil
}

func parseSourceContentRange(raw string) (start, end, total int64, err error) {
	parts := strings.Fields(raw)
	if len(parts) != 2 || parts[0] != "bytes" {
		return 0, 0, 0, errors.New("invalid content range")
	}
	bounds := strings.Split(parts[1], "/")
	if len(bounds) != 2 || bounds[1] == "*" {
		return 0, 0, 0, errors.New("invalid content range")
	}
	rangeBounds := strings.Split(bounds[0], "-")
	if len(rangeBounds) != 2 {
		return 0, 0, 0, errors.New("invalid content range")
	}
	if !sourceDecimal(rangeBounds[0]) || !sourceDecimal(rangeBounds[1]) || !sourceDecimal(bounds[1]) {
		return 0, 0, 0, errors.New("invalid content range")
	}
	start, err = strconv.ParseInt(rangeBounds[0], 10, 64)
	if err != nil {
		return 0, 0, 0, errors.New("invalid content range")
	}
	end, err = strconv.ParseInt(rangeBounds[1], 10, 64)
	if err != nil {
		return 0, 0, 0, errors.New("invalid content range")
	}
	total, err = strconv.ParseInt(bounds[1], 10, 64)
	if err != nil || start < 0 || end < start || total <= end {
		return 0, 0, 0, errors.New("invalid content range")
	}
	return start, end, total, nil
}

func sourceDecimal(raw string) bool {
	if raw == "" {
		return false
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

var (
	errSourceRangeBodySize = errors.New("source range response size mismatch")
	errSourceRangeBodyRead = errors.New("read source range response")
)

type exactRangeReadCloser struct {
	body        io.ReadCloser
	remaining   int64
	finalized   bool
	terminalErr error
}

func (r *exactRangeReadCloser) Read(p []byte) (int, error) {
	if r.terminalErr != nil {
		return 0, r.terminalErr
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.remaining == 0 {
		if r.finalized {
			return 0, io.EOF
		}
		var extra [1]byte
		n, err := r.body.Read(extra[:])
		if n > 0 {
			r.terminalErr = errSourceRangeBodySize
			return 0, r.terminalErr
		}
		if errors.Is(err, io.EOF) {
			r.finalized = true
			return 0, io.EOF
		}
		if err != nil {
			r.terminalErr = errSourceRangeBodyRead
			return 0, r.terminalErr
		}
		return 0, nil
	}
	if int64(len(p)) > r.remaining {
		p = p[:int(r.remaining)]
	}
	n, err := io.ReadFull(r.body, p)
	if n > 0 {
		r.remaining -= int64(n)
	}
	if err == io.ErrUnexpectedEOF || errors.Is(err, io.EOF) {
		if r.remaining != 0 {
			r.terminalErr = errSourceRangeBodySize
			return n, r.terminalErr
		}
		return n, io.EOF
	}
	if err != nil {
		r.terminalErr = errSourceRangeBodyRead
		return n, r.terminalErr
	}
	return n, nil
}

func (r *exactRangeReadCloser) Close() error { return r.body.Close() }

func readSourceResponse(response *http.Response) ([]byte, error) {
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxSourceResponseBytes+1))
	if err != nil {
		return nil, errors.New("read source response")
	}
	if int64(len(body)) > maxSourceResponseBytes {
		return nil, errors.New("source response is too large")
	}
	return body, nil
}

func sortAndValidateFiles(files []RemoteFile) ([]RemoteFile, error) {
	result := make([]RemoteFile, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		typ := strings.ToLower(strings.TrimSpace(file.Type))
		switch typ {
		case "", "file", "blob":
		case "directory", "dir", "tree":
			continue
		default:
			return nil, errors.New("source returned a non-regular file")
		}
		if file.Size < 0 || file.Size > maxSourceFileBytes || validateRemoteFilePath(file.Path) != nil {
			return nil, errors.New("source returned an invalid file")
		}
		if _, ok := seen[file.Path]; ok {
			return nil, errors.New("source returned duplicate files")
		}
		seen[file.Path] = struct{}{}
		file.Type = "file"
		result = append(result, file)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}
