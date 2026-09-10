package importer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

var modelScopeHosts = []string{"modelscope.cn", "www.modelscope.cn"}

const (
	modelScopeKnownTruncationLimit = 3000
	maxModelScopeRedirects         = 3
	// The commit-history endpoint is used as a bounded head lookup. The
	// official SDK sends these values when listing commits for a Ref; asking
	// for one item keeps the selected head unambiguous and prevents an
	// unbounded pagination loop in the importer.
	modelScopeCommitPageNumber = 1
	modelScopeCommitPageSize   = 1
	// The v1 contract uses "main" as its generic default while ModelScope's
	// public model repositories commonly use "master".  Only an empty, valid
	// main response may trigger this one bounded compatibility lookup.
	modelScopeRevisionLookupMaxRequests = 2
	// The legacy ModelScope tree endpoint has no continuation token.  When a
	// recursive listing reaches its cap we split it by Root and make bounded
	// shallow/recursive requests.  A hard request budget prevents a hostile or
	// pathological directory graph from turning one import into unbounded IO.
	modelScopeTreeMaxRequests = 1024
	modelScopeTreeMaxFiles    = 10000
)

// ErrImmutableRevisionRequired is returned when a ModelScope branch/tag is
// used directly by List/Open. Workers resolve mutable refs first and persist
// the returned commit, so the actual tree/download operations remain pinned.
var ErrImmutableRevisionRequired = errors.New("model scope revision must be an immutable commit")

type ModelScopeSource struct {
	// client is used for API/tree requests and must never follow redirects.
	client *http.Client
	// downloadClient is used only for file bytes, where the provider may send
	// an explicitly allowlisted HTTPS CDN redirect.
	downloadClient *http.Client
	endpoint       *url.URL
}

// NewModelScopeSource creates an adapter for the public ModelScope Hub.
func NewModelScopeSource(clients ...*http.Client) *ModelScopeSource {
	var client *http.Client
	if len(clients) > 0 {
		client = clients[0]
	}
	return newModelScopeSource(client, "https://www.modelscope.cn")
}

func newModelScopeSource(client *http.Client, endpoint string) *ModelScopeSource {
	parsed, err := sourceEndpoint(endpoint, modelScopeHosts...)
	if err != nil {
		return nil
	}
	return &ModelScopeSource{
		client:         sourceHTTPClient(client),
		downloadClient: modelScopeHTTPClient(client),
		endpoint:       parsed,
	}
}

// ModelScope stores large LFS files behind an HTTPS CDN redirect. Follow only
// HTTPS redirects that remain within the ModelScope DNS suffix; never forward
// caller credentials or accept userinfo on the redirected URL.
func modelScopeHTTPClient(client *http.Client) *http.Client {
	clone := sourceHTTPClient(client)
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxModelScopeRedirects {
			return errors.New("source redirect limit exceeded")
		}
		if req == nil || req.URL == nil || req.URL.Scheme != "https" || req.URL.User != nil || !sourceRedirectPortAllowed(req.URL) {
			return http.ErrUseLastResponse
		}
		host := strings.ToLower(req.URL.Hostname())
		if host != "modelscope.cn" && !strings.HasSuffix(host, ".modelscope.cn") {
			return http.ErrUseLastResponse
		}
		scrubSourceRedirectHeaders(req)
		return nil
	}
	return clone
}

type modelScopeTreeEntry struct {
	Name      string `json:"Name"`
	Path      string `json:"Path"`
	Type      string `json:"Type"`
	Size      *int64 `json:"Size"`
	SizeBytes *int64 `json:"SizeBytes"`
}

type modelScopeTreeResponse struct {
	Data    json.RawMessage `json:"Data"`
	Code    *int            `json:"Code"`
	Success *bool           `json:"Success"`
}

type modelScopeCommit struct {
	ID string `json:"Id"`
}

type modelScopeCommitData struct {
	// Keep the raw value so an explicit `Commit:null` (the provider's empty
	// history response) is distinguishable from a missing field.  The latter
	// is malformed and must not be treated as an empty repository.
	Commits    json.RawMessage `json:"Commit"`
	TotalCount *int            `json:"TotalCount"`
}

type modelScopeCommitResponse struct {
	Data       *modelScopeCommitData `json:"Data"`
	TotalCount *int                  `json:"TotalCount"`
	Code       *int                  `json:"Code"`
	Success    *bool                 `json:"Success"`
}

type modelScopeFilesEnvelope struct {
	Files []modelScopeTreeEntry `json:"Files"`
}

func (s *ModelScopeSource) List(ctx context.Context, repository Repository) ([]RemoteFile, error) {
	if s == nil || s.endpoint == nil || ValidateRepository(repository) != nil || repository.Source != "modelscope" {
		return nil, errors.New("invalid model scope repository")
	}
	if err := validateModelScopeRevision(repository.Revision); err != nil {
		return nil, err
	}
	entries, err := s.listTreePage(ctx, repository, "", true)
	if err != nil {
		return nil, err
	}
	entries, err = s.completeTree(ctx, repository, entries)
	if err != nil {
		return nil, err
	}
	files := make([]RemoteFile, 0, len(entries))
	for _, entry := range entries {
		isDirectory, typeErr := modelScopeEntryType(entry.Type)
		if typeErr != nil {
			return nil, typeErr
		}
		if isDirectory {
			continue
		}
		path := strings.TrimSpace(entry.Path)
		size := int64(-1)
		if entry.Size != nil {
			size = *entry.Size
		} else if entry.SizeBytes != nil {
			size = *entry.SizeBytes
		}
		files = append(files, RemoteFile{Path: path, Size: size, Type: entry.Type})
	}
	return sortAndValidateFiles(files)
}

// ResolveRevision resolves a ModelScope branch/tag through the provider's
// official commit-history endpoint and returns the immutable 40-hex commit
// id. The endpoint accepts Ref, PageNumber and PageSize query parameters; the
// first (newest) history entry is the branch/tag head. A commit input is
// returned locally, which keeps redeliveries and already-pinned imports free
// of an extra network request. The worker persists the result before List/Open
// so all files come from one snapshot. Because the v1 generic default is
// "main" while ModelScope commonly defaults to "master", an explicit empty
// main history gets one bounded fallback request for master; a non-empty main
// history is always preferred.
func (s *ModelScopeSource) ResolveRevision(ctx context.Context, repository Repository) (string, error) {
	if s == nil || s.endpoint == nil || ValidateRepository(repository) != nil || repository.Source != "modelscope" {
		return "", errors.New("invalid model scope repository")
	}
	if isModelScopeCommit(repository.Revision) {
		return strings.ToLower(strings.TrimSpace(repository.Revision)), nil
	}
	// ValidateRepository has already rejected query/fragment/control/path
	// traversal bytes. Keep the revision in a query parameter, matching the
	// official SDK's list_repo_commits call; putting it in the path would hit an
	// undocumented endpoint and does not prove that the returned id is the
	// requested ref's head.
	refs := []string{repository.Revision}
	if repository.Revision == "main" {
		refs = append(refs, "master")
	}
	if len(refs) > modelScopeRevisionLookupMaxRequests {
		return "", sourceRevisionRejected("source revision lookup budget exceeded")
	}
	for _, ref := range refs {
		commit, empty, err := s.resolveModelScopeRef(ctx, repository, ref)
		if err != nil {
			return "", err
		}
		if !empty {
			return commit, nil
		}
		// A valid empty `main` history is the only condition that permits the
		// provider-default fallback. Other empty refs fail closed below.
	}
	return "", sourceRevisionRejected("source revision history is empty")
}

// resolveModelScopeRef performs one bounded commit-history request. It
// returns empty=true only for an explicit empty-history envelope (`Commit:null`
// or `Commit:[]` with `TotalCount:0`); all other missing or inconsistent fields
// are policy failures. Keeping this helper single-request makes the fallback
// in ResolveRevision auditable and caps network work at two requests.
func (s *ModelScopeSource) resolveModelScopeRef(ctx context.Context, repository Repository, revision string) (commit string, empty bool, err error) {
	requestURL := appendURLPath(s.endpoint, "api", "v1", "models", repository.RepoID, "commits")
	query := requestURL.Query()
	query.Set("Ref", revision)
	query.Set("PageNumber", strconv.Itoa(modelScopeCommitPageNumber))
	query.Set("PageSize", strconv.Itoa(modelScopeCommitPageSize))
	requestURL.RawQuery = query.Encode()
	response, err := sourceGET(ctx, s.client, requestURL)
	if err != nil {
		return "", false, classifyRevisionLookupError(err)
	}
	body, err := readSourceResponse(response)
	if err != nil {
		return "", false, err
	}
	var envelope modelScopeCommitResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", false, sourceRevisionRejected("source revision response is malformed")
	}
	if envelope.Code == nil || *envelope.Code != http.StatusOK || envelope.Success == nil || !*envelope.Success || envelope.Data == nil {
		return "", false, sourceRevisionRejected("source revision was not resolved")
	}
	commitPayload := strings.TrimSpace(string(envelope.Data.Commits))
	totalCount, countErr := modelScopeCommitTotalCount(envelope)
	if countErr != nil || totalCount < 0 || commitPayload == "" {
		return "", false, sourceRevisionRejected("source revision history is incomplete")
	}
	if commitPayload == "null" || commitPayload == "[]" {
		if totalCount != 0 {
			return "", false, sourceRevisionRejected("source revision history is incomplete")
		}
		return "", true, nil
	}
	var commits []modelScopeCommit
	if err := json.Unmarshal([]byte(commitPayload), &commits); err != nil {
		return "", false, sourceRevisionRejected("source revision history is malformed")
	}
	if totalCount < 1 || len(commits) != modelScopeCommitPageSize {
		return "", false, sourceRevisionRejected("source revision history is incomplete")
	}
	commit = strings.TrimSpace(commits[0].ID)
	if !isModelScopeCommit(commit) {
		return "", false, sourceRevisionRejected("source revision is not an immutable commit")
	}
	return strings.ToLower(commit), false, nil
}

// modelScopeCommitTotalCount accepts the two response layouts seen across
// ModelScope API deployments and SDK versions: some place TotalCount under
// Data, while the legacy SDK's response wrapper reads a top-level field. If a
// deployment sends both, they must agree; silently choosing one could turn a
// malformed page into an apparently valid snapshot.
func modelScopeCommitTotalCount(envelope modelScopeCommitResponse) (int, error) {
	if envelope.Data == nil {
		return 0, sourceRevisionRejected("source revision response is malformed")
	}
	nested := envelope.Data.TotalCount
	topLevel := envelope.TotalCount
	switch {
	case nested != nil && topLevel != nil:
		if *nested != *topLevel {
			return 0, sourceRevisionRejected("source revision history count is inconsistent")
		}
		return *nested, nil
	case nested != nil:
		return *nested, nil
	case topLevel != nil:
		return *topLevel, nil
	default:
		return 0, sourceRevisionRejected("source revision history count is missing")
	}
}

// listTreePage performs one bounded ModelScope file-tree request and decodes
// the legacy response envelope. Root is a repository-relative directory; an
// empty root addresses the repository root.
func (s *ModelScopeSource) listTreePage(ctx context.Context, repository Repository, root string, recursive bool) ([]modelScopeTreeEntry, error) {
	if root != "" && validateRemoteFilePath(root) != nil {
		return nil, errors.New("source tree root is invalid")
	}
	requestURL := appendURLPath(s.endpoint, "api", "v1", "models", repository.RepoID, "repo", "files")
	query := requestURL.Query()
	query.Set("Revision", repository.Revision)
	query.Set("Recursive", modelScopeBool(recursive))
	if root != "" {
		query.Set("Root", root)
	}
	requestURL.RawQuery = query.Encode()
	response, err := sourceGET(ctx, s.client, requestURL)
	if err != nil {
		return nil, err
	}
	body, err := readSourceResponse(response)
	if err != nil {
		return nil, err
	}
	var envelope modelScopeTreeResponse
	if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil, errors.New("source response is malformed")
	}
	if envelope.Code != nil && *envelope.Code != 0 && *envelope.Code != http.StatusOK {
		return nil, errors.New("source returned an unsuccessful response")
	}
	if envelope.Success != nil && !*envelope.Success {
		return nil, errors.New("source returned an unsuccessful response")
	}
	var entries []modelScopeTreeEntry
	if err := json.Unmarshal(envelope.Data, &entries); err != nil {
		var wrapped modelScopeFilesEnvelope
		if wrappedErr := json.Unmarshal(envelope.Data, &wrapped); wrappedErr != nil || wrapped.Files == nil {
			return nil, errors.New("source response is malformed")
		}
		entries = wrapped.Files
	}
	return entries, nil
}

func modelScopeBool(value bool) string {
	if value {
		return "True"
	}
	return "False"
}

type modelScopeTreeNode struct {
	root    string
	entries []modelScopeTreeEntry
}

// completeTree recovers a recursively listed tree that hit ModelScope's
// silent 3000-entry cap. The endpoint has no page token, but accepts Root;
// re-listing an oversized root shallowly gives us directory boundaries that
// can be walked independently. If a single directory itself has >=3000
// direct children, completeness cannot be proven and the import fails closed.
func (s *ModelScopeSource) completeTree(ctx context.Context, repository Repository, initial []modelScopeTreeEntry) ([]modelScopeTreeEntry, error) {
	queue := []modelScopeTreeNode{{root: "", entries: initial}}
	seenRoots := map[string]struct{}{"": {}}
	complete := make([]modelScopeTreeEntry, 0, len(initial))
	requests := 1 // the initial recursive request has already been made
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		if node.entries == nil {
			if requests >= modelScopeTreeMaxRequests {
				return nil, errors.New("model scope tree walk budget exceeded")
			}
			entries, err := s.listTreePage(ctx, repository, node.root, true)
			requests++
			if err != nil {
				return nil, err
			}
			node.entries = entries
		}
		if len(node.entries) < modelScopeKnownTruncationLimit {
			normalized, err := normalizeModelScopeEntries(node.root, node.entries)
			if err != nil {
				return nil, err
			}
			complete = append(complete, normalized...)
			if len(complete) > modelScopeTreeMaxFiles {
				return nil, errors.New("model scope tree file budget exceeded")
			}
			continue
		}

		if requests >= modelScopeTreeMaxRequests {
			return nil, errors.New("model scope tree walk budget exceeded")
		}
		shallow, err := s.listTreePage(ctx, repository, node.root, false)
		requests++
		if err != nil {
			return nil, err
		}
		// A shallow page at the endpoint cap is itself incomplete; there is no
		// safe way to discover all sibling directories, so do not continue with
		// a partial archive.
		if len(shallow) >= modelScopeKnownTruncationLimit {
			return nil, errors.New("model scope directory may be truncated")
		}
		normalized, err := normalizeModelScopeEntries(node.root, shallow)
		if err != nil {
			return nil, err
		}
		for _, entry := range normalized {
			if modelScopeEntryIsDirectory(entry) {
				if _, exists := seenRoots[entry.Path]; exists {
					return nil, errors.New("model scope tree directory cycle detected")
				}
				seenRoots[entry.Path] = struct{}{}
				if requests+len(queue) >= modelScopeTreeMaxRequests {
					return nil, errors.New("model scope tree walk budget exceeded")
				}
				queue = append(queue, modelScopeTreeNode{root: entry.Path})
				continue
			}
			complete = append(complete, entry)
			if len(complete) > modelScopeTreeMaxFiles {
				return nil, errors.New("model scope tree file budget exceeded")
			}
		}
	}
	return complete, nil
}

func normalizeModelScopeEntries(root string, entries []modelScopeTreeEntry) ([]modelScopeTreeEntry, error) {
	if root != "" && validateRemoteFilePath(root) != nil {
		return nil, errors.New("source tree root is invalid")
	}
	result := make([]modelScopeTreeEntry, 0, len(entries))
	for _, entry := range entries {
		if _, err := modelScopeEntryType(entry.Type); err != nil {
			return nil, err
		}
		path := entry.Path
		if path == "" {
			path = entry.Name
		}
		if path == "" || strings.TrimSpace(path) != path {
			return nil, errors.New("source returned an invalid tree entry")
		}
		if root != "" && path != root && !strings.HasPrefix(path, root+"/") {
			// A Root-scoped API may return either a basename or a path relative
			// to Root. Validate the relative path before joining so `..`, encoded
			// separators and absolute paths cannot escape the requested subtree.
			if validateRemoteFilePath(path) != nil {
				return nil, errors.New("source tree entry escaped requested root")
			}
			path = root + "/" + path
		}
		if validateRemoteFilePath(path) != nil {
			return nil, errors.New("source returned an invalid tree entry")
		}
		entry.Path = path
		result = append(result, entry)
	}
	return result, nil
}

func modelScopeEntryIsDirectory(entry modelScopeTreeEntry) bool {
	directory, _ := modelScopeEntryType(entry.Type)
	return directory
}

func modelScopeEntryType(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "tree", "directory", "dir":
		return true, nil
	case "blob", "file":
		return false, nil
	default:
		// Treat an omitted/unknown type as invalid rather than guessing that it
		// is a regular file. Guessing can archive a directory or silently skip
		// part of a repository when the provider changes its envelope.
		return false, errors.New("source returned an unknown tree entry type")
	}
}

func (s *ModelScopeSource) Open(ctx context.Context, repository Repository, filePath string) (io.ReadCloser, int64, error) {
	if s == nil || s.endpoint == nil || ValidateRepository(repository) != nil || repository.Source != "modelscope" || validateRemoteFilePath(filePath) != nil {
		return nil, 0, errors.New("invalid model scope file")
	}
	if err := validateModelScopeRevision(repository.Revision); err != nil {
		return nil, 0, err
	}
	// ModelScope's model-file endpoint is a repository operation rather than
	// the web UI's /models/.../resolve path.  Keep revision and file path in
	// query parameters so slashes in a repository/file name are encoded by
	// net/url and cannot alter the endpoint route.
	requestURL := appendURLPath(s.endpoint, "api", "v1", "models", repository.RepoID, "repo")
	query := requestURL.Query()
	query.Set("Revision", repository.Revision)
	query.Set("FilePath", filePath)
	requestURL.RawQuery = query.Encode()
	client := s.downloadClient
	if client == nil {
		// Keep manually constructed test/adaptor values safe: a missing
		// download client falls back to the redirect-free client rather than a
		// process-global default.
		client = s.client
	}
	response, err := sourceGET(ctx, client, requestURL)
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

func validateModelScopeRevision(revision string) error {
	if !isModelScopeCommit(revision) {
		return ErrImmutableRevisionRequired
	}
	return nil
}

func isModelScopeCommit(revision string) bool {
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
