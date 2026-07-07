package middleware

import "testing"

func TestInferPermission(t *testing.T) {
	tests := []struct {
		method       string
		path         string
		wantResource string
		wantAction   string
	}{
		{method: "GET", path: "/api/v1/tasks/task-1", wantResource: "tasks", wantAction: "get"},
		{method: "POST", path: "/api/v1/svc/models", wantResource: "models", wantAction: "create"},
		{method: "PATCH", path: "/api/v1/kb/kb-1", wantResource: "kb", wantAction: "update"},
		{method: "DELETE", path: "/api/v1/svc/models/model-1", wantResource: "models", wantAction: "delete"},
		{method: "POST", path: "/api/v1/instances/inst-1/exec", wantResource: "instances", wantAction: "exec"},
		{method: "GET", path: "/api/v1/instances/inst-1/exec/session-1", wantResource: "instances", wantAction: "exec"},
	}

	for _, tt := range tests {
		gotResource, gotAction := inferPermission(tt.method, tt.path)
		if gotResource != tt.wantResource || gotAction != tt.wantAction {
			t.Fatalf("inferPermission(%q, %q) = %q, %q; want %q, %q",
				tt.method, tt.path, gotResource, gotAction, tt.wantResource, tt.wantAction)
		}
	}
}
