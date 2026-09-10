package importer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

var huggingFaceHosts = []string{"huggingface.co", "www.huggingface.co"}

// Hugging Face's documented download endpoints are a fixed set of HTTPS
// hosts. Keep this list explicit instead of allowing arbitrary *.hf.co
// subdomains: the Hub's API/metadata client remains redirect-free, while only
// /resolve downloads may follow these storage/CDN hops.
var huggingFaceDownloadRedirectHosts = map[string]struct{}{
	"cas-server.xethub.hf.co":          {},
	"cas-server.xethub-eu.hf.co":       {},
	"transfer.xethub.hf.co":            {},
	"transfer.xethub-eu.hf.co":         {},
	"cdn-lfs.hf.co":                    {},
	"us.aws.cdn.hf.co":                 {},
	"aws.cdn.hf.co":                    {},
	"us-east-1.aws.cdn.hf.co":          {},
	"us-west-2.aws.cdn.hf.co":          {},
	"eu-west-3.aws.cdn.hf.co":          {},
	"ap-southeast-1.aws.cdn.hf.co":     {},
	"us.gcp.cdn.hf.co":                 {},
	"us-east1.us.gcp.cdn.hf.co":        {},
	"us-central1.us.gcp.cdn.hf.co":     {},
	"us-west4.us.gcp.cdn.hf.co":        {},
	"europe-west4.us.gcp.cdn.hf.co":    {},
	"asia-southeast1.us.gcp.cdn.hf.co": {},
	"cdn-lfs-us-1.hf.co":               {},
	"cdn-lfs-eu-1.hf.co":               {},
}

const maxHuggingFaceRedirects = 3

const (
	maxHuggingFacePages     = 32
	maxHuggingFaceTreeFiles = 10000
)

type HuggingFaceSource struct {
	client         *http.Client
	downloadClient *http.Client
	endpoint       *url.URL
}

// NewHuggingFaceSource creates an adapter for the public Hugging Face Hub.
// The variadic form keeps the default convenient while allowing callers to
// inject an HTTP client with a transport, timeout, or test fixture.
func NewHuggingFaceSource(clients ...*http.Client) *HuggingFaceSource {
	var client *http.Client
	if len(clients) > 0 {
		client = clients[0]
	}
	return newHuggingFaceSource(client, "https://huggingface.co")
}

func newHuggingFaceSource(client *http.Client, endpoint string) *HuggingFaceSource {
	parsed, err := sourceEndpoint(endpoint, huggingFaceHosts...)
	if err != nil {
		return nil
	}
	return &HuggingFaceSource{
		client:         sourceHTTPClient(client),
		downloadClient: huggingFaceDownloadHTTPClient(client),
		endpoint:       parsed,
	}
}

func huggingFaceDownloadHTTPClient(client *http.Client) *http.Client {
	clone := sourceHTTPClient(client)
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxHuggingFaceRedirects {
			return errors.New("source redirect limit exceeded")
		}
		if req == nil || req.URL == nil || req.URL.Scheme != "https" || req.URL.User != nil || !sourceRedirectPortAllowed(req.URL) {
			return http.ErrUseLastResponse
		}
		if _, ok := huggingFaceDownloadRedirectHosts[strings.ToLower(req.URL.Hostname())]; !ok {
			return http.ErrUseLastResponse
		}
		// Source adapters never accept credentials, but explicitly strip
		// hop-sensitive headers in case a caller supplied a custom transport or
		// default redirect headers on the injected client.
		scrubSourceRedirectHeaders(req)
		return nil
	}
	return clone
}

type huggingFaceTreeEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size *int64 `json:"size"`
	LFS  *struct {
		OID  string `json:"oid"`
		Size *int64 `json:"size"`
	} `json:"lfs"`
}

type huggingFaceModelInfo struct {
	SHA string `json:"sha"`
}

// ResolveRevision resolves a mutable branch/tag to the Hub's immutable
// 40-hex commit. A commit input is already immutable and is returned without
// a network call. The worker persists this value before downloading files.
func (s *HuggingFaceSource) ResolveRevision(ctx context.Context, repository Repository) (string, error) {
	if s == nil || s.endpoint == nil || ValidateRepository(repository) != nil || repository.Source != "huggingface" {
		return "", errors.New("invalid hugging face repository")
	}
	if isHuggingFaceCommit(repository.Revision) {
		return strings.ToLower(repository.Revision), nil
	}
	requestURL := appendURLPath(s.endpoint, "api", "models", repository.RepoID, "revision", repository.Revision)
	response, err := sourceGET(ctx, s.client, requestURL)
	if err != nil {
		return "", classifyRevisionLookupError(err)
	}
	body, err := readSourceResponse(response)
	if err != nil {
		return "", err
	}
	var info huggingFaceModelInfo
	if err := json.Unmarshal(body, &info); err != nil || !isHuggingFaceCommit(info.SHA) {
		return "", sourceRevisionRejected("source revision is not an immutable commit")
	}
	return strings.ToLower(strings.TrimSpace(info.SHA)), nil
}

func isHuggingFaceCommit(revision string) bool {
	revision = strings.TrimSpace(revision)
	if len(revision) != 40 {
		return false
	}
	for _, r := range revision {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') && !(r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

func (s *HuggingFaceSource) List(ctx context.Context, repository Repository) ([]RemoteFile, error) {
	if s == nil || s.endpoint == nil || ValidateRepository(repository) != nil || repository.Source != "huggingface" {
		return nil, errors.New("invalid hugging face repository")
	}
	requestURL := appendURLPath(s.endpoint, "api", "models", repository.RepoID, "tree", repository.Revision)
	query := requestURL.Query()
	query.Set("recursive", "true")
	query.Set("expand", "false")
	requestURL.RawQuery = query.Encode()
	files := make([]RemoteFile, 0)
	seenPages := map[string]struct{}{requestURL.String(): {}}
	var totalBytes int64
	for page := 0; ; page++ {
		if page >= maxHuggingFacePages {
			return nil, errors.New("source tree pagination limit exceeded")
		}
		if !huggingFaceAPIURLAllowedForRepository(requestURL, s.endpoint, repository) {
			return nil, errors.New("source tree pagination target rejected")
		}
		response, err := sourceGET(ctx, s.client, requestURL)
		if err != nil {
			return nil, err
		}
		next := response.Header.Get("Link")
		body, err := readSourceResponse(response)
		if err != nil {
			return nil, err
		}
		var entries []huggingFaceTreeEntry
		if err := json.Unmarshal(body, &entries); err != nil {
			return nil, errors.New("source response is malformed")
		}
		for _, entry := range entries {
			typ := strings.ToLower(strings.TrimSpace(entry.Type))
			switch typ {
			case "directory", "tree":
				continue
			case "file", "blob":
			default:
				// The Hub currently returns file/directory here. Do not treat an
				// omitted or future type as a regular file: that could archive a
				// directory or silently lose part of a repository.
				return nil, errors.New("source returned an unknown tree entry type")
			}
			size := int64(-1)
			if entry.Size != nil {
				size = *entry.Size
			}
			oid := ""
			if entry.LFS != nil {
				oid = entry.LFS.OID
				if entry.LFS.Size != nil {
					size = *entry.LFS.Size
				}
			}
			if size < 0 || size > WorkerArchiveMaxBytes || totalBytes > WorkerArchiveMaxBytes-size {
				return nil, errors.New("source tree size budget exceeded")
			}
			if len(files) >= maxHuggingFaceTreeFiles {
				return nil, errors.New("source tree file budget exceeded")
			}
			totalBytes += size
			files = append(files, RemoteFile{Path: entry.Path, Size: size, OID: oid, Type: entry.Type})
		}
		requestURL, err = huggingFaceNextPageURL(next, s.endpoint, repository)
		if err != nil {
			return nil, err
		}
		if requestURL == nil {
			break
		}
		if _, exists := seenPages[requestURL.String()]; exists {
			return nil, errors.New("source tree pagination loop detected")
		}
		seenPages[requestURL.String()] = struct{}{}
	}
	return sortAndValidateFiles(files)
}

func huggingFaceAPIURLAllowed(target, endpoint *url.URL) bool {
	if target == nil || endpoint == nil || target.Scheme != "https" || target.User != nil || target.Fragment != "" || !sourceRedirectPortAllowed(target) {
		return false
	}
	return strings.EqualFold(target.Hostname(), endpoint.Hostname()) && strings.HasPrefix(target.Path, "/api/models/")
}

func huggingFaceAPIURLAllowedForRepository(target, endpoint *url.URL, repository Repository) bool {
	if !huggingFaceAPIURLAllowed(target, endpoint) || ValidateRepository(repository) != nil || repository.Source != "huggingface" {
		return false
	}
	expected := appendURLPath(endpoint, "api", "models", repository.RepoID, "tree", repository.Revision)
	// The Hub's Link continuation changes only its cursor/query. Binding the
	// path to the original repository and revision prevents a same-host Link
	// from silently switching the files being archived.
	return target.Path == expected.Path
}

func huggingFaceNextPageURL(link string, endpoint *url.URL, repository Repository) (*url.URL, error) {
	link = strings.TrimSpace(link)
	if link == "" {
		return nil, nil
	}
	for _, item := range strings.Split(link, ",") {
		item = strings.TrimSpace(item)
		if !strings.Contains(strings.ToLower(item), `rel="next"`) && !strings.Contains(strings.ToLower(item), "rel=next") {
			continue
		}
		start, end := strings.IndexByte(item, '<'), strings.IndexByte(item, '>')
		if start < 0 || end <= start+1 {
			return nil, errors.New("source tree pagination link is malformed")
		}
		target, err := url.Parse(item[start+1 : end])
		if err != nil || !huggingFaceAPIURLAllowedForRepository(target, endpoint, repository) {
			return nil, errors.New("source tree pagination target rejected")
		}
		return target, nil
	}
	return nil, nil
}

func (s *HuggingFaceSource) Open(ctx context.Context, repository Repository, filePath string) (io.ReadCloser, int64, error) {
	if s == nil || s.endpoint == nil || ValidateRepository(repository) != nil || repository.Source != "huggingface" || validateRemoteFilePath(filePath) != nil {
		return nil, 0, errors.New("invalid hugging face file")
	}
	requestURL := appendURLPath(s.endpoint, repository.RepoID, "resolve", repository.Revision, filePath)
	query := requestURL.Query()
	query.Set("download", "true")
	requestURL.RawQuery = query.Encode()
	response, err := sourceGET(ctx, s.downloadClient, requestURL)
	if err != nil {
		return nil, 0, err
	}
	if response.ContentLength < 0 {
		return response.Body, -1, nil
	}
	if response.ContentLength > maxSourceFileBytes {
		_ = response.Body.Close()
		return nil, 0, errors.New("source file is too large")
	}
	return response.Body, response.ContentLength, nil
}
