package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

const networkStoreTenantID = "5dbb1d01-0000-4000-8000-000000000001"

func TestMetadataNetworkStoreUpsertsVPC(t *testing.T) {
	tx := &fakeMetadataTx{}
	store := NewMetadataNetworkStore(fakeMetadataStore{tx: tx}, WithNetworkStoreClock(func() time.Time {
		return time.Unix(100, 0)
	}))

	err := store.UpsertVPC(context.Background(), ports.NetworkVPCRecord{
		TenantID:  networkStoreTenantID,
		VPCID:     "vpc-test",
		Name:      "vpc-a",
		CIDR:      "10.30.0.0/16",
		State:     ports.NetworkResourceAvailable,
		Reason:    "created",
		CreatedAt: time.Unix(90, 0),
	})
	if err != nil {
		t.Fatalf("UpsertVPC() error = %v", err)
	}
	if !strings.Contains(tx.sql, "INSERT INTO network_vpcs") {
		t.Fatalf("sql = %q, want network_vpcs insert", tx.sql)
	}
	if got, want := tx.args[1], "vpc-test"; got != want {
		t.Fatalf("vpc_id arg = %v, want %s", got, want)
	}
	if got, want := tx.args[4], string(ports.NetworkResourceAvailable); got != want {
		t.Fatalf("state arg = %v, want %s", got, want)
	}
}

func TestMetadataNetworkStoreSerializesNestedNetworkResources(t *testing.T) {
	tx := &fakeMetadataTx{}
	store := NewMetadataNetworkStore(fakeMetadataStore{tx: tx}, WithNetworkStoreClock(func() time.Time {
		return time.Unix(100, 0)
	}))

	err := store.UpsertSecurityGroup(context.Background(), ports.NetworkSecurityGroupRecord{
		TenantID:        networkStoreTenantID,
		SecurityGroupID: "sg-test",
		Name:            "web-sg",
		Rules: []ports.NetworkSecurityGroupRule{
			{Direction: "ingress", Protocol: "tcp", PortRange: "443", CIDR: "0.0.0.0/0", Action: "allow"},
		},
		State: ports.NetworkResourceAvailable,
	})
	if err != nil {
		t.Fatalf("UpsertSecurityGroup() error = %v", err)
	}
	if !strings.Contains(tx.sql, "INSERT INTO network_security_groups") {
		t.Fatalf("sql = %q, want network_security_groups insert", tx.sql)
	}
	if got, want := tx.args[2], ""; got != want {
		t.Fatalf("vpc_id arg = %v, want empty (record carries no VPC)", got)
	}
	rules, ok := tx.args[5].(string)
	if !ok || !strings.Contains(rules, `"Protocol":"tcp"`) {
		t.Fatalf("rules arg = %#v, want serialized rule payload", tx.args[5])
	}

	err = store.UpsertLoadBalancer(context.Background(), ports.NetworkLoadBalancerRecord{
		TenantID:       networkStoreTenantID,
		LoadBalancerID: "lb-test",
		Name:           "web-lb",
		VPCID:          "vpc-test",
		Scheme:         "public",
		Listeners: []ports.NetworkLoadBalancerListener{
			{Protocol: "http", Port: 80, TargetPort: 8080},
		},
		State: ports.NetworkResourceAvailable,
	})
	if err != nil {
		t.Fatalf("UpsertLoadBalancer() error = %v", err)
	}
	if !strings.Contains(tx.sql, "INSERT INTO network_load_balancers") {
		t.Fatalf("sql = %q, want network_load_balancers insert", tx.sql)
	}
	listeners, ok := tx.args[7].(string)
	if !ok || !strings.Contains(listeners, `"TargetPort":8080`) {
		t.Fatalf("listeners arg = %#v, want serialized listener payload", tx.args[7])
	}
}

func TestLocalNetworkServicePersistsCreateAndDelete(t *testing.T) {
	// DeleteVPC 在 store 模式下先查持久层 VPC、再遍历关联资源（VPC-4 删除保护），
	// fake 需提供 GetVPC 行数据与各关联表的空结果集。
	tx := &fakeMetadataTx{
		row: fakeMetadataRow{values: []any{
			networkStoreTenantID, "vpc-persisted", "persisted-vpc", "",
			string(ports.NetworkResourceAvailable), "", time.Unix(100, 0), time.Unix(100, 0),
		}},
		queryRows: map[string]ports.Rows{
			"FROM network_subnets":         &fakeRows{},
			"FROM network_security_groups": &fakeRows{},
			"FROM network_load_balancers":  &fakeRows{},
			"FROM network_routes":          &fakeRows{},
		},
	}
	service := NewLocalNetworkService(
		WithNetworkResourceStore(NewMetadataNetworkStore(fakeMetadataStore{tx: tx})),
	)

	vpc, err := service.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{
		TenantID:       networkStoreTenantID,
		IdempotencyKey: "persisted-vpc",
		Name:           "persisted-vpc",
	})
	if err != nil {
		t.Fatalf("CreateVPC() error = %v", err)
	}
	if _, err := service.DeleteVPC(context.Background(), ports.NetworkResourceGetRequest{
		TenantID:   networkStoreTenantID,
		ResourceID: vpc.VPCID,
	}); err != nil {
		t.Fatalf("DeleteVPC() error = %v", err)
	}
	if len(tx.execs) != 2 {
		t.Fatalf("exec count = %d, want create and delete persistence writes", len(tx.execs))
	}
	if got, want := tx.args[4], string(ports.NetworkResourceDeleted); got != want {
		t.Fatalf("last persisted state = %v, want %s", got, want)
	}
}

func TestMetadataNetworkStoreUpsertsRoute(t *testing.T) {
	tx := &fakeMetadataTx{}
	store := NewMetadataNetworkStore(fakeMetadataStore{tx: tx}, WithNetworkStoreClock(func() time.Time {
		return time.Unix(100, 0)
	}))

	err := store.UpsertRoute(context.Background(), ports.NetworkRouteRecord{
		TenantID:        networkStoreTenantID,
		RouteID:         "rt-persisted",
		VPCID:           "vpc-persisted",
		DestinationCIDR: "10.250.0.0/16",
		NextHopType:     "gateway",
		NextHopID:       "10.244.180.1",
		Description:     "default route",
		State:           ports.NetworkResourceAvailable,
		Provider:        "kubeovn",
		RealProvider:    true,
		CreatedAt:       time.Unix(90, 0),
	})
	if err != nil {
		t.Fatalf("UpsertRoute() error = %v", err)
	}
	if !strings.Contains(tx.sql, "INSERT INTO network_routes") {
		t.Fatalf("sql = %q, want network_routes insert", tx.sql)
	}
	if got, want := tx.args[1], "rt-persisted"; got != want {
		t.Fatalf("route id arg = %v, want %s", got, want)
	}
	if got, want := tx.args[9], true; got != want {
		t.Fatalf("real provider arg = %v, want %v", got, want)
	}
}

func TestMetadataNetworkStoreUpdatesResourceState(t *testing.T) {
	tx := &fakeMetadataTx{}
	store := NewMetadataNetworkStore(fakeMetadataStore{tx: tx}, WithNetworkStoreClock(func() time.Time {
		return time.Unix(100, 0)
	}))

	err := store.UpdateResourceState(context.Background(), ports.NetworkResourceStateUpdateRequest{
		TenantID:     networkStoreTenantID,
		ResourceKind: "load-balancer",
		ResourceID:   "lb-test",
		State:        ports.NetworkResourceFailed,
		Reason:       "vip allocation failed",
		UpdatedAt:    time.Unix(120, 0),
	})
	if err != nil {
		t.Fatalf("UpdateResourceState() error = %v", err)
	}
	if !strings.Contains(tx.sql, "UPDATE network_load_balancers") {
		t.Fatalf("sql = %q, want network_load_balancers update", tx.sql)
	}
	if got, want := tx.args[1], "lb-test"; got != want {
		t.Fatalf("resource id arg = %v, want %s", got, want)
	}
	if got, want := tx.args[2], string(ports.NetworkResourceFailed); got != want {
		t.Fatalf("state arg = %v, want %s", got, want)
	}
}
