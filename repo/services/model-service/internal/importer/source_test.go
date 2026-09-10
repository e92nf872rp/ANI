package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

const testModelScopeCommit = "0123456789abcdef0123456789abcdef01234567"

func TestValidateRepositoryRejectsTraversalAndUnsupportedSources(t *testing.T) {
	tests := []Repository{
		{Source: "huggingface", RepoID: "../private", Revision: "main"},
		{Source: "huggingface", RepoID: "Qwen/Qwen3", Revision: "../main"},
		{Source: "huggingface", RepoID: "Qwen/%2e%2e/secret", Revision: "main"},
		{Source: "modelscope", RepoID: "Qwen/Qwen3", Revision: "main/"},
		{Source: "github", RepoID: "org/model", Revision: "main"},
	}
	for _, repository := range tests {
		if err := ValidateRepository(repository); err == nil {
			t.Errorf("ValidateRepository(%+v) succeeded, want rejection", repository)
		}
	}
	if source := newHuggingFaceSource(nil, "https://huggingface.co:8443"); source != nil {
		t.Fatal("newHuggingFaceSource() accepted a non-HTTPS endpoint port")
	}
}

func TestHuggingFaceListFiltersDirectoriesAndRejectsRedirects(t *testing.T) {
	var gotURL *url.URL
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`[
                {"type":"file","path":"config.json","size":12},
                {"type":"directory","path":"weights"},
                {"type":"file","path":"weights/model.safetensors","lfs":{"size":42}}
            ]`)),
			Header:  make(http.Header),
			Request: req,
		}, nil
	})}
	source := mustHuggingFaceSource(t, client, "https://huggingface.co")
	files, err := source.List(context.Background(), Repository{Source: "huggingface", RepoID: "Qwen/Qwen3", Revision: "main"})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(files) != 2 || files[0].Path != "config.json" || files[1].Size != 42 {
		t.Fatalf("List() = %+v, want two regular files", files)
	}
	if gotURL == nil || gotURL.Path != "/api/models/Qwen/Qwen3/tree/main" || gotURL.Query().Get("recursive") != "true" {
		t.Fatalf("request URL = %v, want recursive HF tree URL", gotURL)
	}

	redirectClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusFound, Body: io.NopCloser(strings.NewReader("redirect")), Header: make(http.Header), Request: req}, nil
	})}
	if _, err := mustHuggingFaceSource(t, redirectClient, "https://huggingface.co").List(context.Background(), Repository{Source: "huggingface", RepoID: "Qwen/Qwen3", Revision: "main"}); err == nil {
		t.Fatal("List() followed or accepted redirect")
	} else if strings.Contains(err.Error(), "huggingface.co") || strings.Contains(err.Error(), "Qwen") {
		t.Fatalf("List() error leaks source details: %v", err)
	}
}

func TestHuggingFaceListFollowsBoundedNextPagination(t *testing.T) {
	var requests []*http.Request
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.Clone(req.Context()))
		if len(requests) == 1 {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Link": []string{`<https://huggingface.co/api/models/Qwen/Qwen3/tree/main?cursor=next>; rel="next"`}},
				Body:       io.NopCloser(strings.NewReader(`[{"type":"file","path":"a","size":1}]`)), Request: req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`[{"type":"file","path":"b","size":2}]`)), Request: req,
		}, nil
	})}
	source := mustHuggingFaceSource(t, client, "https://huggingface.co")
	files, err := source.List(context.Background(), Repository{Source: "huggingface", RepoID: "Qwen/Qwen3", Revision: "main"})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(files) != 2 || files[0].Path != "a" || files[1].Path != "b" {
		t.Fatalf("List() = %+v, want both pages", files)
	}
	if len(requests) != 2 || requests[1].URL.Query().Get("cursor") != "next" {
		t.Fatalf("pagination requests = %#v, want next cursor", requests)
	}
}

func TestHuggingFaceListRejectsUnsafeOrUnboundedPagination(t *testing.T) {
	cases := []struct {
		name string
		link string
	}{
		{name: "cross host", link: `<https://evil.example/tree?cursor=next>; rel="next"`},
		{name: "cross repository", link: `<https://huggingface.co/api/models/other/repo/tree/main?cursor=next>; rel="next"`},
		{name: "cross revision", link: `<https://huggingface.co/api/models/Qwen/Qwen3/tree/release?cursor=next>; rel="next"`},
		{name: "self loop", link: `<https://huggingface.co/api/models/Qwen/Qwen3/tree/main?recursive=true&expand=false>; rel="next"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				header := make(http.Header)
				if calls == 1 {
					header.Set("Link", tc.link)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(`[{"type":"file","path":"a","size":1}]`)), Request: req}, nil
			})}
			_, err := mustHuggingFaceSource(t, client, "https://huggingface.co").List(context.Background(), Repository{Source: "huggingface", RepoID: "Qwen/Qwen3", Revision: "main"})
			if err == nil {
				t.Fatal("List() accepted unsafe pagination link")
			}
			if tc.name != "self loop" && calls != 1 {
				t.Fatalf("List() made %d requests after rejecting %s link", calls, tc.name)
			}
		})
	}

	var calls int
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		cursor := req.URL.Query().Get("cursor")
		link := "<https://huggingface.co/api/models/Qwen/Qwen3/tree/main?cursor=" + cursor + "x>; rel=\"next\""
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Link": []string{link}}, Body: io.NopCloser(strings.NewReader(`[{"type":"file","path":"a","size":1}]`)), Request: req}, nil
	})}
	_, err := mustHuggingFaceSource(t, client, "https://huggingface.co").List(context.Background(), Repository{Source: "huggingface", RepoID: "Qwen/Qwen3", Revision: "main"})
	if err == nil || calls > maxHuggingFacePages {
		t.Fatalf("List() pagination calls=%d err=%v, want bounded rejection", calls, err)
	}
}

func TestHuggingFaceListRejectsTreeFileBudgetOverflow(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `[{"type":"file","path":"weights.bin","size":` + fmt.Sprintf("%d", WorkerArchiveMaxBytes+1) + `}]`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
	_, err := mustHuggingFaceSource(t, client, "https://huggingface.co").List(context.Background(), Repository{Source: "huggingface", RepoID: "Qwen/Qwen3", Revision: "main"})
	if err == nil {
		t.Fatal("List() accepted a tree larger than the worker budget")
	}
}

func TestHuggingFaceResolveRevisionReturnsImmutableCommit(t *testing.T) {
	var gotURL *url.URL
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"sha":"0123456789abcdef0123456789abcdef01234567"}`)), Request: req}, nil
	})}
	source := mustHuggingFaceSource(t, client, "https://huggingface.co")
	resolved, err := source.ResolveRevision(context.Background(), Repository{Source: "huggingface", RepoID: "Qwen/Qwen3", Revision: "main"})
	if err != nil || resolved != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("ResolveRevision() = %q, err=%v", resolved, err)
	}
	if gotURL == nil || gotURL.Path != "/api/models/Qwen/Qwen3/revision/main" {
		t.Fatalf("resolve URL = %v", gotURL)
	}
}

func TestModelScopeResolveRevisionUsesOfficialCommitHistoryEndpoint(t *testing.T) {
	var gotURL *url.URL
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"Code":200,"Success":true,"Data":{"Commit":[{"Id":"ABCDEF0123456789abcdef0123456789abcdef01"}],"TotalCount":1}}`)),
			Request:    req,
		}, nil
	})}
	source := mustModelScopeSource(t, client, "https://www.modelscope.cn")
	resolved, err := source.ResolveRevision(context.Background(), Repository{
		Source: "modelscope", RepoID: "Qwen/Qwen3", Revision: "master",
	})
	if err != nil || resolved != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Fatalf("ResolveRevision() = %q, err=%v", resolved, err)
	}
	if gotURL == nil || gotURL.Path != "/api/v1/models/Qwen/Qwen3/commits" {
		t.Fatalf("resolve URL = %v, want ModelScope commit history endpoint", gotURL)
	}
	query := gotURL.Query()
	if query.Get("Ref") != "master" || query.Get("PageNumber") != "1" || query.Get("PageSize") != "1" {
		t.Fatalf("resolve query = %v, want Ref=master PageNumber=1 PageSize=1", query)
	}
}

func TestModelScopeResolveRevisionAcceptsTopLevelTotalCountCompatibilityShape(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"Code":200,"Success":true,"Data":{"Commit":[{"Id":"ABCDEF0123456789abcdef0123456789abcdef01"}]},"TotalCount":1}`)),
			Request:    req,
		}, nil
	})}
	source := mustModelScopeSource(t, client, "https://www.modelscope.cn")
	resolved, err := source.ResolveRevision(context.Background(), Repository{Source: "modelscope", RepoID: "Qwen/Qwen3", Revision: "master"})
	if err != nil || resolved != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Fatalf("ResolveRevision() = %q, err=%v; want top-level TotalCount compatibility", resolved, err)
	}
}

func TestModelScopeResolveRevisionRejectsConflictingTotalCountLocations(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"Code":200,"Success":true,"Data":{"Commit":[{"Id":"ABCDEF0123456789abcdef0123456789abcdef01"}],"TotalCount":1},"TotalCount":2}`)),
			Request:    req,
		}, nil
	})}
	source := mustModelScopeSource(t, client, "https://www.modelscope.cn")
	_, err := source.ResolveRevision(context.Background(), Repository{Source: "modelscope", RepoID: "Qwen/Qwen3", Revision: "master"})
	if !errors.Is(err, ErrSourceRevisionRejected) {
		t.Fatalf("ResolveRevision() error = %v, want ErrSourceRevisionRejected", err)
	}
}

func TestModelScopeResolveRevisionFallsBackToProviderDefaultForGenericMain(t *testing.T) {
	var refs []string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		ref := req.URL.Query().Get("Ref")
		refs = append(refs, ref)
		payload := `{"Code":200,"Success":true,"Data":{"Commit":null,"TotalCount":0}}`
		if ref == "master" {
			payload = `{"Code":200,"Success":true,"Data":{"Commit":[{"Id":"ABCDEF0123456789abcdef0123456789abcdef01"}],"TotalCount":11}}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
	})}
	source := mustModelScopeSource(t, client, "https://www.modelscope.cn")
	resolved, err := source.ResolveRevision(context.Background(), Repository{Source: "modelscope", RepoID: "Qwen/Qwen3", Revision: "main"})
	if err != nil || resolved != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Fatalf("ResolveRevision() = %q, err=%v; want provider master head", resolved, err)
	}
	if fmt.Sprint(refs) != fmt.Sprint([]string{"main", "master"}) {
		t.Fatalf("resolve refs = %#v, want bounded main then master lookup", refs)
	}
}

func TestModelScopeResolveRevisionPrefersGenericMainWhenItHasACommit(t *testing.T) {
	var refs []string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		refs = append(refs, req.URL.Query().Get("Ref"))
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"Code":200,"Success":true,"Data":{"Commit":[{"Id":"ABCDEF0123456789abcdef0123456789abcdef01"}],"TotalCount":1}}`)),
			Request:    req,
		}, nil
	})}
	source := mustModelScopeSource(t, client, "https://www.modelscope.cn")
	resolved, err := source.ResolveRevision(context.Background(), Repository{Source: "modelscope", RepoID: "Qwen/Qwen3", Revision: "main"})
	if err != nil || resolved != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Fatalf("ResolveRevision() = %q, err=%v", resolved, err)
	}
	if fmt.Sprint(refs) != fmt.Sprint([]string{"main"}) {
		t.Fatalf("resolve refs = %#v, want main only when it has a commit", refs)
	}
}

func TestModelScopeResolveRevisionDoesNotFetchAnExistingCommit(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected network request")
	})}
	source := mustModelScopeSource(t, client, "https://www.modelscope.cn")
	commit := "0123456789abcdef0123456789abcdef01234567"
	resolved, err := source.ResolveRevision(context.Background(), Repository{
		Source: "modelscope", RepoID: "Qwen/Qwen3", Revision: commit,
	})
	if err != nil || resolved != commit {
		t.Fatalf("ResolveRevision() = %q, err=%v; want existing commit", resolved, err)
	}
	if calls != 0 {
		t.Fatalf("existing commit triggered %d network calls", calls)
	}
}

func TestModelScopeResolveRevisionRejectsUnsuccessfulOrMalformedResponse(t *testing.T) {
	cases := []string{
		`{"Code":200,"Success":false,"Data":{"Commit":[{"Id":"0123456789abcdef0123456789abcdef01234567"}],"TotalCount":1}}`,
		`{"Code":200,"Success":true,"Data":{"Commit":[{"Id":"not-a-commit"}],"TotalCount":1}}`,
		`{"Code":200,"Success":true,"Data":{}}`,
	}
	for i, payload := range cases {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
			})}
			source := mustModelScopeSource(t, client, "https://www.modelscope.cn")
			_, err := source.ResolveRevision(context.Background(), Repository{Source: "modelscope", RepoID: "Qwen/Qwen3", Revision: "master"})
			if err == nil {
				t.Fatal("ResolveRevision() accepted an unsuccessful or malformed response")
			}
			if !errors.Is(err, ErrSourceRevisionRejected) {
				t.Fatalf("ResolveRevision() error = %v, want ErrSourceRevisionRejected", err)
			}
		})
	}
}

func TestModelScopeResolveRevisionRejectsAmbiguousCommitHistory(t *testing.T) {
	cases := []string{
		// The resolver asks for one item; accepting an oversized page would make
		// the selected head dependent on undocumented ordering.
		`{"Code":200,"Success":true,"Data":{"Commit":[{"Id":"0123456789abcdef0123456789abcdef01234567"},{"Id":"abcdef0123456789abcdef0123456789abcdef01"}],"TotalCount":2}}`,
		// A non-empty page without an authoritative total is not the official
		// commit-history envelope and must fail closed.
		`{"Code":200,"Success":true,"Data":{"Commit":[{"Id":"0123456789abcdef0123456789abcdef01234567"}]}}`,
		// A total count of zero cannot describe a usable head commit.
		`{"Code":200,"Success":true,"Data":{"Commit":[],"TotalCount":0}}`,
	}
	for i, payload := range cases {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
			})}
			source := mustModelScopeSource(t, client, "https://www.modelscope.cn")
			_, err := source.ResolveRevision(context.Background(), Repository{Source: "modelscope", RepoID: "Qwen/Qwen3", Revision: "master"})
			if !errors.Is(err, ErrSourceRevisionRejected) {
				t.Fatalf("ResolveRevision() error = %v, want ErrSourceRevisionRejected", err)
			}
		})
	}
}

func TestHuggingFaceResolveRevisionRejectsMalformedResponseAsPolicyError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"sha":"not-a-commit"}`)), Request: req}, nil
	})}
	source := mustHuggingFaceSource(t, client, "https://huggingface.co")
	_, err := source.ResolveRevision(context.Background(), Repository{Source: "huggingface", RepoID: "Qwen/Qwen3", Revision: "main"})
	if !errors.Is(err, ErrSourceRevisionRejected) {
		t.Fatalf("ResolveRevision() error = %v, want ErrSourceRevisionRejected", err)
	}
}

func TestRevisionLookupHTTPStatusClassification(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		permanent bool
		redirect  bool
	}{
		{name: "not found", status: http.StatusNotFound, permanent: true},
		{name: "forbidden", status: http.StatusForbidden, permanent: true},
		{name: "rate limited", status: http.StatusTooManyRequests},
		{name: "timeout", status: http.StatusRequestTimeout},
		{name: "server error", status: http.StatusBadGateway},
		{name: "redirect", status: http.StatusFound, permanent: true, redirect: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
			})}
			source := mustHuggingFaceSource(t, client, "https://huggingface.co")
			_, err := source.ResolveRevision(context.Background(), Repository{Source: "huggingface", RepoID: "Qwen/Qwen3", Revision: "main"})
			if err == nil {
				t.Fatal("ResolveRevision() unexpectedly succeeded")
			}
			if got := errors.Is(err, ErrSourceRevisionRejected); got != tc.permanent {
				t.Fatalf("ErrSourceRevisionRejected=%v, want %v (err=%v)", got, tc.permanent, err)
			}
		})
	}
}

func TestModelScopeListRejectsUnknownOrEmptyEntryType(t *testing.T) {
	for _, typ := range []string{"", "mystery", "symlink"} {
		t.Run(fmt.Sprintf("type-%q", typ), func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				payload := fmt.Sprintf(`{"Code":200,"Success":true,"Data":[{"Path":"weights/model.bin","Type":%q,"Size":1}]}`, typ)
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
			})}
			source := mustModelScopeSource(t, client, "https://www.modelscope.cn")
			_, err := source.List(context.Background(), Repository{Source: "modelscope", RepoID: "Qwen/Qwen3", Revision: testModelScopeCommit})
			if err == nil {
				t.Fatalf("List() accepted entry Type=%q", typ)
			}
		})
	}
}

func TestHuggingFaceListRejectsUnknownOrEmptyEntryType(t *testing.T) {
	for _, typ := range []string{"", "mystery", "symlink"} {
		t.Run(fmt.Sprintf("type-%q", typ), func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				payload := fmt.Sprintf(`[{"type":%q,"path":"weights/model.bin","size":1}]`, typ)
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
			})}
			source := mustHuggingFaceSource(t, client, "https://huggingface.co")
			_, err := source.List(context.Background(), Repository{Source: "huggingface", RepoID: "Qwen/Qwen3", Revision: "main"})
			if err == nil {
				t.Fatalf("List() accepted entry type=%q", typ)
			}
		})
	}
}

func TestModelScopeRootNormalizationRejectsOutsideFullPath(t *testing.T) {
	size := int64(1)
	for _, path := range []string{"/weights/model.bin", "../secret", "weights/../secret", "weights/../../secret"} {
		t.Run(path, func(t *testing.T) {
			_, err := normalizeModelScopeEntries("weights", []modelScopeTreeEntry{{Path: path, Type: "blob", Size: &size}})
			if err == nil {
				t.Fatalf("normalizeModelScopeEntries accepted path %q outside root", path)
			}
		})
	}
}

func TestHuggingFaceOpenRejectsUnsafePathAndNonSuccess(t *testing.T) {
	called := false
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("model")), Header: make(http.Header), Request: req}, nil
	})}
	source := mustHuggingFaceSource(t, client, "https://huggingface.co")
	repository := Repository{Source: "huggingface", RepoID: "Qwen/Qwen3", Revision: "main"}
	if _, _, err := source.Open(context.Background(), repository, "../secret"); err == nil {
		t.Fatal("Open() accepted traversal path")
	}
	if called {
		t.Fatal("Open() performed network request for unsafe path")
	}

	badClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("private details")), Header: make(http.Header), Request: req}, nil
	})}
	if _, _, err := mustHuggingFaceSource(t, badClient, "https://huggingface.co").Open(context.Background(), repository, "config.json"); err == nil {
		t.Fatal("Open() accepted non-success response")
	} else if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "Qwen") {
		t.Fatalf("Open() error leaks response details: %v", err)
	}
}

func TestHuggingFaceOpenFollowsOnlyAllowlistedHTTPSCDNRedirect(t *testing.T) {
	var requests []*http.Request
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New() error = %v", err)
	}
	hubURL, err := url.Parse("https://huggingface.co")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	jar.SetCookies(hubURL, []*http.Cookie{{Name: "session", Value: "must-not-forward"}})
	client := &http.Client{Jar: jar, Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.Clone(req.Context()))
		if len(requests) == 1 {
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"https://cdn-lfs-us-1.hf.co/repos/object"}},
				Body:       io.NopCloser(strings.NewReader("redirect")),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        make(http.Header),
			Body:          io.NopCloser(strings.NewReader("model")),
			ContentLength: int64(len("model")),
			Request:       req,
		}, nil
	})}
	source := mustHuggingFaceSource(t, client, "https://huggingface.co")
	body, size, err := source.Open(context.Background(), Repository{Source: "huggingface", RepoID: "Qwen/Qwen3", Revision: "main"}, "config.json")
	if err != nil {
		t.Fatalf("Open() rejected allowlisted HF CDN redirect: %v", err)
	}
	t.Cleanup(func() {
		if err := body.Close(); err != nil {
			t.Errorf("close source body: %v", err)
		}
	})
	data, err := io.ReadAll(body)
	if err != nil || string(data) != "model" || size != int64(len(data)) {
		t.Fatalf("Open() body=%q size=%d err=%v, want model payload", data, size, err)
	}
	if len(requests) != 2 || requests[1].URL.Hostname() != "cdn-lfs-us-1.hf.co" {
		t.Fatalf("redirect requests = %#v, want one allowlisted CDN hop", requests)
	}
	for _, header := range []string{"Authorization", "Cookie", "Proxy-Authorization"} {
		if requests[0].Header.Get(header) != "" {
			t.Fatalf("initial request carried %s header", header)
		}
		if requests[1].Header.Get(header) != "" {
			t.Fatalf("redirect forwarded %s header", header)
		}
	}
}

func TestHuggingFaceOpenRejectsUnsafeRedirectTargets(t *testing.T) {
	tests := []struct {
		name     string
		location string
	}{
		{name: "untrusted host", location: "https://evil.example/object"},
		{name: "http downgrade", location: "http://cdn-lfs-us-1.hf.co/object"},
		{name: "non-default port", location: "https://cdn-lfs-us-1.hf.co:8443/object"},
		{name: "userinfo", location: "https://user:password@cdn-lfs-us-1.hf.co/object"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{tc.location}}, Body: io.NopCloser(strings.NewReader("redirect")), Request: req}, nil
			})}
			source := mustHuggingFaceSource(t, client, "https://huggingface.co")
			_, _, err := source.Open(context.Background(), Repository{Source: "huggingface", RepoID: "Qwen/Qwen3", Revision: "main"}, "config.json")
			if err == nil {
				t.Fatal("Open() accepted unsafe redirect")
			}
			if strings.Contains(err.Error(), "evil.example") || strings.Contains(err.Error(), "password") {
				t.Fatalf("Open() error leaks redirect details: %v", err)
			}
		})
	}
}

func TestHuggingFaceOpenRejectsRedirectLoopsAndExcessiveHops(t *testing.T) {
	var calls int
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"https://cdn-lfs-us-1.hf.co/object"}},
			Body:       io.NopCloser(strings.NewReader("redirect")),
			Request:    req,
		}, nil
	})}
	source := mustHuggingFaceSource(t, client, "https://huggingface.co")
	_, _, err := source.Open(context.Background(), Repository{Source: "huggingface", RepoID: "Qwen/Qwen3", Revision: "main"}, "config.json")
	if err == nil {
		t.Fatal("Open() accepted an excessive redirect chain")
	}
	if calls > maxHuggingFaceRedirects+1 {
		t.Fatalf("redirect calls=%d, want at most %d", calls, maxHuggingFaceRedirects+1)
	}
}

func TestModelScopeListParsesDataAndRejectsMalformedResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"Data":[{"Name":"config.json","Type":"blob","Size":8},{"Name":"weights/model.bin","Type":"tree","Size":0}]}`)),
			Header:     make(http.Header), Request: req,
		}, nil
	})}
	source := mustModelScopeSource(t, client, "https://www.modelscope.cn")
	files, err := source.List(context.Background(), Repository{Source: "modelscope", RepoID: "Qwen/Qwen3", Revision: testModelScopeCommit})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(files) != 1 || files[0].Path != "config.json" || files[0].Size != 8 {
		t.Fatalf("List() = %+v, want one blob file", files)
	}

	malformed := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header), Request: req}, nil
	})}
	if _, err := mustModelScopeSource(t, malformed, "https://www.modelscope.cn").List(context.Background(), Repository{Source: "modelscope", RepoID: "Qwen/Qwen3", Revision: testModelScopeCommit}); err == nil {
		t.Fatal("List() accepted malformed response")
	}
	failed := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"Code":1001,"Data":[]}`)), Header: make(http.Header), Request: req}, nil
	})}
	if _, err := mustModelScopeSource(t, failed, "https://www.modelscope.cn").List(context.Background(), Repository{Source: "modelscope", RepoID: "Qwen/Qwen3", Revision: testModelScopeCommit}); err == nil {
		t.Fatal("List() accepted application-level ModelScope error")
	}
	failedSuccess := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"Code":200,"Success":false,"Data":[]}`)), Header: make(http.Header), Request: req}, nil
	})}
	if _, err := mustModelScopeSource(t, failedSuccess, "https://www.modelscope.cn").List(context.Background(), Repository{Source: "modelscope", RepoID: "Qwen/Qwen3", Revision: testModelScopeCommit}); err == nil {
		t.Fatal("List() accepted Success=false response")
	}
}

func TestModelScopeListParsesFilesEnvelope(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"Code":200,"Data":{"Files":[{"Name":"config.json","Path":"config.json","Type":"blob","Size":8}]}}`)), Header: make(http.Header), Request: req}, nil
	})}
	source := mustModelScopeSource(t, client, "https://www.modelscope.cn")
	files, err := source.List(context.Background(), Repository{Source: "modelscope", RepoID: "Qwen/Qwen3", Revision: testModelScopeCommit})
	if err != nil || len(files) != 1 || files[0].Path != "config.json" || files[0].Size != 8 {
		t.Fatalf("List() = %#v, err=%v; want Files envelope parsed", files, err)
	}
}

func TestModelScopeOpenUsesPublicRepositoryDownloadEndpoint(t *testing.T) {
	var gotURL *url.URL
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL
		return &http.Response{
			StatusCode:    http.StatusOK,
			Body:          io.NopCloser(strings.NewReader("model")),
			ContentLength: int64(len("model")),
			Header:        make(http.Header),
			Request:       req,
		}, nil
	})}
	source := mustModelScopeSource(t, client, "https://www.modelscope.cn")
	body, size, err := source.Open(context.Background(), Repository{Source: "modelscope", RepoID: "Qwen/Qwen3", Revision: testModelScopeCommit}, "weights/model.bin")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := body.Close(); err != nil {
			t.Errorf("close source body: %v", err)
		}
	})
	if size != int64(len("model")) {
		t.Fatalf("Open() size = %d, want %d", size, len("model"))
	}
	if gotURL == nil || gotURL.Path != "/api/v1/models/Qwen/Qwen3/repo" ||
		gotURL.Query().Get("Revision") != testModelScopeCommit || gotURL.Query().Get("FilePath") != "weights/model.bin" {
		t.Fatalf("request URL = %v, want ModelScope repo download endpoint", gotURL)
	}
}

func TestModelScopeListAndOpenRejectMutableRevision(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"Code":200,"Data":[]}`)),
			Header:     make(http.Header), Request: req,
		}, nil
	})}
	source := mustModelScopeSource(t, client, "https://www.modelscope.cn")
	repository := Repository{Source: "modelscope", RepoID: "Qwen/Qwen3", Revision: "master"}
	if _, err := source.List(context.Background(), repository); !errors.Is(err, ErrImmutableRevisionRequired) {
		t.Fatalf("List() error = %v, want immutable revision rejection", err)
	}
	if _, _, err := source.Open(context.Background(), repository, "config.json"); !errors.Is(err, ErrImmutableRevisionRequired) {
		t.Fatalf("Open() error = %v, want immutable revision rejection", err)
	}
	if calls != 0 {
		t.Fatalf("mutable revision triggered %d network calls", calls)
	}
}

func TestModelScopeListAndOpenUseSameImmutableRevision(t *testing.T) {
	var requests []*http.Request
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.Clone(req.Context()))
		if strings.HasSuffix(req.URL.Path, "/repo/files") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"Code":200,"Data":[{"Name":"config.json","Type":"blob","Size":2}]}`)),
				Header:     make(http.Header), Request: req,
			}, nil
		}
		return &http.Response{
			StatusCode:    http.StatusOK,
			Body:          io.NopCloser(strings.NewReader("ok")),
			ContentLength: 2,
			Header:        make(http.Header), Request: req,
		}, nil
	})}
	source := mustModelScopeSource(t, client, "https://www.modelscope.cn")
	repository := Repository{Source: "modelscope", RepoID: "Qwen/Qwen3", Revision: testModelScopeCommit}
	if _, err := source.List(context.Background(), repository); err != nil {
		t.Fatalf("List() error = %v", err)
	}
	body, _, err := source.Open(context.Background(), repository, "config.json")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	_ = body.Close()
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want List and Open", len(requests))
	}
	for _, req := range requests {
		if req.URL.Query().Get("Revision") != testModelScopeCommit {
			t.Fatalf("request revision = %q, want %q", req.URL.Query().Get("Revision"), testModelScopeCommit)
		}
	}
}

func TestModelScopeListRejectsKnownTruncationLimit(t *testing.T) {
	var body strings.Builder
	body.WriteString(`{"Code":200,"Data":[`)
	for i := 0; i < modelScopeKnownTruncationLimit; i++ {
		if i > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"Name":"file-%04d.bin","Type":"blob","Size":1}`, i)
	}
	body.WriteString(`]}`)
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body.String())), Request: req}, nil
	})}
	_, err := mustModelScopeSource(t, client, "https://www.modelscope.cn").List(context.Background(), Repository{Source: "modelscope", RepoID: "Qwen/Qwen3", Revision: testModelScopeCommit})
	if err == nil {
		t.Fatal("List() accepted a full 3000-entry page that may be truncated")
	}
}

func TestModelScopeListWalksTruncatedRootByDirectory(t *testing.T) {
	var requests []string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		root := req.URL.Query().Get("Root")
		recursive := req.URL.Query().Get("Recursive")
		requests = append(requests, root+"|"+recursive)
		var payload string
		switch {
		case root == "" && recursive == "True":
			// The server's recursive root response is capped. It is deliberately
			// noisy here: List must discard this incomplete prefix and re-list
			// the root shallowly before descending into directories.
			var body strings.Builder
			body.WriteString(`{"Code":200,"Data":[`)
			for i := 0; i < modelScopeKnownTruncationLimit; i++ {
				if i > 0 {
					body.WriteByte(',')
				}
				fmt.Fprintf(&body, `{"Name":"ignored-%04d.bin","Type":"blob","Size":1}`, i)
			}
			body.WriteString(`]}`)
			payload = body.String()
		case root == "" && recursive == "False":
			payload = `{"Code":200,"Data":[{"Path":"weights","Type":"tree"},{"Path":"README.md","Type":"blob","Size":4}]}`
		case root == "weights" && recursive == "True":
			payload = `{"Code":200,"Data":[{"Path":"weights/config.json","Type":"blob","Size":2},{"Path":"weights/model.bin","Type":"blob","Size":3}]}`
		default:
			return nil, fmt.Errorf("unexpected tree request root=%q recursive=%q", root, recursive)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(payload)), Header: make(http.Header), Request: req}, nil
	})}
	source := mustModelScopeSource(t, client, "https://www.modelscope.cn")
	files, err := source.List(context.Background(), Repository{Source: "modelscope", RepoID: "Qwen/Qwen3", Revision: testModelScopeCommit})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(files) != 3 || files[0].Path != "README.md" || files[1].Path != "weights/config.json" || files[2].Path != "weights/model.bin" {
		t.Fatalf("List() = %#v, want complete directory walk", files)
	}
	wantRequests := []string{"|True", "|False", "weights|True"}
	if fmt.Sprint(requests) != fmt.Sprint(wantRequests) {
		t.Fatalf("tree requests = %#v, want %#v", requests, wantRequests)
	}
}

func TestModelScopeRootNormalizationAcceptsRelativeNestedPathsAndRejectsWhitespace(t *testing.T) {
	size := int64(3)
	entries, err := normalizeModelScopeEntries("weights", []modelScopeTreeEntry{{Path: "sub/model.bin", Type: "blob", Size: &size}})
	if err != nil || len(entries) != 1 || entries[0].Path != "weights/sub/model.bin" {
		t.Fatalf("relative nested entry = %#v, err=%v; want weights/sub/model.bin", entries, err)
	}
	badSize := int64(1)
	if _, err := normalizeModelScopeEntries("weights", []modelScopeTreeEntry{{Path: " model.bin", Type: "blob", Size: &badSize}}); err == nil {
		t.Fatal("normalization accepted an entry with surrounding whitespace")
	}
}

func TestModelScopeTreeRejectsRedirectButDownloadFollowsAllowlistedCDN(t *testing.T) {
	var listCalls, openCalls int
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/repo/files") {
			listCalls++
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://cdn-lfs-cn-1.modelscope.cn/object"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		}
		openCalls++
		if openCalls == 1 {
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://cdn-lfs-cn-1.modelscope.cn/object"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("model")), ContentLength: 5, Header: make(http.Header), Request: req}, nil
	})}
	source := newModelScopeSource(client, "https://www.modelscope.cn")
	if _, err := source.List(context.Background(), Repository{Source: "modelscope", RepoID: "Qwen/Qwen3", Revision: testModelScopeCommit}); err == nil {
		t.Fatal("List() followed an API redirect")
	}
	if listCalls != 1 {
		t.Fatalf("List() made %d requests, want one redirect-free API request", listCalls)
	}
	body, size, err := source.Open(context.Background(), Repository{Source: "modelscope", RepoID: "Qwen/Qwen3", Revision: testModelScopeCommit}, "weights/model.bin")
	if err != nil {
		t.Fatalf("Open() rejected allowlisted HTTPS CDN redirect: %v", err)
	}
	t.Cleanup(func() {
		if err := body.Close(); err != nil {
			t.Errorf("close source body: %v", err)
		}
	})
	if size != 5 || openCalls != 2 {
		t.Fatalf("Open() size/calls = %d/%d, want 5/2", size, openCalls)
	}
}

func TestModelScopeDownloadRedirectsAreBounded(t *testing.T) {
	callback := modelScopeHTTPClient(nil).CheckRedirect
	for _, viaLen := range []int{3, 4} {
		target, err := url.Parse("https://cdn-lfs-cn-1.modelscope.cn/object")
		if err != nil {
			t.Fatalf("url.Parse() error = %v", err)
		}
		via := make([]*http.Request, viaLen)
		if err := callback(&http.Request{URL: target, Header: make(http.Header)}, via); err == nil {
			t.Fatalf("redirect callback accepted %d prior hops", viaLen)
		}
	}
}

func TestSourceRedirectCallbacksRequireHTTPSDefaultPortAndScrubHeaders(t *testing.T) {
	tests := []struct {
		name     string
		callback func(*http.Request, []*http.Request) error
		host     string
	}{
		{name: "hugging face", callback: huggingFaceDownloadHTTPClient(nil).CheckRedirect, host: "cdn-lfs-us-1.hf.co"},
		{name: "model scope", callback: modelScopeHTTPClient(nil).CheckRedirect, host: "cdn-lfs-cn-1.modelscope.cn"},
	}
	for _, tc := range tests {
		t.Run(tc.name+" rejects non-default port", func(t *testing.T) {
			target, err := url.Parse("https://" + tc.host + ":8443/object")
			if err != nil {
				t.Fatalf("url.Parse() error = %v", err)
			}
			if err := tc.callback(&http.Request{URL: target, Header: make(http.Header)}, []*http.Request{{}}); err == nil {
				t.Fatal("redirect callback accepted non-default port")
			}
		})
		t.Run(tc.name+" scrubs sensitive headers", func(t *testing.T) {
			target, err := url.Parse("https://" + tc.host + "/object")
			if err != nil {
				t.Fatalf("url.Parse() error = %v", err)
			}
			req := &http.Request{URL: target, Header: http.Header{
				"Authorization":       []string{"Bearer secret"},
				"Cookie":              []string{"session=secret"},
				"Proxy-Authorization": []string{"Basic secret"},
				"Referer":             []string{"https://huggingface.co/private"},
			}}
			if err := tc.callback(req, []*http.Request{{}}); err != nil {
				t.Fatalf("redirect callback rejected allowlisted target: %v", err)
			}
			for _, header := range []string{"Authorization", "Cookie", "Proxy-Authorization", "Referer"} {
				if req.Header.Get(header) != "" {
					t.Fatalf("redirect callback kept %s header", header)
				}
			}
		})
	}
}

func TestSourceErrorsStayRedacted(t *testing.T) {
	want := errors.New("transport secret")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, want })}
	_, err := mustModelScopeSource(t, client, "https://www.modelscope.cn").List(context.Background(), Repository{Source: "modelscope", RepoID: "org/model", Revision: testModelScopeCommit})
	if err == nil || strings.Contains(err.Error(), want.Error()) || strings.Contains(err.Error(), "org/model") {
		t.Fatalf("List() error = %v, contains transport/repository details", err)
	}
}

func mustHuggingFaceSource(t *testing.T, client *http.Client, endpoint string) *HuggingFaceSource {
	t.Helper()
	source := newHuggingFaceSource(client, endpoint)
	if source == nil {
		t.Fatalf("newHuggingFaceSource() returned nil")
	}
	return source
}

func mustModelScopeSource(t *testing.T, client *http.Client, endpoint string) *ModelScopeSource {
	t.Helper()
	source := newModelScopeSource(client, endpoint)
	if source == nil {
		t.Fatalf("newModelScopeSource() returned nil")
	}
	return source
}
