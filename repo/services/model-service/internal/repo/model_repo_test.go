package repo

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/kubercloud/ani/pkg/types"
)

func TestModelQueriesContainExplicitTenantFence(t *testing.T) {
	checks := map[string][]string{
		"get":                            {getModelByIDSQL, "id=$1", "tenant_id=$2"},
		"get-version":                    {getModelVersionByIDSQL, "v.id=$1", "m.tenant_id=$2", "m.status <> 'deleted'"},
		"list":                           {listModelsBaseWhere, "tenant_id=$1"},
		"delete":                         {softDeleteModelSQL, "id=$1", "tenant_id=$2"},
		"create-version":                 {createModelVersionSQL, "SELECT $3", "FROM models", "id=$1", "tenant_id=$2", "status <> 'deleted'"},
		"create-version-parent-update":   {updateModelAfterVersionSQL, "id=$1", "tenant_id=$2", "+$3", "status <> 'deleted'"},
		"list-versions-parent-ownership": {listModelVersionsParentSQL, "SELECT id", "FROM models", "id=$1", "tenant_id=$2", "status <> 'deleted'"},
		"list-versions":                  {listModelVersionsSQL, "JOIN models", "m.tenant_id=$2"},
	}
	for name, check := range checks {
		sql := strings.Join(strings.Fields(check[0]), " ")
		for _, fragment := range check[1:] {
			if !strings.Contains(sql, fragment) {
				t.Errorf("%s SQL lacks %q: %s", name, fragment, sql)
			}
		}
	}
}

func TestBuildListModelsQueriesKeepTenantArgumentFirst(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	cursorTime := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	cursorID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	where, args, err := buildListModelsFilter(ListFilter{
		TenantID: tenantID,
		Status:   "ready",
		Cursor:   types.EncodeCursor(cursorTime, cursorID),
	})
	if err != nil {
		t.Fatalf("buildListModelsFilter: %v", err)
	}
	if got := strings.Join(strings.Fields(where), " "); got != "WHERE tenant_id=$1 AND status <> 'deleted' AND status=$2 AND (created_at, id) < ($3, $4)" {
		t.Fatalf("where = %q", got)
	}
	wantArgs := []any{tenantID, "ready", cursorTime, cursorID}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
	countSQL := strings.Join(strings.Fields(buildCountModelsSQL(where)), " ")
	if !strings.Contains(countSQL, "FROM models WHERE tenant_id=$1") {
		t.Fatalf("count SQL lost tenant where: %s", countSQL)
	}
	listSQL := strings.Join(strings.Fields(buildListModelsSQL(where, len(args)+1)), " ")
	if !strings.Contains(listSQL, "FROM models WHERE tenant_id=$1") || !strings.Contains(listSQL, "LIMIT $5") {
		t.Fatalf("list SQL has wrong tenant/limit placeholders: %s", listSQL)
	}
}

type stubQueryRower struct {
	sql  string
	args []any
	err  error
}

func (s *stubQueryRower) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	s.sql = sql
	s.args = args
	return stubRow{err: s.err}
}

type stubRow struct{ err error }

func (r stubRow) Scan(...any) error { return r.err }

func TestListVersionsRequiresOwnedParent(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	query := &stubQueryRower{err: pgx.ErrNoRows}

	err := requireModelOwned(context.Background(), query, tenantID, modelID)
	if !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
	if got := strings.Join(strings.Fields(query.sql), " "); got != "SELECT id FROM models WHERE id=$1 AND tenant_id=$2 AND status <> 'deleted'" {
		t.Fatalf("ownership SQL = %q", got)
	}
	if want := []any{modelID, tenantID}; !reflect.DeepEqual(query.args, want) {
		t.Fatalf("ownership args = %#v, want %#v", query.args, want)
	}
}

func TestZeroRowsMapToNotFound(t *testing.T) {
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	if err := mapQueryNoRows(pgx.ErrNoRows, "modelRepo.CreateVersion", modelID); !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("insert no rows error = %v, want ErrNotFound", err)
	}
	sentinel := fmt.Errorf("db down")
	if err := mapQueryNoRows(sentinel, "modelRepo.CreateVersion", modelID); !errors.Is(err, sentinel) {
		t.Fatalf("non-no-rows error = %v, want sentinel", err)
	}
	for _, operation := range []string{"modelRepo.CreateVersion", "modelRepo.SoftDelete"} {
		if err := requireRowsAffected(0, operation, modelID); !errors.Is(err, types.ErrNotFound) {
			t.Errorf("%s zero rows error = %v, want ErrNotFound", operation, err)
		}
		if err := requireRowsAffected(1, operation, modelID); err != nil {
			t.Errorf("%s one row error = %v", operation, err)
		}
	}
}
