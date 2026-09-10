package main

import (
	"os"
	"testing"

	"github.com/kubercloud/ani/services/model-service/internal/importer"
)

func TestLoadArchiveLimitsUsesWorkspaceAlignedDefaults(t *testing.T) {
	for _, name := range []string{
		"MODEL_IMPORT_MAX_FILES",
		"MODEL_IMPORT_MAX_TOTAL_BYTES",
		"MODEL_IMPORT_MAX_FILE_BYTES",
		"MODEL_IMPORT_MAX_OUTPUT_BYTES",
	} {
		t.Setenv(name, "")
	}

	limits, err := loadArchiveLimits()
	if err != nil {
		t.Fatalf("loadArchiveLimits() error = %v", err)
	}
	want := importer.ArchiveLimits{
		MaxFiles:       10000,
		MaxTotalBytes:  3 << 30,
		MaxFileBytes:   3 << 30,
		MaxOutputBytes: 3 << 30,
	}
	if limits != want {
		t.Fatalf("limits = %+v, want %+v", limits, want)
	}
}

func TestLoadArchiveLimitsAcceptsBoundedOverrides(t *testing.T) {
	t.Setenv("MODEL_IMPORT_MAX_FILES", "12")
	t.Setenv("MODEL_IMPORT_MAX_TOTAL_BYTES", "1073741824")
	t.Setenv("MODEL_IMPORT_MAX_FILE_BYTES", "536870912")
	t.Setenv("MODEL_IMPORT_MAX_OUTPUT_BYTES", "805306368")

	limits, err := loadArchiveLimits()
	if err != nil {
		t.Fatalf("loadArchiveLimits() error = %v", err)
	}
	if limits.MaxFiles != 12 || limits.MaxTotalBytes != 1<<30 || limits.MaxFileBytes != 1<<29 || limits.MaxOutputBytes != 805306368 {
		t.Fatalf("limits = %+v, want bounded overrides", limits)
	}
}

func TestLoadArchiveLimitsRejectsUnsafeValues(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
	}{
		{name: "malformed", env: map[string]string{"MODEL_IMPORT_MAX_TOTAL_BYTES": "not-a-number"}},
		{name: "negative", env: map[string]string{"MODEL_IMPORT_MAX_FILE_BYTES": "-1"}},
		{name: "too-large", env: map[string]string{"MODEL_IMPORT_MAX_OUTPUT_BYTES": "4294967296"}},
		{name: "file-exceeds-total", env: map[string]string{
			"MODEL_IMPORT_MAX_TOTAL_BYTES": "100",
			"MODEL_IMPORT_MAX_FILE_BYTES":  "101",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{
				"MODEL_IMPORT_MAX_FILES",
				"MODEL_IMPORT_MAX_TOTAL_BYTES",
				"MODEL_IMPORT_MAX_FILE_BYTES",
				"MODEL_IMPORT_MAX_OUTPUT_BYTES",
			} {
				t.Setenv(name, "")
			}
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			if _, err := loadArchiveLimits(); err == nil {
				t.Fatal("loadArchiveLimits() succeeded for unsafe value")
			}
		})
	}
}

func TestLoadArchiveLimitsDoesNotReadSecrets(t *testing.T) {
	// Keep this test explicit so future configuration additions do not turn
	// archive policy into a credential-bearing env contract.
	if _, ok := os.LookupEnv("HF_TOKEN"); ok {
		t.Skip("test environment already exports HF_TOKEN")
	}
}
