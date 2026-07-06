package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

func (s *MetadataNetworkStore) ListVPCs(ctx context.Context, tenantID string) ([]ports.NetworkVPCRecord, error) {
	return listNetworkRecords(ctx, s, tenantID, `
		SELECT tenant_id::text, vpc_id, name, cidr, state, COALESCE(reason, ''), created_at, updated_at
		FROM network_vpcs
		WHERE tenant_id = $1::uuid AND state <> 'deleted'
		ORDER BY updated_at DESC
	`, scanNetworkVPC)
}

func (s *MetadataNetworkStore) GetVPC(ctx context.Context, tenantID string, vpcID string) (ports.NetworkVPCRecord, error) {
	return getNetworkRecord(ctx, s, tenantID, vpcID, `
		SELECT tenant_id::text, vpc_id, name, cidr, state, COALESCE(reason, ''), created_at, updated_at
		FROM network_vpcs
		WHERE tenant_id = $1::uuid AND vpc_id = $2 AND state <> 'deleted'
	`, scanNetworkVPC)
}

func (s *MetadataNetworkStore) ListSubnets(ctx context.Context, tenantID string) ([]ports.NetworkSubnetRecord, error) {
	return listNetworkRecords(ctx, s, tenantID, `
		SELECT tenant_id::text, subnet_id, vpc_id, name, cidr, COALESCE(gateway, ''), state, COALESCE(reason, ''), created_at, updated_at
		FROM network_subnets
		WHERE tenant_id = $1::uuid AND state <> 'deleted'
		ORDER BY updated_at DESC
	`, scanNetworkSubnet)
}

func (s *MetadataNetworkStore) GetSubnet(ctx context.Context, tenantID string, subnetID string) (ports.NetworkSubnetRecord, error) {
	return getNetworkRecord(ctx, s, tenantID, subnetID, `
		SELECT tenant_id::text, subnet_id, vpc_id, name, cidr, COALESCE(gateway, ''), state, COALESCE(reason, ''), created_at, updated_at
		FROM network_subnets
		WHERE tenant_id = $1::uuid AND subnet_id = $2 AND state <> 'deleted'
	`, scanNetworkSubnet)
}

func (s *MetadataNetworkStore) ListSecurityGroups(ctx context.Context, tenantID string) ([]ports.NetworkSecurityGroupRecord, error) {
	return listNetworkRecords(ctx, s, tenantID, `
		SELECT tenant_id::text, security_group_id, name, COALESCE(description, ''), rules, state, COALESCE(reason, ''), created_at, updated_at
		FROM network_security_groups
		WHERE tenant_id = $1::uuid AND state <> 'deleted'
		ORDER BY updated_at DESC
	`, scanNetworkSecurityGroup)
}

func (s *MetadataNetworkStore) GetSecurityGroup(ctx context.Context, tenantID string, securityGroupID string) (ports.NetworkSecurityGroupRecord, error) {
	return getNetworkRecord(ctx, s, tenantID, securityGroupID, `
		SELECT tenant_id::text, security_group_id, name, COALESCE(description, ''), rules, state, COALESCE(reason, ''), created_at, updated_at
		FROM network_security_groups
		WHERE tenant_id = $1::uuid AND security_group_id = $2 AND state <> 'deleted'
	`, scanNetworkSecurityGroup)
}

func (s *MetadataNetworkStore) ListLoadBalancers(ctx context.Context, tenantID string) ([]ports.NetworkLoadBalancerRecord, error) {
	return listNetworkRecords(ctx, s, tenantID, `
		SELECT tenant_id::text, load_balancer_id, name, vpc_id, COALESCE(subnet_id, ''), scheme, COALESCE(vip, ''), listeners, state, COALESCE(reason, ''), created_at, updated_at
		FROM network_load_balancers
		WHERE tenant_id = $1::uuid AND state <> 'deleted'
		ORDER BY updated_at DESC
	`, scanNetworkLoadBalancer)
}

func (s *MetadataNetworkStore) GetLoadBalancer(ctx context.Context, tenantID string, loadBalancerID string) (ports.NetworkLoadBalancerRecord, error) {
	return getNetworkRecord(ctx, s, tenantID, loadBalancerID, `
		SELECT tenant_id::text, load_balancer_id, name, vpc_id, COALESCE(subnet_id, ''), scheme, COALESCE(vip, ''), listeners, state, COALESCE(reason, ''), created_at, updated_at
		FROM network_load_balancers
		WHERE tenant_id = $1::uuid AND load_balancer_id = $2 AND state <> 'deleted'
	`, scanNetworkLoadBalancer)
}

func (s *MetadataNetworkStore) ListRoutes(ctx context.Context, request ports.NetworkRouteListRequest) ([]ports.NetworkRouteRecord, error) {
	if s.store == nil {
		return nil, ports.ErrNotConfigured
	}
	if strings.TrimSpace(request.TenantID) == "" {
		return nil, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	var records []ports.NetworkRouteRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		query := `
			SELECT tenant_id::text, route_id, vpc_id, destination_cidr, next_hop_type, next_hop_id,
				COALESCE(description, ''), state, COALESCE(provider, ''), real_provider, created_at
			FROM network_routes
			WHERE tenant_id = $1::uuid AND state <> 'deleted'
		`
		args := []any{request.TenantID}
		if vpcID := strings.TrimSpace(request.VPCID); vpcID != "" {
			query += " AND vpc_id = $2"
			args = append(args, vpcID)
		}
		query += " ORDER BY created_at DESC"
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var record ports.NetworkRouteRecord
			if err := scanNetworkRoute(rows, &record); err != nil {
				return err
			}
			records = append(records, record)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(records, func(i, j int) bool { return records[i].CreatedAt.After(records[j].CreatedAt) })
	return records, nil
}

func (s *MetadataNetworkStore) GetRoute(ctx context.Context, tenantID string, routeID string) (ports.NetworkRouteRecord, error) {
	return getNetworkRecord(ctx, s, tenantID, routeID, `
		SELECT tenant_id::text, route_id, vpc_id, destination_cidr, next_hop_type, next_hop_id,
			COALESCE(description, ''), state, COALESCE(provider, ''), real_provider, created_at
		FROM network_routes
		WHERE tenant_id = $1::uuid AND route_id = $2 AND state <> 'deleted'
	`, scanNetworkRoute)
}

func (s *MetadataNetworkStore) CountDeleteDependencies(ctx context.Context, request ports.NetworkResourceGetRequest, resourceKind string) ([]string, error) {
	if s.store == nil {
		return nil, ports.ErrNotConfigured
	}
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.ResourceID) == "" {
		return nil, fmt.Errorf("%w: tenant_id and resource id are required", ports.ErrInvalid)
	}
	checks := networkDependencyChecks(resourceKind)
	reasons := make([]string, 0, len(checks))
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		for _, check := range checks {
			var count int64
			if err := tx.QueryRow(ctx, check.query, request.TenantID, request.ResourceID).Scan(&count); err != nil {
				return err
			}
			if count > 0 {
				reasons = append(reasons, check.reason)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reasons, nil
}

type networkDependencyCheck struct {
	query  string
	reason string
}

func networkDependencyChecks(resourceKind string) []networkDependencyCheck {
	switch strings.TrimSpace(resourceKind) {
	case "vpc":
		return []networkDependencyCheck{
			{
				query:  "SELECT COUNT(*) FROM network_subnets WHERE tenant_id = $1::uuid AND vpc_id = $2 AND state <> 'deleted'",
				reason: "VPC still has associated subnets",
			},
			{
				query:  "SELECT COUNT(*) FROM network_load_balancers WHERE tenant_id = $1::uuid AND vpc_id = $2 AND state <> 'deleted'",
				reason: "VPC still has associated load balancers",
			},
			{
				query:  "SELECT COUNT(*) FROM network_routes WHERE tenant_id = $1::uuid AND vpc_id = $2 AND state <> 'deleted'",
				reason: "VPC still has associated routes",
			},
			{
				query:  "SELECT COUNT(*) FROM workload_instances WHERE tenant_id = $1::uuid AND vpc_id = $2 AND state NOT IN ('deleted', 'failed', 'terminated')",
				reason: "VPC is still used by instances",
			},
		}
	case "subnet":
		return []networkDependencyCheck{
			{
				query:  "SELECT COUNT(*) FROM network_load_balancers WHERE tenant_id = $1::uuid AND subnet_id = $2 AND state <> 'deleted'",
				reason: "Subnet is still used by load balancers",
			},
			{
				query:  "SELECT COUNT(*) FROM workload_instances WHERE tenant_id = $1::uuid AND subnet_id = $2 AND state NOT IN ('deleted', 'failed', 'terminated')",
				reason: "Subnet is still used by instances",
			},
		}
	default:
		return nil
	}
}

type networkRecordScanner[T any] func(ports.Row, *T) error

func listNetworkRecords[T any](ctx context.Context, store *MetadataNetworkStore, tenantID string, query string, scan networkRecordScanner[T]) ([]T, error) {
	if store.store == nil {
		return nil, ports.ErrNotConfigured
	}
	if strings.TrimSpace(tenantID) == "" {
		return nil, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	var records []T
	err := store.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, err := tx.Query(ctx, query, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var record T
			if err := scan(rows, &record); err != nil {
				return err
			}
			records = append(records, record)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

func getNetworkRecord[T any](ctx context.Context, store *MetadataNetworkStore, tenantID string, resourceID string, query string, scan networkRecordScanner[T]) (T, error) {
	var zero T
	if store.store == nil {
		return zero, ports.ErrNotConfigured
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(resourceID) == "" {
		return zero, fmt.Errorf("%w: tenant_id and resource id are required", ports.ErrInvalid)
	}
	var record T
	err := store.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		row := tx.QueryRow(ctx, query, tenantID, resourceID)
		return scan(row, &record)
	})
	if err != nil {
		return zero, err
	}
	return record, nil
}

func scanNetworkVPC(row ports.Row, record *ports.NetworkVPCRecord) error {
	var state string
	if err := row.Scan(&record.TenantID, &record.VPCID, &record.Name, &record.CIDR, &state, &record.Reason, &record.CreatedAt, &record.UpdatedAt); err != nil {
		return ports.ErrNotFound
	}
	record.State = ports.NetworkResourceState(state)
	return nil
}

func scanNetworkSubnet(row ports.Row, record *ports.NetworkSubnetRecord) error {
	var state string
	if err := row.Scan(&record.TenantID, &record.SubnetID, &record.VPCID, &record.Name, &record.CIDR, &record.Gateway, &state, &record.Reason, &record.CreatedAt, &record.UpdatedAt); err != nil {
		return ports.ErrNotFound
	}
	record.State = ports.NetworkResourceState(state)
	return nil
}

func scanNetworkSecurityGroup(row ports.Row, record *ports.NetworkSecurityGroupRecord) error {
	var state string
	var rulesJSON []byte
	if err := row.Scan(&record.TenantID, &record.SecurityGroupID, &record.Name, &record.Description, &rulesJSON, &state, &record.Reason, &record.CreatedAt, &record.UpdatedAt); err != nil {
		return ports.ErrNotFound
	}
	record.State = ports.NetworkResourceState(state)
	if len(rulesJSON) > 0 {
		if err := json.Unmarshal(rulesJSON, &record.Rules); err != nil {
			return fmt.Errorf("decode security group rules: %w", err)
		}
	}
	return nil
}

func scanNetworkLoadBalancer(row ports.Row, record *ports.NetworkLoadBalancerRecord) error {
	var state string
	var listenersJSON []byte
	if err := row.Scan(&record.TenantID, &record.LoadBalancerID, &record.Name, &record.VPCID, &record.SubnetID, &record.Scheme, &record.VIP, &listenersJSON, &state, &record.Reason, &record.CreatedAt, &record.UpdatedAt); err != nil {
		return ports.ErrNotFound
	}
	record.State = ports.NetworkResourceState(state)
	if len(listenersJSON) > 0 {
		if err := json.Unmarshal(listenersJSON, &record.Listeners); err != nil {
			return fmt.Errorf("decode load balancer listeners: %w", err)
		}
	}
	return nil
}

func scanNetworkRoute(row ports.Row, record *ports.NetworkRouteRecord) error {
	var state string
	if err := row.Scan(&record.TenantID, &record.RouteID, &record.VPCID, &record.DestinationCIDR, &record.NextHopType, &record.NextHopID, &record.Description, &state, &record.Provider, &record.RealProvider, &record.CreatedAt); err != nil {
		return ports.ErrNotFound
	}
	record.State = ports.NetworkResourceState(state)
	return nil
}
