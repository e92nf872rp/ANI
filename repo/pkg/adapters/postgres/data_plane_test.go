package postgres

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kubercloud/ani/pkg/ports"
)

func TestNewSQLDataPlaneSatisfiesPort(t *testing.T) {
	_ = ports.SQLDataPlane((*SQLDataPlane)(nil))
}

func TestValidateQueryRequest(t *testing.T) {
	tenantID := uuid.New()
	tests := []struct {
		name    string
		req     ports.DataPlaneQueryRequest
		wantErr bool
	}{
		{
			name:    "empty sql rejected",
			req:     ports.DataPlaneQueryRequest{SQL: "  ", Role: ports.DataPlaneRoleTenant, TenantID: tenantID},
			wantErr: true,
		},
		{
			name:    "unsupported role rejected",
			req:     ports.DataPlaneQueryRequest{SQL: "SELECT 1", Role: ports.DataPlaneRole("admin"), TenantID: tenantID},
			wantErr: true,
		},
		{
			name:    "tenant role requires tenant id",
			req:     ports.DataPlaneQueryRequest{SQL: "SELECT 1", Role: ports.DataPlaneRoleTenant, TenantID: uuid.Nil},
			wantErr: true,
		},
		{
			name:    "tenant role with tenant id ok",
			req:     ports.DataPlaneQueryRequest{SQL: "SELECT 1", Role: ports.DataPlaneRoleTenant, TenantID: tenantID},
			wantErr: false,
		},
		{
			name:    "service role does not require tenant id",
			req:     ports.DataPlaneQueryRequest{SQL: "SELECT 1", Role: ports.DataPlaneRoleService},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateQueryRequest(tt.req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateQueryRequest() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDataPlaneQueryErrorMapping(t *testing.T) {
	if got := dataPlaneQueryError(nil); got != nil {
		t.Fatalf("dataPlaneQueryError(nil) = %v, want nil", got)
	}
	if got := dataPlaneQueryError(fmt.Errorf("wrapped: %w", pgx.ErrNoRows)); !errors.Is(got, ports.ErrNotFound) {
		t.Fatalf("ErrNoRows origin should map to ErrNotFound, got %v", got)
	}
	// unknown error maps to ErrInvalid so the handler can return 400
	if got := dataPlaneQueryError(errors.New("boom")); !errors.Is(got, ports.ErrInvalid) {
		t.Fatalf("unknown error should map to ErrInvalid, got %v", got)
	}
}

func TestNullUUIDAndNullStr(t *testing.T) {
	if got := nullUUID(uuid.Nil); got != nil {
		t.Fatalf("nullUUID(uuid.Nil) = %v, want nil", got)
	}
	if got := nullUUID(uuid.New()); got == nil {
		t.Fatal("nullUUID(non-nil) should not be nil")
	}
	if got := nullStr(""); got != nil {
		t.Fatalf("nullStr(\"\") = %v, want nil", got)
	}
	if got := nullStr("x"); got == nil {
		t.Fatal("nullStr(non-empty) should not be nil")
	}
}

func TestNormalizeJSONValue(t *testing.T) {
	u := uuid.New()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		in   any
		want any
	}{
		{name: "nil passthrough", in: nil, want: nil},
		{name: "bool passthrough", in: true, want: true},
		{name: "string passthrough", in: "x", want: "x"},
		{name: "int64 passthrough", in: int64(42), want: int64(42)},
		{name: "float64 passthrough", in: 1.5, want: 1.5},
		{name: "bytes to base64", in: []byte{0x01, 0x02, 0xff}, want: "AQL/"},
		{name: "time to rfc3339", in: now, want: "2026-08-10T12:00:00Z"},
		{name: "uuid to string", in: u, want: u.String()},
		{name: "16byte to uuid string", in: [16]byte(u), want: u.String()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeJSONValue(tt.in); got != tt.want {
				t.Fatalf("normalizeJSONValue(%v) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

// fakeDataPlaneRows is a minimal pgx.Rows stub for exercising collectRows'
// row-limit logic without a live database.
type fakeDataPlaneRows struct {
	cols   []pgconn.FieldDescription
	rows   int
	values [][]any
	idx    int
	closed bool
}

func (f *fakeDataPlaneRows) FieldDescriptions() []pgconn.FieldDescription { return f.cols }
func (f *fakeDataPlaneRows) Next() bool {
	if f.closed || f.idx >= f.rows {
		return false
	}
	f.idx++
	return true
}
func (f *fakeDataPlaneRows) Values() ([]any, error) { return f.values[f.idx-1], nil }
func (f *fakeDataPlaneRows) Scan(dest ...any) error { return nil }
func (f *fakeDataPlaneRows) RawValues() [][]byte    { return nil }
func (f *fakeDataPlaneRows) CommandTag() pgconn.CommandTag {
	return pgconn.NewCommandTag("SELECT 0")
}
func (f *fakeDataPlaneRows) Err() error { return nil }
func (f *fakeDataPlaneRows) Close()     { f.closed = true }
func (f *fakeDataPlaneRows) Conn() *pgx.Conn {
	return nil
}

func TestCollectRowsEnforcesRowLimit(t *testing.T) {
	cols := []pgconn.FieldDescription{{Name: "id"}}
	makeFake := func(n int) *fakeDataPlaneRows {
		f := &fakeDataPlaneRows{cols: cols, rows: n, values: make([][]any, n)}
		for i := range f.values {
			f.values[i] = []any{int64(i)}
		}
		return f
	}

	// Exactly the limit is fine.
	ok, err := collectRows(makeFake(10), 10)
	if err != nil {
		t.Fatalf("collectRows(10 rows, limit 10) should succeed, got %v", err)
	}
	if len(ok.Rows) != 10 {
		t.Fatalf("expected 10 rows, got %d", len(ok.Rows))
	}

	// Exceeding the limit errors with ErrPayloadTooLarge.
	_, err = collectRows(makeFake(20), 10)
	if err == nil {
		t.Fatal("collectRows should error when rows exceed the limit")
	}
	if !errors.Is(err, ports.ErrPayloadTooLarge) {
		t.Fatalf("expected ErrPayloadTooLarge, got %v", err)
	}
}

func TestCollectRowsPreservesColumnOrder(t *testing.T) {
	cols := []pgconn.FieldDescription{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	fake := &fakeDataPlaneRows{cols: cols, rows: 1, values: [][]any{{int64(1), "x", true}}}
	res, err := collectRows(fake, 10)
	if err != nil {
		t.Fatalf("collectRows failed: %v", err)
	}
	if len(res.Columns) != 3 || res.Columns[0] != "a" || res.Columns[1] != "b" || res.Columns[2] != "c" {
		t.Fatalf("unexpected column order: %v", res.Columns)
	}
	if res.Rows[0]["a"] != int64(1) || res.Rows[0]["b"] != "x" || res.Rows[0]["c"] != true {
		t.Fatalf("unexpected row values: %v", res.Rows[0])
	}
}

func TestPickColumnNormalizerFastPaths(t *testing.T) {
	u := uuid.New()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		oid  uint32
		in   any
		want any
	}{
		{oid: 16, in: true, want: true},                       // bool
		{oid: 23, in: int64(42), want: int64(42)},             // int4 -> int64 via pgx
		{oid: 701, in: 1.5, want: 1.5},                        // float8
		{oid: 25, in: "x", want: "x"},                         // text
		{oid: 17, in: []byte{0x01, 0x02, 0xff}, want: "AQL/"}, // bytea -> base64
		{oid: 2950, in: u, want: u.String()},                  // uuid (uuid.UUID)
		{oid: 2950, in: [16]byte(u), want: u.String()},        // uuid ([16]byte)
		{oid: 1184, in: now, want: "2026-08-10T12:00:00Z"},    // timestamptz
	}
	for _, tt := range tests {
		n := pickColumnNormalizer(tt.oid)
		if got := n(tt.in); got != tt.want {
			t.Fatalf("normalizer(oid=%d, %v) = %#v, want %#v", tt.oid, tt.in, got, tt.want)
		}
	}
}

func TestWithDataPlaneMaxRowsOption(t *testing.T) {
	d := NewSQLDataPlane(nil, WithDataPlaneMaxRows(5))
	if d.maxRows != 5 {
		t.Fatalf("expected maxRows=5, got %d", d.maxRows)
	}
	// Non-positive values are ignored, keeping the default.
	d2 := NewSQLDataPlane(nil, WithDataPlaneMaxRows(0))
	if d2.maxRows != defaultDataPlaneMaxRows {
		t.Fatalf("expected default maxRows=%d, got %d", defaultDataPlaneMaxRows, d2.maxRows)
	}
}

func TestWithDataPlaneLoggerOption(t *testing.T) {
	custom := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	d := NewSQLDataPlane(nil, WithDataPlaneLogger(custom))
	if d.logger != custom {
		t.Fatal("WithDataPlaneLogger did not set the logger")
	}
	// nil logger is ignored, keeping the default.
	d2 := NewSQLDataPlane(nil, WithDataPlaneLogger(nil))
	if d2.logger == nil {
		t.Fatal("default logger should not be nil")
	}
}
