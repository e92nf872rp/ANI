package authz

import (
	"net/http"
	"strings"
	"testing"
)

func TestTargetRegistryFailsClosed(t *testing.T) {
	if _, err := NewTargetOperationRegistry(""); !IsTargetRegistryFailure(err, TargetReasonPolicyMismatch) {
		t.Fatalf("empty revision error = %v", err)
	}
	if _, err := NewTargetOperationRegistry("sha256:stale"); !IsTargetRegistryFailure(err, TargetReasonPolicyMismatch) {
		t.Fatalf("stale revision error = %v", err)
	}

	registry, err := NewTargetOperationRegistry(TargetPolicyRevision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Lookup("GET", "/api/v1/not-registered", TargetPolicyRevision); !IsTargetRegistryFailure(err, TargetReasonOperationUnregistered) {
		t.Fatalf("unknown route error = %v", err)
	}
	if _, err := registry.LookupOperation("notRegistered", TargetPolicyRevision); !IsTargetRegistryFailure(err, TargetReasonOperationUnregistered) {
		t.Fatalf("unknown operation error = %v", err)
	}
}

func TestTargetRegistryDecisionAndObligationCompleteness(t *testing.T) {
	registry, err := NewTargetOperationRegistry(TargetPolicyRevision)
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.byRoute) == 0 || len(registry.byRoute) != len(registry.byOperation) {
		t.Fatalf("registry counts routes=%d operations=%d", len(registry.byRoute), len(registry.byOperation))
	}
	if got := len(registry.byRoute); got != 298 {
		t.Fatalf("frozen operation count = %d, want 298", got)
	}
	classifications := map[TargetAuthClassification]int{}
	owners := map[TargetBackendOwner]int{}
	decisionCalls := map[int]int{}
	decisions := map[TargetIAMDecision]int{}
	for key, policy := range registry.byRoute {
		if err := validateTargetOperationPolicy(key, policy); err != nil {
			t.Fatal(err)
		}
		classifications[policy.AuthClassification]++
		owners[policy.BackendOwner]++
		decisionCalls[policy.IAMDecisionCalls]++
		decisions[policy.IAMDecision]++
	}
	if classifications[TargetAuthClassificationPublic] != 11 || classifications[TargetAuthClassificationAuthenticated] != 9 || classifications[TargetAuthClassificationAuthorized] != 278 {
		t.Fatalf("frozen auth classification counts = %#v", classifications)
	}
	if owners[TargetOwnerGateway] != 3 || owners[TargetOwnerCoreControl] != 208 || owners[TargetOwnerIAM] != 87 {
		t.Fatalf("frozen owner counts = %#v", owners)
	}
	if decisionCalls[0] != 11 || decisionCalls[1] != 287 || len(decisionCalls) != 2 {
		t.Fatalf("frozen IAM decision-call counts = %#v", decisionCalls)
	}
	if decisions[TargetIAMDecisionNone] != 11 || decisions[TargetIAMDecisionValidatePrincipal] != 9 || decisions[TargetIAMDecisionCheckPermission] != 278 || len(decisions) != 3 {
		t.Fatalf("frozen IAM decision kinds = %#v", decisions)
	}
}

func TestTargetRegistryLookupReturnsImmutableCopies(t *testing.T) {
	registry, err := NewTargetOperationRegistry(TargetPolicyRevision)
	if err != nil {
		t.Fatal(err)
	}
	first, err := registry.LookupOperation("getInstance", TargetPolicyRevision)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.PermissionActions) == 0 {
		t.Fatal("getInstance has no permission actions")
	}
	want := first.PermissionActions[0]
	first.PermissionActions[0] = TargetPermissionAction("forged")
	second, err := registry.LookupOperation("getInstance", TargetPolicyRevision)
	if err != nil {
		t.Fatal(err)
	}
	if second.PermissionActions[0] != want {
		t.Fatalf("registry action mutated through lookup: got %q, want %q", second.PermissionActions[0], want)
	}
}

func TestTargetStableErrorMapping(t *testing.T) {
	want := map[int]TargetStableError{
		401: {ResponseComponent: "Unauthorized", Code: "CREDENTIAL_INVALID"},
		403: {ResponseComponent: "Forbidden", Code: "PERMISSION_DENIED"},
		409: {ResponseComponent: "Conflict", Code: "IDEMPOTENCY_CONFLICT"},
		429: {ResponseComponent: "RateLimitExceeded", Code: "AUTH_RATE_LIMITED"},
		503: {ResponseComponent: "ServiceUnavailable", Code: "IAM_UNAVAILABLE"},
		504: {ResponseComponent: "GatewayTimeout", Code: "IAM_TIMEOUT"},
	}
	for status, expected := range want {
		got, ok := TargetStableErrorForStatus(status)
		if !ok || got != expected {
			t.Fatalf("stable error %d = %#v, %t; want %#v", status, got, ok, expected)
		}
	}
	if _, ok := TargetStableErrorForStatus(500); ok {
		t.Fatal("unexpected default mapping for status 500")
	}
	for _, reason := range []string{TargetReasonPolicyMismatch, TargetReasonOperationUnregistered} {
		got, ok := TargetStableErrorForReason(reason)
		if !ok || got.HTTPStatus != 503 || got.ResponseComponent != "ServiceUnavailable" || got.Code != reason {
			t.Fatalf("stable reason %s = %#v, %t", reason, got, ok)
		}
	}
	if _, ok := TargetStableErrorForReason("UNKNOWN"); ok {
		t.Fatal("unexpected default reason mapping")
	}
}

func TestBuildTargetForwardHeadersStripsForgeryBeforeInjection(t *testing.T) {
	client := make(http.Header)
	client.Set("X-Ani-Principal-Id", "forged")
	client.Set("x-ANI-Roles", "platform-admin")
	client.Set("X-Ani-Permissions", "*")
	client["x-ani-raw-lowercase"] = []string{"forged"}
	client["X-ANI-RAW-UPPERCASE"] = []string{"forged"}
	client.Set("X-Request-ID", "request-1")
	forward, err := BuildTargetForwardHeaders(client, map[string]string{
		"x-ani-principal-id": "principal-1",
		"x-ani-decision-id":  "decision-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := forward.Get("x-ani-principal-id"); got != "principal-1" {
		t.Fatalf("principal header = %q", got)
	}
	if got := forward.Get("x-ani-decision-id"); got != "decision-1" {
		t.Fatalf("decision header = %q", got)
	}
	if got := forward.Get("x-ani-roles"); got != "" {
		t.Fatalf("roles header survived: %q", got)
	}
	if got := forward.Get("x-ani-permissions"); got != "" {
		t.Fatalf("permissions header survived: %q", got)
	}
	for key := range forward {
		if len(key) >= len("x-ani-") && strings.EqualFold(key[:len("x-ani-")], "x-ani-") && key != "X-Ani-Principal-Id" && key != "X-Ani-Decision-Id" {
			t.Fatalf("forged x-ani header survived: %q", key)
		}
	}
	if got := forward.Get("x-request-id"); got != "request-1" {
		t.Fatalf("ordinary header = %q", got)
	}
}

func TestBuildTargetForwardHeadersRejectsUnregisteredInjection(t *testing.T) {
	_, err := BuildTargetForwardHeaders(http.Header{}, map[string]string{"x-ani-roles": "admin"})
	if err == nil {
		t.Fatal("expected unregistered trusted header to fail")
	}
}

func TestBuildTargetForwardHeadersRejectsDuplicateNormalizedInjection(t *testing.T) {
	_, err := BuildTargetForwardHeaders(http.Header{}, map[string]string{
		"X-Ani-Principal-Id": "principal-1",
		"x-ani-principal-id": "principal-2",
	})
	if err == nil {
		t.Fatal("expected duplicate normalized trusted header to fail")
	}
}
