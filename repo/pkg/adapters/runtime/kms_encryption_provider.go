package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type KMSEncryptionProviderConfig struct {
	BaseURL     string
	BearerToken string
	Provider    string
	HTTPClient  *http.Client
}

type KMSSM4HTTPEncryptionProvider struct {
	baseURL     string
	bearerToken string
	provider    string
	httpClient  *http.Client
}

func NewKMSSM4HTTPEncryptionProvider(config KMSEncryptionProviderConfig) (*KMSSM4HTTPEncryptionProvider, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("%w: KMS provider base URL is required", ports.ErrNotConfigured)
	}
	provider := strings.TrimSpace(config.Provider)
	if provider == "" {
		provider = "kms-sm4"
	}
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &KMSSM4HTTPEncryptionProvider{
		baseURL:     baseURL,
		bearerToken: config.BearerToken,
		provider:    provider,
		httpClient:  client,
	}, nil
}

func (p *KMSSM4HTTPEncryptionProvider) CreateKeyMaterial(ctx context.Context, req ports.EncryptionProviderCreateKeyRequest) (ports.EncryptionProviderKeyResult, error) {
	var result kmsKeyResult
	if err := p.post(ctx, "/v1/keys", req, &result); err != nil {
		return ports.EncryptionProviderKeyResult{}, err
	}
	return p.keyResult(result, req.TenantID, req.KeyID), nil
}

func (p *KMSSM4HTTPEncryptionProvider) RotateKeyMaterial(ctx context.Context, req ports.EncryptionProviderRotateKeyRequest) (ports.EncryptionProviderKeyResult, error) {
	var result kmsKeyResult
	path := "/v1/keys/" + url.PathEscape(req.PreviousKeyID) + "/rotate"
	if err := p.post(ctx, path, req, &result); err != nil {
		return ports.EncryptionProviderKeyResult{}, err
	}
	return p.keyResult(result, req.TenantID, req.RotatedKeyID), nil
}

func (p *KMSSM4HTTPEncryptionProvider) RevokeKeyMaterial(ctx context.Context, req ports.EncryptionProviderRevokeKeyRequest) (ports.EncryptionProviderKeyResult, error) {
	var result kmsKeyResult
	path := "/v1/keys/" + url.PathEscape(req.KeyID) + "/revoke"
	if err := p.post(ctx, path, req, &result); err != nil {
		return ports.EncryptionProviderKeyResult{}, err
	}
	return p.keyResult(result, req.TenantID, req.KeyID), nil
}

func (p *KMSSM4HTTPEncryptionProvider) DeleteKeyMaterial(ctx context.Context, req ports.EncryptionProviderDeleteKeyRequest) (ports.EncryptionProviderKeyResult, error) {
	var result kmsKeyResult
	path := "/v1/keys/" + url.PathEscape(req.KeyID) + "/delete"
	if err := p.post(ctx, path, req, &result); err != nil {
		return ports.EncryptionProviderKeyResult{}, err
	}
	return p.keyResult(result, req.TenantID, req.KeyID), nil
}

func (p *KMSSM4HTTPEncryptionProvider) SealObject(ctx context.Context, req ports.EncryptionProviderSealRequest) (ports.EncryptionProviderSealResult, error) {
	var result kmsSealResult
	if err := p.post(ctx, "/v1/seal", req, &result); err != nil {
		return ports.EncryptionProviderSealResult{}, err
	}
	expiresAt, err := parseKMSExpiresAt(result.ExpiresAt)
	if err != nil {
		return ports.EncryptionProviderSealResult{}, err
	}
	return ports.EncryptionProviderSealResult{
		SealedObjectURI: result.SealedObjectURI,
		UnsealToken:     result.UnsealToken,
		ExpiresAt:       expiresAt,
		Provider:        p.providerName(result.Provider),
		ResourceRefs:    cloneStrings(result.ResourceRefs),
	}, nil
}

func (p *KMSSM4HTTPEncryptionProvider) CreateUnsealToken(ctx context.Context, req ports.EncryptionProviderUnsealTokenRequest) (ports.EncryptionProviderUnsealTokenResult, error) {
	var result kmsUnsealTokenResult
	if err := p.post(ctx, "/v1/unseal-token", req, &result); err != nil {
		return ports.EncryptionProviderUnsealTokenResult{}, err
	}
	expiresAt, err := parseKMSExpiresAt(result.ExpiresAt)
	if err != nil {
		return ports.EncryptionProviderUnsealTokenResult{}, err
	}
	return ports.EncryptionProviderUnsealTokenResult{
		UnsealToken:  result.UnsealToken,
		ExpiresAt:    expiresAt,
		Provider:     p.providerName(result.Provider),
		ResourceRefs: cloneStrings(result.ResourceRefs),
	}, nil
}

func (p *KMSSM4HTTPEncryptionProvider) post(ctx context.Context, path string, body any, out any) error {
	content, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(content))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(p.bearerToken) != "" {
		req.Header.Set("Authorization", "Bearer "+p.bearerToken)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: KMS provider returned status %d: %s", ports.ErrInvalid, resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if len(responseBody) == 0 {
		return nil
	}
	return json.Unmarshal(responseBody, out)
}

func (p *KMSSM4HTTPEncryptionProvider) keyResult(result kmsKeyResult, tenantID string, keyID string) ports.EncryptionProviderKeyResult {
	refs := cloneStrings(result.ResourceRefs)
	if len(refs) == 0 {
		refs = []string{"kms://" + tenantID + "/" + keyID}
	}
	return ports.EncryptionProviderKeyResult{
		Applied:      result.Applied,
		Provider:     p.providerName(result.Provider),
		ResourceRefs: refs,
		Reason:       result.Reason,
		AppliedAt:    result.AppliedAt,
	}
}

func (p *KMSSM4HTTPEncryptionProvider) providerName(provider string) string {
	if strings.TrimSpace(provider) == "" {
		return p.provider
	}
	return provider
}

type kmsKeyResult struct {
	Applied      bool      `json:"applied"`
	Provider     string    `json:"provider"`
	ResourceRefs []string  `json:"resource_refs"`
	Reason       string    `json:"reason"`
	AppliedAt    time.Time `json:"applied_at"`
}

type kmsSealResult struct {
	SealedObjectURI string   `json:"sealed_object_uri"`
	UnsealToken     string   `json:"unseal_token"`
	ExpiresAt       string   `json:"expires_at"`
	Provider        string   `json:"provider"`
	ResourceRefs    []string `json:"resource_refs"`
}

type kmsUnsealTokenResult struct {
	UnsealToken  string   `json:"unseal_token"`
	ExpiresAt    string   `json:"expires_at"`
	Provider     string   `json:"provider"`
	ResourceRefs []string `json:"resource_refs"`
}

func parseKMSExpiresAt(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, value)
}

func cloneStrings(values []string) []string {
	return append([]string(nil), values...)
}

var _ ports.EncryptionProvider = (*KMSSM4HTTPEncryptionProvider)(nil)
