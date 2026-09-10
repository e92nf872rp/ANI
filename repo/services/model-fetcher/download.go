package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Descriptor identifies one immutable model artifact.
type Descriptor struct {
	URL               string
	Filename          string
	ExpectedSize      int64
	ExpectedSHA256    string
	AllowInsecureHTTP bool
}

const maxDownloadSize = int64(1 << 40) // defensive bound when a descriptor is malformed

// modelDownloadHTTPTimeout bounds a presigned URL fetch even when the caller
// supplies a context without a deadline. It is a variable so unit tests can
// exercise timeout handling without waiting for the production bound.
var modelDownloadHTTPTimeout = 30 * time.Minute

// Download fetches an artifact into outputDir. The final file is either the
// complete verified artifact or is left unchanged.
func Download(ctx context.Context, d Descriptor, client *http.Client, outputDir string) error {
	d.ExpectedSHA256 = normalizeDigest(d.ExpectedSHA256)
	if d.URL == "" || d.Filename == "" || d.ExpectedSize < 0 || d.ExpectedSize > maxDownloadSize || !validDigest(d.ExpectedSHA256) {
		return errors.New("invalid download descriptor")
	}
	if err := validateDownloadURL(d.URL, d.AllowInsecureHTTP); err != nil {
		return err
	}
	if filepath.Base(d.Filename) != d.Filename || d.Filename == "." || d.Filename == ".." {
		return errors.New("invalid download filename")
	}
	if client == nil {
		client = http.DefaultClient
	}
	target := filepath.Join(outputDir, d.Filename)
	if matchingFile(target, d) {
		return nil
	}
	if err := os.MkdirAll(outputDir, 0750); err != nil {
		return errors.New("create download directory")
	}
	tmp, err := os.CreateTemp(outputDir, ".model-fetch-*")
	if err != nil {
		return errors.New("create temporary download file")
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, d.URL, nil)
	if err != nil {
		return errors.New("create download request")
	}
	request.Header.Set("Accept", "application/octet-stream")
	fetchClient := *client
	if modelDownloadHTTPTimeout <= 0 {
		return errors.New("download timeout is not configured")
	}
	if fetchClient.Timeout <= 0 || fetchClient.Timeout > modelDownloadHTTPTimeout {
		fetchClient.Timeout = modelDownloadHTTPTimeout
	}
	fetchClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	response, err := fetchClient.Do(request)
	if err != nil {
		return errors.New("download request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if response.StatusCode >= http.StatusMultipleChoices && response.StatusCode < http.StatusBadRequest {
			return errors.New("download redirect rejected")
		}
		return errors.New("download returned non-success status")
	}
	if response.ContentLength > d.ExpectedSize && d.ExpectedSize >= 0 {
		return errors.New("download size mismatch")
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(response.Body, d.ExpectedSize+1))
	if err != nil {
		return errors.New("download write failed")
	}
	if written != d.ExpectedSize {
		return errors.New("download size mismatch")
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), d.ExpectedSHA256) {
		return errors.New("download checksum mismatch")
	}
	if err := tmp.Sync(); err != nil {
		return errors.New("sync downloaded file")
	}
	if err := tmp.Close(); err != nil {
		return errors.New("close downloaded file")
	}
	if err := os.Rename(tmpName, target); err != nil {
		return errors.New("install downloaded file")
	}
	dir, err := os.Open(outputDir)
	if err != nil {
		return errors.New("open download directory")
	}
	syncErr := dir.Sync()
	_ = dir.Close()
	if syncErr != nil {
		return errors.New("sync download directory")
	}
	return nil
}

func validateDownloadURL(raw string, allowInsecureHTTP bool) error {
	value := strings.TrimSpace(raw)
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.Opaque != "" || u.User != nil || u.Fragment != "" {
		return errors.New("invalid download URL")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		if allowInsecureHTTP {
			return nil
		}
		return errors.New("insecure download URL is disabled")
	default:
		return errors.New("download URL scheme is not allowed")
	}
}

func validDigest(value string) bool {
	value = normalizeDigest(value)
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func normalizeDigest(value string) string {
	value = strings.TrimSpace(value)
	return strings.TrimPrefix(value, "sha256:")
}

func matchingFile(path string, d Descriptor) bool {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() != d.ExpectedSize {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, io.LimitReader(f, d.ExpectedSize+1))
	closeErr := f.Close()
	return copyErr == nil && closeErr == nil && strings.EqualFold(fmt.Sprintf("%x", h.Sum(nil)), d.ExpectedSHA256)
}
