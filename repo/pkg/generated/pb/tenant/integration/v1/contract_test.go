package tenantintegrationv1_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	tenantintegrationv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/integration/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

type errorContract struct {
	Domain  string      `json:"domain"`
	Reasons []errorRule `json:"reasons"`
}

type errorRule struct {
	Reason           string   `json:"reason"`
	GRPCCode         string   `json:"grpc_code"`
	RequiredMetadata []string `json:"required_metadata"`
}

type contractPins struct {
	SchemaVersion  string             `json:"schema_version"`
	IAMStartCommit string             `json:"iam_start_commit"`
	ANIStartCommit string             `json:"ani_start_commit"`
	PolicyRevision string             `json:"policy_revision"`
	Toolchain      map[string]toolPin `json:"toolchain"`
	Artifacts      map[string]string  `json:"artifacts"`
	Fixtures       map[string]string  `json:"fixtures"`
}

type toolPin struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

var expectedFixtureNames = []string{
	"core_error_contract.v1.json",
	"core_tenant_iam_bootstrap_requested.v1.json",
	"core_tenant_lifecycle_changed.v1.json",
	"core_tenant_lifecycle_heartbeat.v1.json",
	"core_tenant_lifecycle_snapshot_page.v1.json",
	"iam_check_permission.v1.json",
	"iam_error_contract.v1.json",
	"iam_password_login.v1.json",
}

func TestCoreProducerFixturesDecodeWithGeneratedTypes(t *testing.T) {
	cases := []struct {
		file    string
		message proto.Message
	}{
		{"core_tenant_lifecycle_changed.v1.json", &tenantintegrationv1.TenantLifecycleChanged{}},
		{"core_tenant_iam_bootstrap_requested.v1.json", &tenantintegrationv1.TenantIAMBootstrapRequested{}},
		{"core_tenant_lifecycle_heartbeat.v1.json", &tenantintegrationv1.TenantLifecycleHeartbeat{}},
		{"core_tenant_lifecycle_snapshot_page.v1.json", &tenantintegrationv1.TenantLifecycleSnapshotPage{}},
	}
	for _, test := range cases {
		t.Run(test.file, func(t *testing.T) {
			data := readFile(t, filepath.Join(fixtureDir(t), test.file))
			if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(data, test.message); err != nil {
				t.Fatalf("strictly decode producer fixture: %v", err)
			}
			if test.message.ProtoReflect().Descriptor().FullName() == "tenant.integration.v1.TenantLifecycleChanged" {
				message := test.message.(*tenantintegrationv1.TenantLifecycleChanged)
				if message.GetEnvelope().GetSchemaMajor() != 1 || message.GetEnvelope().GetAggregateVersion() == 0 {
					t.Fatalf("lifecycle version contract missing: %#v", message.GetEnvelope())
				}
			}
		})
	}
}

func TestIAMConsumerFixturesDecodeFromPinnedDescriptor(t *testing.T) {
	set := loadDescriptorSet(t, filepath.Join(repoRoot(t), "pkg/generated/pb/iam/v1/iam_descriptor.pb"))
	files, err := protodesc.NewFiles(set)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		file    string
		message protoreflect.FullName
	}{
		{"iam_password_login.v1.json", "iam.v1.PasswordLoginRequest"},
		{"iam_check_permission.v1.json", "iam.v1.CheckPermissionRequest"},
	}
	for _, test := range cases {
		t.Run(test.file, func(t *testing.T) {
			descriptor, err := files.FindDescriptorByName(test.message)
			if err != nil {
				t.Fatal(err)
			}
			message := dynamicpb.NewMessage(descriptor.(protoreflect.MessageDescriptor))
			if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(readFile(t, filepath.Join(fixtureDir(t), test.file)), message); err != nil {
				t.Fatalf("strictly decode consumer fixture: %v", err)
			}
		})
	}
}

func TestDescriptorInventoriesAndOwnership(t *testing.T) {
	service := tenantintegrationv1.File_tenant_integration_v1_tenant_iam_integration_proto.Services().ByName("TenantIAMIntegrationService")
	if service == nil {
		t.Fatal("missing Core-owned TenantIAMIntegrationService")
	}
	methods := []string{}
	for index := 0; index < service.Methods().Len(); index++ {
		methods = append(methods, string(service.Methods().Get(index).Name()))
	}
	sort.Strings(methods)
	if got, want := strings.Join(methods, ","), "BeginTenantLifecycleSnapshot,ListTenantLifecycleSnapshotPage"; got != want {
		t.Fatalf("Core snapshot methods = %s, want %s", got, want)
	}

	iam := loadDescriptorSet(t, filepath.Join(repoRoot(t), "pkg/generated/pb/iam/v1/iam_descriptor.pb"))
	services := map[string]int{}
	for _, file := range iam.File {
		for _, candidate := range file.Service {
			services[file.GetPackage()+"."+candidate.GetName()] = len(candidate.Method)
		}
	}
	want := map[string]int{
		"iam.v1.AuthenticationService": 15,
		"iam.v1.AuthorizationService":  1,
		"iam.v1.IAMAdminService":       53,
	}
	if len(services) != len(want) {
		t.Fatalf("IAM service inventory = %#v", services)
	}
	for name, methodCount := range want {
		if services[name] != methodCount {
			t.Fatalf("IAM service %s methods = %d, want %d", name, services[name], methodCount)
		}
	}
	if _, leaked := services["auth.v1.AuthService"]; leaked {
		t.Fatal("legacy auth.v1.AuthService leaked into IAM descriptor")
	}
}

func TestBootstrapFingerprintAndStableErrorInfo(t *testing.T) {
	fixture := &tenantintegrationv1.TenantIAMBootstrapRequested{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(readFile(t, filepath.Join(fixtureDir(t), "core_tenant_iam_bootstrap_requested.v1.json")), fixture); err != nil {
		t.Fatal(err)
	}
	payload := struct {
		Locale          string `json:"locale"`
		NormalizedEmail string `json:"normalized_email"`
		OperationID     string `json:"operation_id"`
		TenantID        string `json:"tenant_id"`
	}{
		fixture.GetIntendedAdministrator().GetLocale(),
		fixture.GetIntendedAdministrator().GetNormalizedEmail(),
		fixture.GetEnvelope().GetOperationId(),
		fixture.GetEnvelope().GetTenantId(),
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if want := "sha256:" + sha256Hex(canonical); fixture.GetPayloadFingerprint() != want {
		t.Fatalf("bootstrap fingerprint = %s, want %s", fixture.GetPayloadFingerprint(), want)
	}

	tests := map[string]struct {
		descriptor *descriptorpb.FileDescriptorSet
		enum       protoreflect.FullName
		prefix     string
	}{
		"iam_error_contract.v1.json": {
			descriptor: loadDescriptorSet(t, filepath.Join(repoRoot(t), "pkg/generated/pb/iam/v1/iam_descriptor.pb")),
			enum:       "iam.v1.IAMErrorReason",
			prefix:     "IAM_ERROR_REASON_",
		},
		"core_error_contract.v1.json": {
			descriptor: loadDescriptorSet(t, filepath.Join(repoRoot(t), "pkg/generated/pb/tenant/integration/v1/tenant_iam_integration_descriptor.pb")),
			enum:       "tenant.integration.v1.CoreIntegrationErrorReason",
			prefix:     "CORE_INTEGRATION_ERROR_REASON_",
		},
	}
	for name, test := range tests {
		var contract errorContract
		decodeJSON(t, filepath.Join(fixtureDir(t), name), &contract)
		if contract.Domain == "" || len(contract.Reasons) == 0 {
			t.Fatalf("incomplete error contract %s", name)
		}
		declaredReasons := enumReasons(t, test.descriptor, test.enum, test.prefix)
		seen := map[string]struct{}{}
		for _, rule := range contract.Reasons {
			if _, duplicate := seen[rule.Reason]; duplicate {
				t.Fatalf("duplicate reason %s", rule.Reason)
			}
			seen[rule.Reason] = struct{}{}
			if _, declared := declaredReasons[rule.Reason]; !declared {
				t.Fatalf("reason %s is absent from %s", rule.Reason, test.enum)
			}
			code, ok := grpcCode(rule.GRPCCode)
			if !ok {
				t.Fatalf("unknown gRPC code %q", rule.GRPCCode)
			}
			metadata := map[string]string{}
			for _, key := range rule.RequiredMetadata {
				metadata[key] = "fixture"
			}
			withDetails, err := status.New(code, rule.Reason).WithDetails(&errdetails.ErrorInfo{
				Reason:   rule.Reason,
				Domain:   contract.Domain,
				Metadata: metadata,
			})
			if err != nil {
				t.Fatal(err)
			}
			info, ok := status.FromProto(withDetails.Proto()).Details()[0].(*errdetails.ErrorInfo)
			if !ok || info.GetReason() != rule.Reason || info.GetDomain() != contract.Domain {
				t.Fatalf("invalid ErrorInfo round trip for %s", rule.Reason)
			}
		}
		if len(seen) != len(declaredReasons) {
			t.Fatalf("fixture reasons = %v, descriptor reasons = %v", sortedKeys(seen), sortedKeys(declaredReasons))
		}
	}
}

func TestImmutableContractPins(t *testing.T) {
	var pins contractPins
	decodeJSON(t, filepath.Join(repoRoot(t), "api/proto/tenant/integration/v1/contract_pins.json"), &pins)
	if pins.SchemaVersion != "ani.contract-pins/v1" || pins.IAMStartCommit != "5ff9f3cfe083b3b911bb076450abbbb967e82a37" || pins.ANIStartCommit != "a221a7b50c2cfdb13f04c13f154338d836a48af3" {
		t.Fatalf("unexpected immutable contract identity: %#v", pins)
	}
	if pins.PolicyRevision != "sha256:f222e2c6d3cd6442449cd722389d3d4fbfcdc7a0fee950c9d28385d3c264affa" {
		t.Fatalf("policy revision = %s", pins.PolicyRevision)
	}
	wantTools := map[string]toolPin{
		"buf":                {Version: "1.72.0", SHA256: "8720830e26a733da55bb89bcd3cb44849c0965fc0c44fb5d691cccdc64dca5af"},
		"protoc-gen-go":      {Version: "v1.36.12", SHA256: "7475078ca943fa552b4755a0b5dd84f4387905a08cb09a47696fd3683cc1c010"},
		"protoc-gen-go-grpc": {Version: "1.6.2", SHA256: "aa1fabbfc27b12d81182864a3f90b47aee907bced808e17e275c5b18c9602b08"},
	}
	if len(pins.Toolchain) != len(wantTools) {
		t.Fatalf("toolchain pins = %#v", pins.Toolchain)
	}
	for name, want := range wantTools {
		if got := pins.Toolchain[name]; got != want {
			t.Fatalf("toolchain %s = %#v, want %#v", name, got, want)
		}
	}
	assertFixtureInventory(t, pins.Fixtures)
	artifacts := map[string]string{
		"iam_descriptor":  filepath.Join(repoRoot(t), "pkg/generated/pb/iam/v1/iam_descriptor.pb"),
		"core_descriptor": filepath.Join(repoRoot(t), "pkg/generated/pb/tenant/integration/v1/tenant_iam_integration_descriptor.pb"),
	}
	for name, path := range artifacts {
		if got, want := sha256Hex(readFile(t, path)), pins.Artifacts[name]; got != want {
			t.Fatalf("artifact %s digest = %s, want %s", name, got, want)
		}
	}
	for name, want := range pins.Fixtures {
		if got := sha256Hex(readFile(t, filepath.Join(fixtureDir(t), name))); got != want {
			t.Fatalf("fixture %s digest = %s, want %s", name, got, want)
		}
	}
}

func assertFixtureInventory(t *testing.T, pins map[string]string) {
	t.Helper()
	want := append([]string(nil), expectedFixtureNames...)
	sort.Strings(want)
	if got := sortedKeys(pins); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("pinned fixtures = %v, want %v", got, want)
	}
	entries, err := os.ReadDir(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			got = append(got, entry.Name())
		}
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("fixture files = %v, want %v", got, want)
	}
}

func enumReasons(t *testing.T, set *descriptorpb.FileDescriptorSet, fullName protoreflect.FullName, prefix string) map[string]struct{} {
	t.Helper()
	files, err := protodesc.NewFiles(set)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := files.FindDescriptorByName(fullName)
	if err != nil {
		t.Fatal(err)
	}
	enum, ok := descriptor.(protoreflect.EnumDescriptor)
	if !ok {
		t.Fatalf("%s is not an enum", fullName)
	}
	reasons := map[string]struct{}{}
	for index := 0; index < enum.Values().Len(); index++ {
		name := string(enum.Values().Get(index).Name())
		if strings.HasSuffix(name, "_UNSPECIFIED") {
			continue
		}
		if !strings.HasPrefix(name, prefix) {
			t.Fatalf("enum value %s lacks prefix %s", name, prefix)
		}
		reasons[strings.TrimPrefix(name, prefix)] = struct{}{}
	}
	return reasons
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func loadDescriptorSet(t *testing.T, path string) *descriptorpb.FileDescriptorSet {
	t.Helper()
	set := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(readFile(t, path), set); err != nil {
		t.Fatal(err)
	}
	return set
}

func decodeJSON(t *testing.T, path string, value any) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(string(readFile(t, path))))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func fixtureDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "api/proto/tenant/integration/v1/testdata")
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "../../../../../.."))
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func grpcCode(name string) (codes.Code, bool) {
	values := map[string]codes.Code{
		"INVALID_ARGUMENT":    codes.InvalidArgument,
		"UNAUTHENTICATED":     codes.Unauthenticated,
		"PERMISSION_DENIED":   codes.PermissionDenied,
		"NOT_FOUND":           codes.NotFound,
		"ALREADY_EXISTS":      codes.AlreadyExists,
		"ABORTED":             codes.Aborted,
		"FAILED_PRECONDITION": codes.FailedPrecondition,
		"RESOURCE_EXHAUSTED":  codes.ResourceExhausted,
		"UNAVAILABLE":         codes.Unavailable,
		"DEADLINE_EXCEEDED":   codes.DeadlineExceeded,
	}
	code, ok := values[name]
	return code, ok
}
