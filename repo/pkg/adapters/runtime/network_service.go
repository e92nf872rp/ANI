package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

type LocalNetworkService struct {
	mu                 sync.RWMutex
	now                func() time.Time
	store              ports.NetworkResourceStore
	instances          ports.WorkloadInstanceStore
	providerRenderer   ports.NetworkProviderRenderer
	providerDryRun     ports.NetworkProviderDryRun
	providerApply      ports.NetworkProviderApply
	providerStatus     ports.NetworkProviderStatusReader
	providerExecution  NetworkProviderExecutionConfig
	vpcs               map[string]ports.NetworkVPCRecord
	subnets            map[string]ports.NetworkSubnetRecord
	securityGroup      map[string]ports.NetworkSecurityGroupRecord
	securityGroupRules map[string]ports.NetworkSecurityGroupRuleRecord
	securityGroupBinds map[string]ports.NetworkSecurityGroupBindingRecord
	loadBalancers      map[string]ports.NetworkLoadBalancerRecord
	routes             map[string]ports.NetworkRouteRecord
	vpcIdempotency     map[string]string
	subnetIdempotency  map[string]string
	securityGroupIdem  map[string]string
	securityRuleIdem   map[string]string
	securityBindIdem   map[string]string
	loadBalancerIdem   map[string]string
	routeIdempotency   map[string]string
}

type NetworkServiceOption func(*LocalNetworkService)

type NetworkProviderExecutionConfig struct {
	UserID          string
	PermissionProof string
}

func WithNetworkServiceClock(now func() time.Time) NetworkServiceOption {
	return func(service *LocalNetworkService) {
		if now != nil {
			service.now = now
		}
	}
}

func WithNetworkResourceStore(store ports.NetworkResourceStore) NetworkServiceOption {
	return func(service *LocalNetworkService) {
		service.store = store
	}
}

// WithNetworkInstanceStore 注入实例记录存储，用于安全组绑定派生视图：
// 实例侧"更换安全组"只更新实例自身记录，不写独立绑定表，安全组详情的
// 绑定查询必须按实例记录反查才能与真实绑定一致（测试缺陷 安全组-5）。
func WithNetworkInstanceStore(instances ports.WorkloadInstanceStore) NetworkServiceOption {
	return func(service *LocalNetworkService) {
		service.instances = instances
	}
}

func WithNetworkRouteProvider(
	renderer ports.NetworkProviderRenderer,
	dryRun ports.NetworkProviderDryRun,
	apply ports.NetworkProviderApply,
	status ports.NetworkProviderStatusReader,
	execution NetworkProviderExecutionConfig,
) NetworkServiceOption {
	return WithNetworkProvider(renderer, dryRun, apply, status, execution)
}

func WithNetworkProvider(
	renderer ports.NetworkProviderRenderer,
	dryRun ports.NetworkProviderDryRun,
	apply ports.NetworkProviderApply,
	status ports.NetworkProviderStatusReader,
	execution NetworkProviderExecutionConfig,
) NetworkServiceOption {
	return func(service *LocalNetworkService) {
		service.providerRenderer = renderer
		service.providerDryRun = dryRun
		service.providerApply = apply
		service.providerStatus = status
		service.providerExecution = execution
	}
}

func NewLocalNetworkService(options ...NetworkServiceOption) *LocalNetworkService {
	service := &LocalNetworkService{
		now:                func() time.Time { return time.Now().UTC() },
		vpcs:               map[string]ports.NetworkVPCRecord{},
		subnets:            map[string]ports.NetworkSubnetRecord{},
		securityGroup:      map[string]ports.NetworkSecurityGroupRecord{},
		securityGroupRules: map[string]ports.NetworkSecurityGroupRuleRecord{},
		securityGroupBinds: map[string]ports.NetworkSecurityGroupBindingRecord{},
		loadBalancers:      map[string]ports.NetworkLoadBalancerRecord{},
		routes:             map[string]ports.NetworkRouteRecord{},
		vpcIdempotency:     map[string]string{},
		subnetIdempotency:  map[string]string{},
		securityGroupIdem:  map[string]string{},
		securityRuleIdem:   map[string]string{},
		securityBindIdem:   map[string]string{},
		loadBalancerIdem:   map[string]string{},
		routeIdempotency:   map[string]string{},
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *LocalNetworkService) GetOverview(_ context.Context, request ports.NetworkOverviewRequest) (ports.NetworkOverviewRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	resources := map[string]ports.NetworkOverviewResourceSummary{
		"vpc":            {Kind: "vpc"},
		"subnet":         {Kind: "subnet"},
		"security_group": {Kind: "security_group"},
		"load_balancer":  {Kind: "load_balancer"},
		"route":          {Kind: "route"},
	}
	for _, record := range s.vpcs {
		addNetworkOverviewState(resources, "vpc", record.TenantID, request.TenantID, record.State)
	}
	for _, record := range s.subnets {
		addNetworkOverviewState(resources, "subnet", record.TenantID, request.TenantID, record.State)
	}
	for _, record := range s.securityGroup {
		addNetworkOverviewState(resources, "security_group", record.TenantID, request.TenantID, record.State)
	}
	for _, record := range s.loadBalancers {
		addNetworkOverviewState(resources, "load_balancer", record.TenantID, request.TenantID, record.State)
	}
	for _, record := range s.routes {
		addNetworkOverviewState(resources, "route", record.TenantID, request.TenantID, record.State)
	}
	return ports.NetworkOverviewRecord{
		Resources: resources,
		Capabilities: []ports.NetworkOverviewCapability{
			{Key: "vpcs", Label: "VPC", Status: "available", Path: "/networks/vpcs", Description: "VPC lifecycle management"},
			{Key: "subnets", Label: "Subnets", Status: "available", Path: "/networks/subnets", Description: "Subnet lifecycle management"},
			{Key: "security_groups", Label: "Security groups", Status: "available", Path: "/networks/security-groups", Description: "Security group lifecycle management"},
			{Key: "load_balancers", Label: "Load balancers", Status: "available", Path: "/networks/load-balancers", Description: "Load balancer lifecycle management"},
			{Key: "routes", Label: "Routes", Status: "available", Path: "/networks/routes", Description: "Route table entries"},
			{Key: "subnet_ip_allocations", Label: "Subnet IP allocations", Status: "available", Description: "Subnet address allocation visibility"},
			{Key: "security_group_rules", Label: "Security group rules", Status: "available", Description: "Security group rule lifecycle management"},
			{Key: "security_group_bindings", Label: "Security group bindings", Status: "available", Description: "Security group target bindings"},
		},
		CreateOrder: []string{"vpc", "subnet", "security_group", "load_balancer"},
		Relationships: []ports.NetworkOverviewRelationship{
			{Source: "vpc", Target: "subnet", Relation: "contains"},
			{Source: "vpc", Target: "load_balancer", Relation: "hosts"},
			{Source: "security_group", Target: "load_balancer", Relation: "can_bind"},
			{Source: "vpc", Target: "route", Relation: "owns"},
		},
		DeleteRisks: []ports.NetworkOverviewDeleteRisk{
			{Kind: "vpc", Risk: "Deleting a VPC affects its subnets, routes, and load balancers."},
			{Kind: "subnet", Risk: "Deleting a subnet affects addresses and dependent load balancers."},
			{Kind: "security_group", Risk: "Deleting a security group removes traffic policy from bound targets."},
			{Kind: "load_balancer", Risk: "Deleting a load balancer interrupts ingress traffic."},
			{Kind: "route", Risk: "Deleting a route can break reachability for matching CIDRs."},
		},
	}, nil
}

func (s *LocalNetworkService) CreateVPC(ctx context.Context, request ports.NetworkVPCCreateRequest) (ports.NetworkVPCRecord, error) {
	if err := requireNetworkTenantAndName(request.TenantID, request.Name); err != nil {
		return ports.NetworkVPCRecord{}, err
	}
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.NetworkVPCRecord{}, err
	}
	s.mu.Lock()
	if id, ok := s.vpcIdempotency[idemKey]; ok {
		if record, exists := s.vpcs[id]; exists {
			s.mu.Unlock()
			return record, nil
		}
	}
	now := s.now().UTC()
	providerConfigured := s.networkProviderConfigured()
	record := ports.NetworkVPCRecord{
		TenantID:  request.TenantID,
		VPCID:     "vpc_" + uuid.NewString(),
		Name:      strings.TrimSpace(request.Name),
		CIDR:      firstNetworkNonEmpty(request.CIDR, "10.0.0.0/16"),
		State:     ports.NetworkResourceAvailable,
		Reason:    "created by local network profile",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if providerConfigured {
		record.State = ports.NetworkResourcePending
		record.Reason = "pending provider apply"
	}
	s.vpcs[record.VPCID] = record
	s.vpcIdempotency[idemKey] = record.VPCID
	s.mu.Unlock()
	if err := s.upsertVPC(ctx, record); err != nil {
		return ports.NetworkVPCRecord{}, err
	}
	if !providerConfigured {
		return record, nil
	}
	applied, err := s.applyVPCProvider(ctx, record)
	if err != nil {
		return ports.NetworkVPCRecord{}, s.markVPCProviderFailed(ctx, record, err)
	}
	s.mu.Lock()
	if _, exists := s.vpcs[applied.VPCID]; exists {
		s.vpcs[applied.VPCID] = applied
	}
	s.mu.Unlock()
	if err := s.upsertVPC(ctx, applied); err != nil {
		return ports.NetworkVPCRecord{}, err
	}
	return applied, nil
}

func (s *LocalNetworkService) ListVPCs(ctx context.Context, request ports.NetworkResourceListRequest) ([]ports.NetworkVPCRecord, error) {
	if s.store != nil {
		items, err := s.store.ListVPCs(ctx, request.TenantID)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(request.Name) != "" {
			filtered := make([]ports.NetworkVPCRecord, 0, len(items))
			for _, record := range items {
				if strings.HasPrefix(record.Name, strings.TrimSpace(request.Name)) {
					filtered = append(filtered, record)
				}
			}
			items = filtered
		}
		if strings.TrimSpace(request.Keyword) != "" {
			filtered := make([]ports.NetworkVPCRecord, 0, len(items))
			for _, record := range items {
				if strings.HasPrefix(record.VPCID, strings.TrimSpace(request.Keyword)) {
					filtered = append(filtered, record)
				}
			}
			items = filtered
		}
		if request.State != "" {
			filtered := make([]ports.NetworkVPCRecord, 0, len(items))
			for _, record := range items {
				if record.State == request.State {
					filtered = append(filtered, record)
				}
			}
			items = filtered
		}
		sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
		return items, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ports.NetworkVPCRecord, 0, len(s.vpcs))
	for _, record := range s.vpcs {
		if record.TenantID != request.TenantID || record.State == ports.NetworkResourceDeleted {
			continue
		}
		if strings.TrimSpace(request.Name) != "" && !strings.HasPrefix(record.Name, strings.TrimSpace(request.Name)) {
			continue
		}
		if strings.TrimSpace(request.Keyword) != "" && !strings.HasPrefix(record.VPCID, strings.TrimSpace(request.Keyword)) {
			continue
		}
		if request.State != "" && record.State != request.State {
			continue
		}
		items = append(items, record)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items, nil
}

func (s *LocalNetworkService) GetVPC(ctx context.Context, request ports.NetworkResourceGetRequest) (ports.NetworkVPCRecord, error) {
	if s.store != nil {
		return s.store.GetVPC(ctx, request.TenantID, request.ResourceID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.vpcs[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.NetworkResourceDeleted {
		return ports.NetworkVPCRecord{}, ports.ErrNotFound
	}
	return record, nil
}

func (s *LocalNetworkService) DeleteVPC(ctx context.Context, request ports.NetworkResourceGetRequest) (ports.NetworkVPCRecord, error) {
	if s.store != nil {
		// store 模式下内存 map 可能为空（网关重启后），VPC 与关联资源都以持久层为准。
		record, err := s.store.GetVPC(ctx, request.TenantID, request.ResourceID)
		if err != nil {
			return ports.NetworkVPCRecord{}, err
		}
		counts, err := s.vpcAssociationCounts(ctx, request.TenantID, request.ResourceID)
		if err != nil {
			return ports.NetworkVPCRecord{}, err
		}
		if err := vpcAssociationConflict(request.ResourceID, counts); err != nil {
			return ports.NetworkVPCRecord{}, err
		}
		now := s.now().UTC()
		record.State = ports.NetworkResourceDeleted
		record.Reason = "deleted by local network profile"
		record.UpdatedAt = now
		if err := s.upsertVPC(ctx, record); err != nil {
			return ports.NetworkVPCRecord{}, err
		}
		s.mu.Lock()
		if existing, ok := s.vpcs[record.VPCID]; ok && existing.TenantID == record.TenantID {
			s.vpcs[record.VPCID] = record
		}
		s.mu.Unlock()
		return record, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.vpcs[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.NetworkResourceDeleted {
		return ports.NetworkVPCRecord{}, ports.ErrNotFound
	}
	counts := s.vpcAssociationCountsMemory(request.TenantID, request.ResourceID)
	if err := vpcAssociationConflict(request.ResourceID, counts); err != nil {
		return ports.NetworkVPCRecord{}, err
	}
	now := s.now().UTC()
	record.State = ports.NetworkResourceDeleted
	record.Reason = "deleted by local network profile"
	record.UpdatedAt = now
	s.vpcs[record.VPCID] = record
	if err := s.upsertVPC(ctx, record); err != nil {
		return ports.NetworkVPCRecord{}, err
	}
	return record, nil
}

// networkVPCAssociationCounts 汇总 VPC 下各类存活关联资源数量（VPC-4 删除保护）。
type networkVPCAssociationCounts struct {
	Subnets        int
	SecurityGroups int
	LoadBalancers  int
	Routes         int
}

func (c networkVPCAssociationCounts) total() int {
	return c.Subnets + c.SecurityGroups + c.LoadBalancers + c.Routes
}

// vpcAssociationCounts 统计 VPC 下的存活关联资源；store 模式查持久层
// （内存 map 重启后为空，不能作为判断依据），与 resolveVPCForValidation 同模式。
func (s *LocalNetworkService) vpcAssociationCounts(ctx context.Context, tenantID string, vpcID string) (networkVPCAssociationCounts, error) {
	if s.store == nil {
		return s.vpcAssociationCountsMemory(tenantID, vpcID), nil
	}
	counts := networkVPCAssociationCounts{}
	subnets, err := s.store.ListSubnets(ctx, tenantID)
	if err != nil {
		return counts, err
	}
	for _, record := range subnets {
		if record.VPCID == vpcID {
			counts.Subnets++
		}
	}
	groups, err := s.store.ListSecurityGroups(ctx, tenantID)
	if err != nil {
		return counts, err
	}
	for _, record := range groups {
		if record.VPCID == vpcID {
			counts.SecurityGroups++
		}
	}
	balancers, err := s.store.ListLoadBalancers(ctx, tenantID)
	if err != nil {
		return counts, err
	}
	for _, record := range balancers {
		if record.VPCID == vpcID {
			counts.LoadBalancers++
		}
	}
	routes, err := s.store.ListRoutes(ctx, tenantID)
	if err != nil {
		return counts, err
	}
	for _, record := range routes {
		if record.VPCID == vpcID {
			counts.Routes++
		}
	}
	return counts, nil
}

// vpcAssociationCountsMemory 内存模式下的关联计数（须在持锁状态下调用）。
func (s *LocalNetworkService) vpcAssociationCountsMemory(tenantID string, vpcID string) networkVPCAssociationCounts {
	counts := networkVPCAssociationCounts{}
	for _, record := range s.subnets {
		if record.TenantID == tenantID && record.VPCID == vpcID && record.State != ports.NetworkResourceDeleted {
			counts.Subnets++
		}
	}
	for _, record := range s.securityGroup {
		if record.TenantID == tenantID && record.VPCID == vpcID && record.State != ports.NetworkResourceDeleted {
			counts.SecurityGroups++
		}
	}
	for _, record := range s.loadBalancers {
		if record.TenantID == tenantID && record.VPCID == vpcID && record.State != ports.NetworkResourceDeleted {
			counts.LoadBalancers++
		}
	}
	for _, record := range s.routes {
		if record.TenantID == tenantID && record.VPCID == vpcID && record.State != ports.NetworkResourceDeleted {
			counts.Routes++
		}
	}
	return counts
}

// vpcAssociationConflict 存在存活关联资源时禁止删除 VPC（方案 A：防御式保护），
// 错误消息列出各类数量，前端据此提示先清理下级资源。
func vpcAssociationConflict(vpcID string, counts networkVPCAssociationCounts) error {
	if counts.total() == 0 {
		return nil
	}
	return fmt.Errorf(
		"%w: cannot delete VPC %s: %d subnet(s), %d security group(s), %d load balancer(s), %d route(s) still exist; delete them first",
		ports.ErrConflict, vpcID, counts.Subnets, counts.SecurityGroups, counts.LoadBalancers, counts.Routes)
}

func (s *LocalNetworkService) CreateSubnet(ctx context.Context, request ports.NetworkSubnetCreateRequest) (ports.NetworkSubnetRecord, error) {
	if err := requireNetworkTenantAndName(request.TenantID, request.Name); err != nil {
		return ports.NetworkSubnetRecord{}, err
	}
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.NetworkSubnetRecord{}, err
	}
	if strings.TrimSpace(request.VPCID) == "" {
		return ports.NetworkSubnetRecord{}, fmt.Errorf("%w: vpc_id is required", ports.ErrInvalid)
	}
	s.mu.Lock()
	if id, ok := s.subnetIdempotency[idemKey]; ok {
		if record, exists := s.subnets[id]; exists {
			s.mu.Unlock()
			return record, nil
		}
	}
	now := s.now().UTC()
	providerConfigured := s.networkProviderConfigured()
	record := ports.NetworkSubnetRecord{
		TenantID:  request.TenantID,
		SubnetID:  "subnet_" + uuid.NewString(),
		VPCID:     strings.TrimSpace(request.VPCID),
		Name:      strings.TrimSpace(request.Name),
		CIDR:      firstNetworkNonEmpty(request.CIDR, "10.0.1.0/24"),
		Gateway:   strings.TrimSpace(request.Gateway),
		State:     ports.NetworkResourceAvailable,
		Reason:    "created by local network profile",
		CreatedAt: now,
		UpdatedAt: now,
	}
	vpc, ok := s.vpcs[record.VPCID]
	if !ok || vpc.TenantID != request.TenantID || vpc.State == ports.NetworkResourceDeleted {
		s.mu.Unlock()
		return ports.NetworkSubnetRecord{}, fmt.Errorf("%w: vpc not found", ports.ErrNotFound)
	}
	if providerConfigured {
		record.State = ports.NetworkResourcePending
		record.Reason = "pending provider apply"
	}
	s.subnets[record.SubnetID] = record
	s.subnetIdempotency[idemKey] = record.SubnetID
	s.mu.Unlock()
	if err := s.upsertSubnet(ctx, record); err != nil {
		return ports.NetworkSubnetRecord{}, err
	}
	if !providerConfigured {
		return record, nil
	}
	applied, err := s.applySubnetProvider(ctx, record)
	if err != nil {
		return ports.NetworkSubnetRecord{}, s.markSubnetProviderFailed(ctx, record, err)
	}
	s.mu.Lock()
	if _, exists := s.subnets[applied.SubnetID]; exists {
		s.subnets[applied.SubnetID] = applied
	}
	s.mu.Unlock()
	if err := s.upsertSubnet(ctx, applied); err != nil {
		return ports.NetworkSubnetRecord{}, err
	}
	return applied, nil
}

func (s *LocalNetworkService) ListSubnets(ctx context.Context, request ports.NetworkResourceListRequest) ([]ports.NetworkSubnetRecord, error) {
	if s.store != nil {
		items, err := s.store.ListSubnets(ctx, request.TenantID)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(request.VPCID) != "" {
			filtered := make([]ports.NetworkSubnetRecord, 0, len(items))
			for _, record := range items {
				if record.VPCID == strings.TrimSpace(request.VPCID) {
					filtered = append(filtered, record)
				}
			}
			items = filtered
		}
		if strings.TrimSpace(request.Name) != "" {
			filtered := make([]ports.NetworkSubnetRecord, 0, len(items))
			for _, record := range items {
				if strings.HasPrefix(record.Name, strings.TrimSpace(request.Name)) {
					filtered = append(filtered, record)
				}
			}
			items = filtered
		}
		if strings.TrimSpace(request.Keyword) != "" {
			filtered := make([]ports.NetworkSubnetRecord, 0, len(items))
			for _, record := range items {
				if strings.HasPrefix(record.SubnetID, strings.TrimSpace(request.Keyword)) {
					filtered = append(filtered, record)
				}
			}
			items = filtered
		}
		if request.State != "" {
			filtered := make([]ports.NetworkSubnetRecord, 0, len(items))
			for _, record := range items {
				if record.State == request.State {
					filtered = append(filtered, record)
				}
			}
			items = filtered
		}
		sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
		return items, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ports.NetworkSubnetRecord, 0, len(s.subnets))
	for _, record := range s.subnets {
		if record.TenantID != request.TenantID || record.State == ports.NetworkResourceDeleted {
			continue
		}
		if strings.TrimSpace(request.VPCID) != "" && record.VPCID != strings.TrimSpace(request.VPCID) {
			continue
		}
		if strings.TrimSpace(request.Name) != "" && !strings.HasPrefix(record.Name, strings.TrimSpace(request.Name)) {
			continue
		}
		if strings.TrimSpace(request.Keyword) != "" && !strings.HasPrefix(record.SubnetID, strings.TrimSpace(request.Keyword)) {
			continue
		}
		if request.State != "" && record.State != request.State {
			continue
		}
		items = append(items, record)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items, nil
}

func (s *LocalNetworkService) GetSubnet(ctx context.Context, request ports.NetworkResourceGetRequest) (ports.NetworkSubnetRecord, error) {
	if s.store != nil {
		return s.store.GetSubnet(ctx, request.TenantID, request.ResourceID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.subnets[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.NetworkResourceDeleted {
		return ports.NetworkSubnetRecord{}, ports.ErrNotFound
	}
	return record, nil
}

func (s *LocalNetworkService) DeleteSubnet(ctx context.Context, request ports.NetworkResourceGetRequest) (ports.NetworkSubnetRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.subnets[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.NetworkResourceDeleted {
		return ports.NetworkSubnetRecord{}, ports.ErrNotFound
	}
	record.State = ports.NetworkResourceDeleted
	record.Reason = "deleted by local network profile"
	record.UpdatedAt = s.now().UTC()
	s.subnets[record.SubnetID] = record
	if err := s.upsertSubnet(ctx, record); err != nil {
		return ports.NetworkSubnetRecord{}, err
	}
	return record, nil
}

func (s *LocalNetworkService) ListSubnetIPAllocations(_ context.Context, request ports.NetworkSubnetIPAllocationListRequest) ([]ports.NetworkSubnetIPAllocationRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	subnet, ok := s.subnets[strings.TrimSpace(request.SubnetID)]
	if !ok || subnet.TenantID != request.TenantID || subnet.State == ports.NetworkResourceDeleted {
		return nil, ports.ErrNotFound
	}
	return []ports.NetworkSubnetIPAllocationRecord{}, nil
}

func (s *LocalNetworkService) CreateSecurityGroup(ctx context.Context, request ports.NetworkSecurityGroupCreateRequest) (ports.NetworkSecurityGroupRecord, error) {
	if err := requireNetworkTenantAndName(request.TenantID, request.Name); err != nil {
		return ports.NetworkSecurityGroupRecord{}, err
	}
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.NetworkSecurityGroupRecord{}, err
	}
	s.mu.Lock()
	if id, ok := s.securityGroupIdem[idemKey]; ok {
		if record, exists := s.securityGroup[id]; exists {
			s.mu.Unlock()
			return record, nil
		}
	}
	now := s.now().UTC()
	providerConfigured := s.networkProviderConfigured()
	record := ports.NetworkSecurityGroupRecord{
		TenantID:        request.TenantID,
		SecurityGroupID: "sg_" + uuid.NewString(),
		VPCID:           strings.TrimSpace(request.VPCID),
		Name:            strings.TrimSpace(request.Name),
		Description:     strings.TrimSpace(request.Description),
		Rules:           append([]ports.NetworkSecurityGroupRule(nil), request.Rules...),
		State:           ports.NetworkResourceAvailable,
		Reason:          "created by local network profile",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	// vpc_id 契约上可空（历史资源可为空），但一旦提供必须校验归属与存活状态，
	// 语义对齐 CreateSubnet 的 VPC 校验。store 模式必须查持久层：网关重启后
	// 内存 map 不含历史 VPC，只查内存会把已存在的 VPC 误判为 not found。
	if record.VPCID != "" {
		vpc, ok := s.resolveVPCForValidation(ctx, request.TenantID, record.VPCID)
		if !ok || vpc.State == ports.NetworkResourceDeleted {
			s.mu.Unlock()
			return ports.NetworkSecurityGroupRecord{}, fmt.Errorf("%w: vpc not found", ports.ErrNotFound)
		}
	}
	if providerConfigured {
		record.State = ports.NetworkResourcePending
		record.Reason = "pending provider apply"
	}
	s.securityGroup[record.SecurityGroupID] = record
	s.securityGroupIdem[idemKey] = record.SecurityGroupID
	for _, rule := range record.Rules {
		ruleID := "sgr_" + uuid.NewString()
		s.securityGroupRules[ruleID] = ports.NetworkSecurityGroupRuleRecord{
			TenantID:        request.TenantID,
			RuleID:          ruleID,
			SecurityGroupID: record.SecurityGroupID,
			Priority:        firstNetworkPriority(rule.Priority, 1000),
			Direction:       strings.TrimSpace(rule.Direction),
			Protocol:        strings.TrimSpace(rule.Protocol),
			PortRange:       strings.TrimSpace(rule.PortRange),
			CIDR:            strings.TrimSpace(rule.CIDR),
			Action:          strings.TrimSpace(rule.Action),
			CreatedAt:       now,
			UpdatedAt:       now,
		}
	}
	s.mu.Unlock()
	if err := s.upsertSecurityGroup(ctx, record); err != nil {
		return ports.NetworkSecurityGroupRecord{}, err
	}
	if !providerConfigured {
		return record, nil
	}
	applied, err := s.applySecurityGroupProvider(ctx, record)
	if err != nil {
		return ports.NetworkSecurityGroupRecord{}, s.markSecurityGroupProviderFailed(ctx, record, err)
	}
	s.mu.Lock()
	if _, exists := s.securityGroup[applied.SecurityGroupID]; exists {
		s.securityGroup[applied.SecurityGroupID] = applied
	}
	s.mu.Unlock()
	if err := s.upsertSecurityGroup(ctx, applied); err != nil {
		return ports.NetworkSecurityGroupRecord{}, err
	}
	return applied, nil
}

func (s *LocalNetworkService) ListSecurityGroups(ctx context.Context, request ports.NetworkResourceListRequest) ([]ports.NetworkSecurityGroupRecord, error) {
	if s.store != nil {
		items, err := s.store.ListSecurityGroups(ctx, request.TenantID)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(request.VPCID) != "" {
			filtered := make([]ports.NetworkSecurityGroupRecord, 0, len(items))
			for _, record := range items {
				if record.VPCID == strings.TrimSpace(request.VPCID) {
					filtered = append(filtered, record)
				}
			}
			items = filtered
		}
		if strings.TrimSpace(request.Name) != "" {
			filtered := make([]ports.NetworkSecurityGroupRecord, 0, len(items))
			for _, record := range items {
				if strings.HasPrefix(record.Name, strings.TrimSpace(request.Name)) {
					filtered = append(filtered, record)
				}
			}
			items = filtered
		}
		if strings.TrimSpace(request.Keyword) != "" {
			filtered := make([]ports.NetworkSecurityGroupRecord, 0, len(items))
			for _, record := range items {
				if strings.HasPrefix(record.SecurityGroupID, strings.TrimSpace(request.Keyword)) {
					filtered = append(filtered, record)
				}
			}
			items = filtered
		}
		if request.State != "" {
			filtered := make([]ports.NetworkSecurityGroupRecord, 0, len(items))
			for _, record := range items {
				if record.State == request.State {
					filtered = append(filtered, record)
				}
			}
			items = filtered
		}
		// 绑定计数以实例记录派生视图为准（安全组-5），一次查询服务整个列表。
		derived := s.derivedSecurityGroupBindings(ctx, request.TenantID)
		s.mu.RLock()
		for i := range items {
			items[i].BoundInstanceCount = s.securityGroupBoundInstanceCountLocked(items[i].SecurityGroupID, derived[items[i].SecurityGroupID])
		}
		s.mu.RUnlock()
		sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
		return items, nil
	}
	derived := s.derivedSecurityGroupBindings(ctx, request.TenantID)
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ports.NetworkSecurityGroupRecord, 0, len(s.securityGroup))
	for _, record := range s.securityGroup {
		if record.TenantID != request.TenantID || record.State == ports.NetworkResourceDeleted {
			continue
		}
		if strings.TrimSpace(request.VPCID) != "" && record.VPCID != strings.TrimSpace(request.VPCID) {
			continue
		}
		if strings.TrimSpace(request.Name) != "" && !strings.HasPrefix(record.Name, strings.TrimSpace(request.Name)) {
			continue
		}
		if strings.TrimSpace(request.Keyword) != "" && !strings.HasPrefix(record.SecurityGroupID, strings.TrimSpace(request.Keyword)) {
			continue
		}
		if request.State != "" && record.State != request.State {
			continue
		}
		record.BoundInstanceCount = s.securityGroupBoundInstanceCountLocked(record.SecurityGroupID, derived[record.SecurityGroupID])
		items = append(items, record)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items, nil
}

// derivedSecurityGroupBindings 从实例记录派生安全组绑定视图：
// map[securityGroupID] -> 该安全组当前真实绑定的实例 binding 记录。
// 实例侧"更换安全组"只写实例自身 record.Network.SecurityGroups（持久化于
// workload_instances.network_summary），不写 securityGroupBinds 内存 map，
// 因此绑定查询必须以实例记录为真实来源反查（测试缺陷 安全组-5）。
// 终态（deleting/deleted）实例不计入绑定。未注入实例 store 时返回 nil。
func (s *LocalNetworkService) derivedSecurityGroupBindings(ctx context.Context, tenantID string) map[string][]ports.NetworkSecurityGroupBindingRecord {
	if s.instances == nil || strings.TrimSpace(tenantID) == "" {
		return nil
	}
	records, err := s.instances.List(ctx, tenantID, "")
	if err != nil {
		return nil
	}
	derived := map[string][]ports.NetworkSecurityGroupBindingRecord{}
	for _, record := range records {
		if record.Status.State == ports.WorkloadStateDeleting || record.Status.State == ports.WorkloadStateDeleted {
			continue
		}
		for _, sg := range record.Network.SecurityGroups {
			sgID := strings.TrimSpace(sg.ID)
			if sgID == "" {
				continue
			}
			derived[sgID] = append(derived[sgID], ports.NetworkSecurityGroupBindingRecord{
				TenantID:        record.TenantID,
				BindingID:       "sgb-inst-" + record.InstanceID + "-" + sgID,
				SecurityGroupID: sgID,
				TargetType:      "instance",
				TargetID:        record.InstanceID,
				CreatedAt:       record.CreatedAt,
			})
		}
	}
	return derived
}

// securityGroupBoundInstanceCountLocked 统计指定安全组当前绑定的实例数（聚合字段
// bound_instance_count 的数据源）：实例绑定以派生视图 derived（来自实例记录）为准，
// 显式 bindings API 写入的记录补充派生未覆盖的目标，按 target_id 去重。
// derived 为 nil 时（未注入实例 store）退化为纯显式绑定计数。
// 调用方必须已持有 s.mu 读锁或写锁。
func (s *LocalNetworkService) securityGroupBoundInstanceCountLocked(securityGroupID string, derived []ports.NetworkSecurityGroupBindingRecord) int {
	derivedTargets := map[string]struct{}{}
	for _, bind := range derived {
		derivedTargets[bind.TargetID] = struct{}{}
	}
	count := 0
	for _, bind := range s.securityGroupBinds {
		if bind.SecurityGroupID != securityGroupID || bind.TargetType != "instance" {
			continue
		}
		if _, covered := derivedTargets[bind.TargetID]; covered {
			continue
		}
		count++
	}
	return count + len(derivedTargets)
}

// resolveVPCForValidation 解析用于创建校验（安全组/子网等绑定 VPC）的 VPC 记录：
// store 模式优先查持久层（进程重启后内存 map 不含历史 VPC），否则回退内存 map；
// 租户归属校验两条路径各自完成（store SQL 按 tenant_id 过滤，内存分支显式比对）。
// 返回 (record, true) 表示 VPC 存在且属于该租户；存活状态由调用方判断。
func (s *LocalNetworkService) resolveVPCForValidation(ctx context.Context, tenantID string, vpcID string) (ports.NetworkVPCRecord, bool) {
	if s.store != nil {
		record, err := s.store.GetVPC(ctx, tenantID, vpcID)
		return record, err == nil
	}
	record, ok := s.vpcs[vpcID]
	if !ok || record.TenantID != tenantID {
		return ports.NetworkVPCRecord{}, false
	}
	return record, true
}

func (s *LocalNetworkService) GetSecurityGroup(ctx context.Context, request ports.NetworkResourceGetRequest) (ports.NetworkSecurityGroupRecord, error) {
	if s.store != nil {
		record, err := s.store.GetSecurityGroup(ctx, request.TenantID, request.ResourceID)
		if err != nil {
			return ports.NetworkSecurityGroupRecord{}, err
		}
		derived := s.derivedSecurityGroupBindings(ctx, request.TenantID)
		s.mu.RLock()
		record.BoundInstanceCount = s.securityGroupBoundInstanceCountLocked(record.SecurityGroupID, derived[record.SecurityGroupID])
		s.mu.RUnlock()
		return record, nil
	}
	derived := s.derivedSecurityGroupBindings(ctx, request.TenantID)
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.securityGroup[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.NetworkResourceDeleted {
		return ports.NetworkSecurityGroupRecord{}, ports.ErrNotFound
	}
	record.BoundInstanceCount = s.securityGroupBoundInstanceCountLocked(record.SecurityGroupID, derived[record.SecurityGroupID])
	return record, nil
}

func (s *LocalNetworkService) DeleteSecurityGroup(ctx context.Context, request ports.NetworkResourceGetRequest) (ports.NetworkSecurityGroupRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.securityGroup[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.NetworkResourceDeleted {
		return ports.NetworkSecurityGroupRecord{}, ports.ErrNotFound
	}
	record.State = ports.NetworkResourceDeleted
	record.Reason = "deleted by local network profile"
	record.UpdatedAt = s.now().UTC()
	s.securityGroup[record.SecurityGroupID] = record
	if err := s.upsertSecurityGroup(ctx, record); err != nil {
		return ports.NetworkSecurityGroupRecord{}, err
	}
	return record, nil
}

func (s *LocalNetworkService) ListSecurityGroupRules(_ context.Context, request ports.NetworkSecurityGroupRuleListRequest) ([]ports.NetworkSecurityGroupRuleRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.securityGroupExistsLocked(request.TenantID, request.SecurityGroupID) {
		return nil, ports.ErrNotFound
	}
	items := make([]ports.NetworkSecurityGroupRuleRecord, 0, len(s.securityGroupRules))
	for _, record := range s.securityGroupRules {
		if record.TenantID != request.TenantID || record.SecurityGroupID != strings.TrimSpace(request.SecurityGroupID) {
			continue
		}
		if strings.TrimSpace(request.Direction) != "" && record.Direction != strings.TrimSpace(request.Direction) {
			continue
		}
		if strings.TrimSpace(request.Protocol) != "" && record.Protocol != strings.TrimSpace(request.Protocol) {
			continue
		}
		items = append(items, record)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Priority == items[j].Priority {
			return items[i].CreatedAt.Before(items[j].CreatedAt)
		}
		return items[i].Priority < items[j].Priority
	})
	return items, nil
}

func (s *LocalNetworkService) CreateSecurityGroupRule(ctx context.Context, request ports.NetworkSecurityGroupRuleCreateRequest) (ports.NetworkSecurityGroupRuleRecord, error) {
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.NetworkSecurityGroupRuleRecord{}, err
	}
	if err := validateSecurityGroupRuleFields(request.Priority, request.Direction, request.Protocol, request.PortRange, request.CIDR, request.Action); err != nil {
		return ports.NetworkSecurityGroupRuleRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.securityRuleIdem[idemKey]; ok {
		if record, exists := s.securityGroupRules[id]; exists {
			return record, nil
		}
	}
	sg, ok := s.securityGroup[strings.TrimSpace(request.SecurityGroupID)]
	if !ok || sg.TenantID != request.TenantID || sg.State == ports.NetworkResourceDeleted {
		return ports.NetworkSecurityGroupRuleRecord{}, ports.ErrNotFound
	}
	now := s.now().UTC()
	record := ports.NetworkSecurityGroupRuleRecord{
		TenantID:        request.TenantID,
		RuleID:          "sgr_" + uuid.NewString(),
		SecurityGroupID: sg.SecurityGroupID,
		Priority:        request.Priority,
		Direction:       strings.TrimSpace(request.Direction),
		Protocol:        strings.TrimSpace(request.Protocol),
		PortRange:       strings.TrimSpace(request.PortRange),
		CIDR:            strings.TrimSpace(request.CIDR),
		Action:          strings.TrimSpace(request.Action),
		Description:     strings.TrimSpace(request.Description),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	s.securityGroupRules[record.RuleID] = record
	s.securityRuleIdem[idemKey] = record.RuleID
	sg.Rules = append(sg.Rules, securityGroupRuleSummary(record))
	sg.UpdatedAt = now
	s.securityGroup[sg.SecurityGroupID] = sg
	if err := s.upsertSecurityGroup(ctx, sg); err != nil {
		return ports.NetworkSecurityGroupRuleRecord{}, err
	}
	return record, nil
}

func (s *LocalNetworkService) GetSecurityGroupRule(_ context.Context, request ports.NetworkSecurityGroupRuleGetRequest) (ports.NetworkSecurityGroupRuleRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.securityGroupRules[strings.TrimSpace(request.RuleID)]
	if !ok || record.TenantID != request.TenantID || record.SecurityGroupID != strings.TrimSpace(request.SecurityGroupID) {
		return ports.NetworkSecurityGroupRuleRecord{}, ports.ErrNotFound
	}
	if !s.securityGroupExistsLocked(request.TenantID, request.SecurityGroupID) {
		return ports.NetworkSecurityGroupRuleRecord{}, ports.ErrNotFound
	}
	return record, nil
}

func (s *LocalNetworkService) UpdateSecurityGroupRule(ctx context.Context, request ports.NetworkSecurityGroupRuleUpdateRequest) (ports.NetworkSecurityGroupRuleRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.securityGroupRules[strings.TrimSpace(request.RuleID)]
	if !ok || record.TenantID != request.TenantID || record.SecurityGroupID != strings.TrimSpace(request.SecurityGroupID) {
		return ports.NetworkSecurityGroupRuleRecord{}, ports.ErrNotFound
	}
	if !s.securityGroupExistsLocked(request.TenantID, request.SecurityGroupID) {
		return ports.NetworkSecurityGroupRuleRecord{}, ports.ErrNotFound
	}
	if request.Priority != 0 {
		if request.Priority < 1 || request.Priority > 32766 {
			return ports.NetworkSecurityGroupRuleRecord{}, fmt.Errorf("%w: priority must be between 1 and 32766", ports.ErrInvalid)
		}
		record.Priority = request.Priority
	}
	if strings.TrimSpace(request.Direction) != "" {
		record.Direction = strings.TrimSpace(request.Direction)
	}
	if strings.TrimSpace(request.Protocol) != "" {
		record.Protocol = strings.TrimSpace(request.Protocol)
	}
	if strings.TrimSpace(request.PortRange) != "" {
		record.PortRange = strings.TrimSpace(request.PortRange)
	}
	if strings.TrimSpace(request.CIDR) != "" {
		record.CIDR = strings.TrimSpace(request.CIDR)
	}
	if strings.TrimSpace(request.Action) != "" {
		record.Action = strings.TrimSpace(request.Action)
	}
	if strings.TrimSpace(request.Description) != "" {
		record.Description = strings.TrimSpace(request.Description)
	}
	if err := validateSecurityGroupRuleFields(record.Priority, record.Direction, record.Protocol, record.PortRange, record.CIDR, record.Action); err != nil {
		return ports.NetworkSecurityGroupRuleRecord{}, err
	}
	record.UpdatedAt = s.now().UTC()
	s.securityGroupRules[record.RuleID] = record
	s.syncSecurityGroupRulesLocked(record.SecurityGroupID)
	if sg, ok := s.securityGroup[record.SecurityGroupID]; ok {
		if err := s.upsertSecurityGroup(ctx, sg); err != nil {
			return ports.NetworkSecurityGroupRuleRecord{}, err
		}
	}
	return record, nil
}

func (s *LocalNetworkService) DeleteSecurityGroupRule(ctx context.Context, request ports.NetworkSecurityGroupRuleGetRequest) (ports.NetworkSecurityGroupRuleRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.securityGroupRules[strings.TrimSpace(request.RuleID)]
	if !ok || record.TenantID != request.TenantID || record.SecurityGroupID != strings.TrimSpace(request.SecurityGroupID) {
		return ports.NetworkSecurityGroupRuleRecord{}, ports.ErrNotFound
	}
	if !s.securityGroupExistsLocked(request.TenantID, request.SecurityGroupID) {
		return ports.NetworkSecurityGroupRuleRecord{}, ports.ErrNotFound
	}
	delete(s.securityGroupRules, record.RuleID)
	s.syncSecurityGroupRulesLocked(record.SecurityGroupID)
	if sg, ok := s.securityGroup[record.SecurityGroupID]; ok {
		if err := s.upsertSecurityGroup(ctx, sg); err != nil {
			return ports.NetworkSecurityGroupRuleRecord{}, err
		}
	}
	return record, nil
}

// resolveSecurityGroupExists 解析安全组是否存在且属于该租户：
// store 模式优先查持久层（进程重启后内存 map 不含历史安全组），否则回退内存 map。
// 返回 (record, true) 表示安全组存在且属于该租户。
func (s *LocalNetworkService) resolveSecurityGroupExists(ctx context.Context, tenantID string, securityGroupID string) (ports.NetworkSecurityGroupRecord, bool) {
	if s.store != nil {
		record, err := s.store.GetSecurityGroup(ctx, tenantID, securityGroupID)
		return record, err == nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.securityGroup[strings.TrimSpace(securityGroupID)]
	if !ok || record.TenantID != tenantID || record.State == ports.NetworkResourceDeleted {
		return ports.NetworkSecurityGroupRecord{}, false
	}
	return record, true
}

// storeBackedSecurityGroupExists 仅查持久层确认安全组存在且属于该租户。
// 供已持有 s.mu 锁的调用方使用（不得在此再加锁）；store 未配置时恒为 false。
func (s *LocalNetworkService) storeBackedSecurityGroupExists(ctx context.Context, tenantID string, securityGroupID string) bool {
	if s.store == nil {
		return false
	}
	_, err := s.store.GetSecurityGroup(ctx, tenantID, securityGroupID)
	return err == nil
}

func (s *LocalNetworkService) ListSecurityGroupBindings(ctx context.Context, request ports.NetworkSecurityGroupBindingListRequest) ([]ports.NetworkSecurityGroupBindingRecord, error) {
	if _, ok := s.resolveSecurityGroupExists(ctx, request.TenantID, request.SecurityGroupID); !ok {
		return nil, ports.ErrNotFound
	}
	// 实例绑定以实例记录派生视图为准（安全组-5）：实例侧"更换安全组"只更新
	// 实例自身记录，不写 securityGroupBinds，显式绑定无法反映真实绑定关系。
	derived := s.derivedSecurityGroupBindings(ctx, request.TenantID)[strings.TrimSpace(request.SecurityGroupID)]
	derivedTargets := make(map[string]struct{}, len(derived))
	for _, bind := range derived {
		derivedTargets[bind.TargetID] = struct{}{}
	}
	s.mu.RLock()
	items := make([]ports.NetworkSecurityGroupBindingRecord, 0, len(s.securityGroupBinds)+len(derived))
	for _, record := range s.securityGroupBinds {
		// 同一实例已被派生视图覆盖时跳过显式记录，避免重复展示。
		if record.TargetType == "instance" {
			if _, covered := derivedTargets[record.TargetID]; covered {
				continue
			}
		}
		if record.TenantID != request.TenantID || record.SecurityGroupID != strings.TrimSpace(request.SecurityGroupID) {
			continue
		}
		if strings.TrimSpace(request.TargetType) != "" && record.TargetType != strings.TrimSpace(request.TargetType) {
			continue
		}
		if strings.TrimSpace(request.TargetID) != "" && record.TargetID != strings.TrimSpace(request.TargetID) {
			continue
		}
		items = append(items, record)
	}
	s.mu.RUnlock()
	for _, record := range derived {
		if strings.TrimSpace(request.TargetType) != "" && record.TargetType != strings.TrimSpace(request.TargetType) {
			continue
		}
		if strings.TrimSpace(request.TargetID) != "" && record.TargetID != strings.TrimSpace(request.TargetID) {
			continue
		}
		items = append(items, record)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}

func (s *LocalNetworkService) CreateSecurityGroupBinding(ctx context.Context, request ports.NetworkSecurityGroupBindingCreateRequest) (ports.NetworkSecurityGroupBindingRecord, error) {
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.NetworkSecurityGroupBindingRecord{}, err
	}
	targetType := strings.TrimSpace(request.TargetType)
	if targetType != "instance" && targetType != "network_interface" && targetType != "load_balancer" {
		return ports.NetworkSecurityGroupBindingRecord{}, fmt.Errorf("%w: unsupported security group binding target_type %q", ports.ErrInvalid, request.TargetType)
	}
	if strings.TrimSpace(request.TargetID) == "" {
		return ports.NetworkSecurityGroupBindingRecord{}, fmt.Errorf("%w: target_id is required", ports.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.securityBindIdem[idemKey]; ok {
		if record, exists := s.securityGroupBinds[id]; exists {
			return record, nil
		}
	}
	if !s.securityGroupExistsLocked(request.TenantID, request.SecurityGroupID) && !s.storeBackedSecurityGroupExists(ctx, request.TenantID, request.SecurityGroupID) {
		return ports.NetworkSecurityGroupBindingRecord{}, ports.ErrNotFound
	}
	record := ports.NetworkSecurityGroupBindingRecord{
		TenantID:        request.TenantID,
		BindingID:       "sgb_" + uuid.NewString(),
		SecurityGroupID: strings.TrimSpace(request.SecurityGroupID),
		TargetType:      targetType,
		TargetID:        strings.TrimSpace(request.TargetID),
		CreatedAt:       s.now().UTC(),
	}
	s.securityGroupBinds[record.BindingID] = record
	s.securityBindIdem[idemKey] = record.BindingID
	return record, nil
}

func (s *LocalNetworkService) DeleteSecurityGroupBinding(ctx context.Context, request ports.NetworkSecurityGroupBindingDeleteRequest) (ports.NetworkSecurityGroupBindingRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.securityGroupBinds[strings.TrimSpace(request.BindingID)]
	if !ok || record.TenantID != request.TenantID || record.SecurityGroupID != strings.TrimSpace(request.SecurityGroupID) {
		return ports.NetworkSecurityGroupBindingRecord{}, ports.ErrNotFound
	}
	if !s.securityGroupExistsLocked(request.TenantID, request.SecurityGroupID) && !s.storeBackedSecurityGroupExists(ctx, request.TenantID, request.SecurityGroupID) {
		return ports.NetworkSecurityGroupBindingRecord{}, ports.ErrNotFound
	}
	delete(s.securityGroupBinds, record.BindingID)
	return record, nil
}

func (s *LocalNetworkService) CreateLoadBalancer(ctx context.Context, request ports.NetworkLoadBalancerCreateRequest) (ports.NetworkLoadBalancerRecord, error) {
	if err := requireNetworkTenantAndName(request.TenantID, request.Name); err != nil {
		return ports.NetworkLoadBalancerRecord{}, err
	}
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.NetworkLoadBalancerRecord{}, err
	}
	if strings.TrimSpace(request.VPCID) == "" {
		return ports.NetworkLoadBalancerRecord{}, fmt.Errorf("%w: vpc_id is required", ports.ErrInvalid)
	}
	s.mu.Lock()
	if id, ok := s.loadBalancerIdem[idemKey]; ok {
		if record, exists := s.loadBalancers[id]; exists {
			s.mu.Unlock()
			return record, nil
		}
	}
	now := s.now().UTC()
	providerConfigured := s.networkProviderConfigured()
	record := ports.NetworkLoadBalancerRecord{
		TenantID:       request.TenantID,
		LoadBalancerID: "lb_" + uuid.NewString(),
		Name:           strings.TrimSpace(request.Name),
		VPCID:          strings.TrimSpace(request.VPCID),
		SubnetID:       strings.TrimSpace(request.SubnetID),
		Scheme:         firstNetworkNonEmpty(request.Scheme, "internal"),
		VIP:            "local-dev",
		Listeners:      append([]ports.NetworkLoadBalancerListener(nil), request.Listeners...),
		State:          ports.NetworkResourceAvailable,
		Reason:         "created by local network profile",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	vpc, ok := s.vpcs[record.VPCID]
	if !ok || vpc.TenantID != request.TenantID || vpc.State == ports.NetworkResourceDeleted {
		s.mu.Unlock()
		return ports.NetworkLoadBalancerRecord{}, fmt.Errorf("%w: vpc not found", ports.ErrNotFound)
	}
	if providerConfigured {
		record.State = ports.NetworkResourcePending
		record.Reason = "pending provider apply"
	}
	s.loadBalancers[record.LoadBalancerID] = record
	s.loadBalancerIdem[idemKey] = record.LoadBalancerID
	s.mu.Unlock()
	if err := s.upsertLoadBalancer(ctx, record); err != nil {
		return ports.NetworkLoadBalancerRecord{}, err
	}
	if !providerConfigured {
		return record, nil
	}
	applied, err := s.applyLoadBalancerProvider(ctx, record)
	if err != nil {
		return ports.NetworkLoadBalancerRecord{}, s.markLoadBalancerProviderFailed(ctx, record, err)
	}
	s.mu.Lock()
	if _, exists := s.loadBalancers[applied.LoadBalancerID]; exists {
		s.loadBalancers[applied.LoadBalancerID] = applied
	}
	s.mu.Unlock()
	if err := s.upsertLoadBalancer(ctx, applied); err != nil {
		return ports.NetworkLoadBalancerRecord{}, err
	}
	return applied, nil
}

func (s *LocalNetworkService) ListLoadBalancers(_ context.Context, request ports.NetworkResourceListRequest) ([]ports.NetworkLoadBalancerRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ports.NetworkLoadBalancerRecord, 0, len(s.loadBalancers))
	for _, record := range s.loadBalancers {
		if record.TenantID != request.TenantID || record.State == ports.NetworkResourceDeleted {
			continue
		}
		if strings.TrimSpace(request.VPCID) != "" && record.VPCID != strings.TrimSpace(request.VPCID) {
			continue
		}
		if request.State != "" && record.State != request.State {
			continue
		}
		if strings.TrimSpace(request.Scheme) != "" && record.Scheme != strings.TrimSpace(request.Scheme) {
			continue
		}
		items = append(items, record)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items, nil
}

func (s *LocalNetworkService) GetLoadBalancer(_ context.Context, request ports.NetworkResourceGetRequest) (ports.NetworkLoadBalancerRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.loadBalancers[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.NetworkResourceDeleted {
		return ports.NetworkLoadBalancerRecord{}, ports.ErrNotFound
	}
	return record, nil
}

func (s *LocalNetworkService) DeleteLoadBalancer(ctx context.Context, request ports.NetworkResourceGetRequest) (ports.NetworkLoadBalancerRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.loadBalancers[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.NetworkResourceDeleted {
		return ports.NetworkLoadBalancerRecord{}, ports.ErrNotFound
	}
	record.State = ports.NetworkResourceDeleted
	record.Reason = "deleted by local network profile"
	record.UpdatedAt = s.now().UTC()
	s.loadBalancers[record.LoadBalancerID] = record
	if err := s.upsertLoadBalancer(ctx, record); err != nil {
		return ports.NetworkLoadBalancerRecord{}, err
	}
	return record, nil
}

func (s *LocalNetworkService) CreateRoute(ctx context.Context, request ports.NetworkRouteCreateRequest) (ports.NetworkRouteRecord, error) {
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.NetworkRouteRecord{}, err
	}
	if strings.TrimSpace(request.VPCID) == "" {
		return ports.NetworkRouteRecord{}, fmt.Errorf("%w: vpc_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.DestinationCIDR) == "" || strings.TrimSpace(request.NextHopType) == "" || strings.TrimSpace(request.NextHopID) == "" {
		return ports.NetworkRouteRecord{}, fmt.Errorf("%w: destination_cidr/next_hop_type/next_hop_id are required", ports.ErrInvalid)
	}
	nextHopType := strings.ToLower(strings.TrimSpace(request.NextHopType))
	if nextHopType != "gateway" && nextHopType != "instance" && nextHopType != "nat" {
		return ports.NetworkRouteRecord{}, fmt.Errorf("%w: unsupported route next_hop_type %q", ports.ErrUnsupported, request.NextHopType)
	}
	s.mu.Lock()
	if id, ok := s.routeIdempotency[idemKey]; ok {
		if record, exists := s.routes[id]; exists {
			s.mu.Unlock()
			return record, nil
		}
	}
	vpc, ok := s.vpcs[strings.TrimSpace(request.VPCID)]
	if !ok || vpc.TenantID != request.TenantID || vpc.State == ports.NetworkResourceDeleted {
		s.mu.Unlock()
		return ports.NetworkRouteRecord{}, fmt.Errorf("%w: vpc not found", ports.ErrNotFound)
	}
	providerConfigured := s.networkProviderConfigured()
	record := ports.NetworkRouteRecord{
		TenantID:        request.TenantID,
		RouteID:         "rt_" + uuid.NewString(),
		VPCID:           strings.TrimSpace(request.VPCID),
		DestinationCIDR: strings.TrimSpace(request.DestinationCIDR),
		NextHopType:     nextHopType,
		NextHopID:       strings.TrimSpace(request.NextHopID),
		Description:     strings.TrimSpace(request.Description),
		State:           ports.NetworkResourceAvailable,
		CreatedAt:       s.now().UTC(),
	}
	if providerConfigured {
		record.State = ports.NetworkResourcePending
	}
	s.routes[record.RouteID] = record
	s.routeIdempotency[idemKey] = record.RouteID
	s.mu.Unlock()
	if !providerConfigured {
		if err := s.upsertRoute(ctx, record); err != nil {
			return ports.NetworkRouteRecord{}, err
		}
		return record, nil
	}
	if err := s.upsertRoute(ctx, record); err != nil {
		return ports.NetworkRouteRecord{}, err
	}
	applied, err := s.applyRouteProvider(ctx, record)
	s.mu.Lock()
	if err != nil {
		record.State = ports.NetworkResourceFailed
		s.routes[record.RouteID] = record
		s.mu.Unlock()
		_ = s.upsertRoute(ctx, record)
		return ports.NetworkRouteRecord{}, err
	}
	record = applied
	if _, exists := s.routes[record.RouteID]; exists {
		s.routes[record.RouteID] = record
	}
	s.mu.Unlock()
	if err := s.upsertRoute(ctx, record); err != nil {
		return ports.NetworkRouteRecord{}, err
	}
	return record, nil
}

func (s *LocalNetworkService) ListRoutes(_ context.Context, request ports.NetworkRouteListRequest) ([]ports.NetworkRouteRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ports.NetworkRouteRecord, 0, len(s.routes))
	for _, record := range s.routes {
		if record.TenantID != request.TenantID || record.State == ports.NetworkResourceDeleted {
			continue
		}
		if strings.TrimSpace(request.VPCID) != "" && record.VPCID != strings.TrimSpace(request.VPCID) {
			continue
		}
		if strings.TrimSpace(request.NextHopType) != "" && record.NextHopType != strings.TrimSpace(request.NextHopType) {
			continue
		}
		items = append(items, record)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}

func (s *LocalNetworkService) GetRoute(_ context.Context, request ports.NetworkResourceGetRequest) (ports.NetworkRouteRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.routes[strings.TrimSpace(request.ResourceID)]
	if !ok || record.TenantID != request.TenantID || record.State == ports.NetworkResourceDeleted {
		return ports.NetworkRouteRecord{}, ports.ErrNotFound
	}
	return record, nil
}

func (s *LocalNetworkService) DeleteRoute(ctx context.Context, request ports.NetworkResourceGetRequest) (ports.NetworkRouteRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.routes[strings.TrimSpace(request.ResourceID)]
	if !ok || record.TenantID != request.TenantID || record.State == ports.NetworkResourceDeleted {
		return ports.NetworkRouteRecord{}, ports.ErrNotFound
	}
	record.State = ports.NetworkResourceDeleted
	s.routes[record.RouteID] = record
	if err := s.upsertRoute(ctx, record); err != nil {
		return ports.NetworkRouteRecord{}, err
	}
	return record, nil
}

func (s *LocalNetworkService) upsertVPC(ctx context.Context, record ports.NetworkVPCRecord) error {
	if s.store == nil {
		return nil
	}
	return s.store.UpsertVPC(ctx, record)
}

func (s *LocalNetworkService) upsertSubnet(ctx context.Context, record ports.NetworkSubnetRecord) error {
	if s.store == nil {
		return nil
	}
	return s.store.UpsertSubnet(ctx, record)
}

func (s *LocalNetworkService) upsertSecurityGroup(ctx context.Context, record ports.NetworkSecurityGroupRecord) error {
	if s.store == nil {
		return nil
	}
	return s.store.UpsertSecurityGroup(ctx, record)
}

func (s *LocalNetworkService) upsertLoadBalancer(ctx context.Context, record ports.NetworkLoadBalancerRecord) error {
	if s.store == nil {
		return nil
	}
	return s.store.UpsertLoadBalancer(ctx, record)
}

func (s *LocalNetworkService) upsertRoute(ctx context.Context, record ports.NetworkRouteRecord) error {
	if s.store == nil {
		return nil
	}
	return s.store.UpsertRoute(ctx, record)
}

func (s *LocalNetworkService) networkProviderConfigured() bool {
	return s.providerRenderer != nil || s.providerDryRun != nil || s.providerApply != nil || s.providerStatus != nil
}

func (s *LocalNetworkService) applyNetworkProvider(ctx context.Context, tenantID string, resourceKind string, resourceID string, manifests []ports.WorkloadManifest) (ports.NetworkProviderStatusResult, ports.NetworkProviderApplyResult, error) {
	if s.providerRenderer == nil || s.providerDryRun == nil || s.providerApply == nil || s.providerStatus == nil {
		return ports.NetworkProviderStatusResult{}, ports.NetworkProviderApplyResult{}, fmt.Errorf("%w: network provider requires renderer, dry-run, apply, and status adapters", ports.ErrNotConfigured)
	}
	userID := strings.TrimSpace(s.providerExecution.UserID)
	permissionProof := strings.TrimSpace(s.providerExecution.PermissionProof)
	if userID == "" || permissionProof == "" {
		return ports.NetworkProviderStatusResult{}, ports.NetworkProviderApplyResult{}, fmt.Errorf("%w: network provider requires explicit user id and permission proof", ports.ErrInvalid)
	}
	requestedAt := s.now().UTC()
	dryRun, err := s.providerDryRun.DryRun(ctx, ports.NetworkProviderDryRunRequest{
		TenantID:        tenantID,
		UserID:          userID,
		ResourceKind:    resourceKind,
		ResourceID:      resourceID,
		Operation:       ports.NetworkProviderOperationCreate,
		Manifests:       manifests,
		PermissionProof: permissionProof,
		RequestedAt:     requestedAt,
	})
	if err != nil {
		return ports.NetworkProviderStatusResult{}, ports.NetworkProviderApplyResult{}, err
	}
	apply, err := s.providerApply.Apply(ctx, ports.NetworkProviderApplyRequest{
		TenantID:        tenantID,
		UserID:          userID,
		ResourceKind:    resourceKind,
		ResourceID:      resourceID,
		Operation:       ports.NetworkProviderOperationCreate,
		Manifests:       manifests,
		PermissionProof: permissionProof,
		DryRunResult:    dryRun,
		RequestedAt:     requestedAt,
	})
	if err != nil {
		return ports.NetworkProviderStatusResult{}, ports.NetworkProviderApplyResult{}, err
	}
	observation, err := s.providerStatus.Observe(ctx, ports.NetworkProviderStatusRequest{
		TenantID:        tenantID,
		UserID:          userID,
		ResourceKind:    resourceKind,
		ResourceID:      resourceID,
		ApplyResult:     apply,
		PermissionProof: permissionProof,
		RequestedAt:     requestedAt,
	})
	if err != nil {
		return ports.NetworkProviderStatusResult{}, ports.NetworkProviderApplyResult{}, err
	}
	return observation, apply, nil
}

func (s *LocalNetworkService) applyVPCProvider(ctx context.Context, record ports.NetworkVPCRecord) (ports.NetworkVPCRecord, error) {
	manifests, err := s.providerRenderer.RenderVPC(ctx, record)
	if err != nil {
		return ports.NetworkVPCRecord{}, err
	}
	observation, _, err := s.applyNetworkProvider(ctx, record.TenantID, "vpc", record.VPCID, manifests)
	if err != nil {
		return ports.NetworkVPCRecord{}, err
	}
	record.State = firstNetworkState(observation.State, ports.NetworkResourceAvailable)
	record.Reason = firstNetworkNonEmpty(observation.Reason, "observed by network provider")
	record.UpdatedAt = firstNonZeroTime(observation.ObservedAt, s.now().UTC())
	return record, nil
}

func (s *LocalNetworkService) applySubnetProvider(ctx context.Context, record ports.NetworkSubnetRecord) (ports.NetworkSubnetRecord, error) {
	manifests, err := s.providerRenderer.RenderSubnet(ctx, record)
	if err != nil {
		return ports.NetworkSubnetRecord{}, err
	}
	observation, _, err := s.applyNetworkProvider(ctx, record.TenantID, "subnet", record.SubnetID, manifests)
	if err != nil {
		return ports.NetworkSubnetRecord{}, err
	}
	record.State = firstNetworkState(observation.State, ports.NetworkResourceAvailable)
	record.Reason = firstNetworkNonEmpty(observation.Reason, "observed by network provider")
	record.UpdatedAt = firstNonZeroTime(observation.ObservedAt, s.now().UTC())
	return record, nil
}

func (s *LocalNetworkService) applySecurityGroupProvider(ctx context.Context, record ports.NetworkSecurityGroupRecord) (ports.NetworkSecurityGroupRecord, error) {
	manifests, err := s.providerRenderer.RenderSecurityGroup(ctx, record)
	if err != nil {
		return ports.NetworkSecurityGroupRecord{}, err
	}
	observation, _, err := s.applyNetworkProvider(ctx, record.TenantID, "security-group", record.SecurityGroupID, manifests)
	if err != nil {
		return ports.NetworkSecurityGroupRecord{}, err
	}
	record.State = firstNetworkState(observation.State, ports.NetworkResourceAvailable)
	record.Reason = firstNetworkNonEmpty(observation.Reason, "observed by network provider")
	record.UpdatedAt = firstNonZeroTime(observation.ObservedAt, s.now().UTC())
	return record, nil
}

func (s *LocalNetworkService) applyLoadBalancerProvider(ctx context.Context, record ports.NetworkLoadBalancerRecord) (ports.NetworkLoadBalancerRecord, error) {
	manifests, err := s.providerRenderer.RenderLoadBalancer(ctx, record)
	if err != nil {
		return ports.NetworkLoadBalancerRecord{}, err
	}
	observation, _, err := s.applyNetworkProvider(ctx, record.TenantID, "load-balancer", record.LoadBalancerID, manifests)
	if err != nil {
		return ports.NetworkLoadBalancerRecord{}, err
	}
	record.State = firstNetworkState(observation.State, ports.NetworkResourceAvailable)
	record.Reason = firstNetworkNonEmpty(observation.Reason, "observed by network provider")
	record.UpdatedAt = firstNonZeroTime(observation.ObservedAt, s.now().UTC())
	return record, nil
}

func (s *LocalNetworkService) applyRouteProvider(ctx context.Context, record ports.NetworkRouteRecord) (ports.NetworkRouteRecord, error) {
	manifests, err := s.providerRenderer.RenderRoute(ctx, record)
	if err != nil {
		return ports.NetworkRouteRecord{}, err
	}
	observation, apply, err := s.applyNetworkProvider(ctx, record.TenantID, "route", record.RouteID, manifests)
	if err != nil {
		return ports.NetworkRouteRecord{}, err
	}
	if observation.State == "" {
		record.State = ports.NetworkResourceAvailable
	} else {
		record.State = observation.State
	}
	record.Provider = firstNetworkNonEmpty(observation.Provider, apply.Provider)
	record.RealProvider = apply.Applied
	return record, nil
}

func (s *LocalNetworkService) markVPCProviderFailed(ctx context.Context, record ports.NetworkVPCRecord, cause error) error {
	record.State = ports.NetworkResourceFailed
	record.Reason = cause.Error()
	record.UpdatedAt = s.now().UTC()
	s.mu.Lock()
	s.vpcs[record.VPCID] = record
	s.mu.Unlock()
	_ = s.upsertVPC(ctx, record)
	return cause
}

func (s *LocalNetworkService) markSubnetProviderFailed(ctx context.Context, record ports.NetworkSubnetRecord, cause error) error {
	record.State = ports.NetworkResourceFailed
	record.Reason = cause.Error()
	record.UpdatedAt = s.now().UTC()
	s.mu.Lock()
	s.subnets[record.SubnetID] = record
	s.mu.Unlock()
	_ = s.upsertSubnet(ctx, record)
	return cause
}

func (s *LocalNetworkService) markSecurityGroupProviderFailed(ctx context.Context, record ports.NetworkSecurityGroupRecord, cause error) error {
	record.State = ports.NetworkResourceFailed
	record.Reason = cause.Error()
	record.UpdatedAt = s.now().UTC()
	s.mu.Lock()
	s.securityGroup[record.SecurityGroupID] = record
	s.mu.Unlock()
	_ = s.upsertSecurityGroup(ctx, record)
	return cause
}

func (s *LocalNetworkService) markLoadBalancerProviderFailed(ctx context.Context, record ports.NetworkLoadBalancerRecord, cause error) error {
	record.State = ports.NetworkResourceFailed
	record.Reason = cause.Error()
	record.UpdatedAt = s.now().UTC()
	s.mu.Lock()
	s.loadBalancers[record.LoadBalancerID] = record
	s.mu.Unlock()
	_ = s.upsertLoadBalancer(ctx, record)
	return cause
}

func firstNetworkState(values ...ports.NetworkResourceState) ports.NetworkResourceState {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func requireNetworkTenantAndName(tenantID string, name string) error {
	if strings.TrimSpace(tenantID) == "" {
		return fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: name is required", ports.ErrInvalid)
	}
	return nil
}

func firstNetworkNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func requireIdempotencyKey(tenantID string, key string) (string, error) {
	tenantID = strings.TrimSpace(tenantID)
	key = strings.TrimSpace(key)
	if tenantID == "" {
		return "", fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	if key == "" {
		return "", fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	return tenantID + "\x00" + key, nil
}

func addNetworkOverviewState(resources map[string]ports.NetworkOverviewResourceSummary, kind string, tenantID string, requestedTenantID string, state ports.NetworkResourceState) {
	if tenantID != requestedTenantID || state == ports.NetworkResourceDeleted {
		return
	}
	summary := resources[kind]
	summary.Total++
	switch state {
	case ports.NetworkResourceAvailable:
		summary.Available++
	case ports.NetworkResourcePending:
		summary.Pending++
	case ports.NetworkResourceFailed:
		summary.Failed++
	case ports.NetworkResourceDeleting:
		summary.Deleting++
	}
	resources[kind] = summary
}

func validateSecurityGroupRuleFields(priority int, direction string, protocol string, portRange string, cidr string, action string) error {
	if priority < 1 || priority > 32766 {
		return fmt.Errorf("%w: priority must be between 1 and 32766", ports.ErrInvalid)
	}
	direction = strings.TrimSpace(direction)
	if direction != "ingress" && direction != "egress" {
		return fmt.Errorf("%w: unsupported security group rule direction %q", ports.ErrInvalid, direction)
	}
	protocol = strings.TrimSpace(protocol)
	if protocol != "tcp" && protocol != "udp" && protocol != "icmp" && protocol != "all" {
		return fmt.Errorf("%w: unsupported security group rule protocol %q", ports.ErrInvalid, protocol)
	}
	if strings.TrimSpace(portRange) == "" {
		return fmt.Errorf("%w: port_range is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(cidr) == "" {
		return fmt.Errorf("%w: cidr is required", ports.ErrInvalid)
	}
	action = strings.TrimSpace(action)
	if action != "allow" && action != "deny" {
		return fmt.Errorf("%w: unsupported security group rule action %q", ports.ErrInvalid, action)
	}
	return nil
}

func firstNetworkPriority(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func securityGroupRuleSummary(record ports.NetworkSecurityGroupRuleRecord) ports.NetworkSecurityGroupRule {
	return ports.NetworkSecurityGroupRule{
		Priority:  record.Priority,
		Direction: record.Direction,
		Protocol:  record.Protocol,
		PortRange: record.PortRange,
		CIDR:      record.CIDR,
		Action:    record.Action,
	}
}

func (s *LocalNetworkService) securityGroupExistsLocked(tenantID string, securityGroupID string) bool {
	record, ok := s.securityGroup[strings.TrimSpace(securityGroupID)]
	return ok && record.TenantID == tenantID && record.State != ports.NetworkResourceDeleted
}

func (s *LocalNetworkService) syncSecurityGroupRulesLocked(securityGroupID string) {
	sg, ok := s.securityGroup[strings.TrimSpace(securityGroupID)]
	if !ok {
		return
	}
	rules := make([]ports.NetworkSecurityGroupRuleRecord, 0, len(s.securityGroupRules))
	for _, record := range s.securityGroupRules {
		if record.SecurityGroupID == sg.SecurityGroupID {
			rules = append(rules, record)
		}
	}
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].Priority == rules[j].Priority {
			return rules[i].CreatedAt.Before(rules[j].CreatedAt)
		}
		return rules[i].Priority < rules[j].Priority
	})
	sg.Rules = make([]ports.NetworkSecurityGroupRule, 0, len(rules))
	for _, rule := range rules {
		sg.Rules = append(sg.Rules, securityGroupRuleSummary(rule))
	}
	sg.UpdatedAt = s.now().UTC()
	s.securityGroup[sg.SecurityGroupID] = sg
}

var _ ports.NetworkService = (*LocalNetworkService)(nil)
