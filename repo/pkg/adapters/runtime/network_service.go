package runtime

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

type LocalNetworkService struct {
	mu                      sync.RWMutex
	now                     func() time.Time
	store                   ports.NetworkResourceStore
	providerRenderer        ports.NetworkProviderRenderer
	providerDryRun          ports.NetworkProviderDryRun
	providerApply           ports.NetworkProviderApply
	providerStatus          ports.NetworkProviderStatusReader
	providerExecution       NetworkProviderExecutionConfig
	vpcs                    map[string]ports.NetworkVPCRecord
	subnets                 map[string]ports.NetworkSubnetRecord
	securityGroup           map[string]ports.NetworkSecurityGroupRecord
	loadBalancers           map[string]ports.NetworkLoadBalancerRecord
	routes                  map[string]ports.NetworkRouteRecord
	vpcIdempotency          map[string]string
	subnetIdempotency       map[string]string
	securityGroupIdem       map[string]string
	securityGroupUpdateIdem map[string]ports.NetworkSecurityGroupRecord
	loadBalancerIdem        map[string]string
	routeIdempotency        map[string]string
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
		now:                     func() time.Time { return time.Now().UTC() },
		vpcs:                    map[string]ports.NetworkVPCRecord{},
		subnets:                 map[string]ports.NetworkSubnetRecord{},
		securityGroup:           map[string]ports.NetworkSecurityGroupRecord{},
		loadBalancers:           map[string]ports.NetworkLoadBalancerRecord{},
		routes:                  map[string]ports.NetworkRouteRecord{},
		vpcIdempotency:          map[string]string{},
		subnetIdempotency:       map[string]string{},
		securityGroupIdem:       map[string]string{},
		securityGroupUpdateIdem: map[string]ports.NetworkSecurityGroupRecord{},
		loadBalancerIdem:        map[string]string{},
		routeIdempotency:        map[string]string{},
	}
	for _, option := range options {
		option(service)
	}
	return service
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
		return s.store.ListVPCs(ctx, request.TenantID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ports.NetworkVPCRecord, 0, len(s.vpcs))
	for _, record := range s.vpcs {
		if record.TenantID == request.TenantID && record.State != ports.NetworkResourceDeleted {
			items = append(items, record)
		}
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
		record, err := s.store.GetVPC(ctx, request.TenantID, request.ResourceID)
		if err != nil {
			return ports.NetworkVPCRecord{}, err
		}
		if err := s.ensureNoDeleteDependencies(ctx, request, "vpc"); err != nil {
			return ports.NetworkVPCRecord{}, err
		}
		record.State = ports.NetworkResourceDeleted
		record.Reason = "deleted by local network profile"
		record.UpdatedAt = s.now().UTC()
		if err := s.upsertVPC(ctx, record); err != nil {
			return ports.NetworkVPCRecord{}, err
		}
		return record, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.vpcs[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.NetworkResourceDeleted {
		return ports.NetworkVPCRecord{}, ports.ErrNotFound
	}
	if err := s.ensureNoDeleteDependenciesLocked(request, "vpc"); err != nil {
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
	s.mu.Unlock()
	if _, err := s.lookupVPC(ctx, request.TenantID, record.VPCID); err != nil {
		return ports.NetworkSubnetRecord{}, err
	}
	if err := validateGatewayInCIDR(record.Gateway, record.CIDR); err != nil {
		return ports.NetworkSubnetRecord{}, err
	}
	if providerConfigured {
		record.State = ports.NetworkResourcePending
		record.Reason = "pending provider apply"
	}
	s.mu.Lock()
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
		return filterSubnetsByVPC(items, request.VPCID), nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ports.NetworkSubnetRecord, 0, len(s.subnets))
	for _, record := range s.subnets {
		if record.TenantID == request.TenantID && record.State != ports.NetworkResourceDeleted {
			if strings.TrimSpace(request.VPCID) != "" && record.VPCID != strings.TrimSpace(request.VPCID) {
				continue
			}
			items = append(items, record)
		}
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
	if s.store != nil {
		record, err := s.store.GetSubnet(ctx, request.TenantID, request.ResourceID)
		if err != nil {
			return ports.NetworkSubnetRecord{}, err
		}
		if err := s.ensureNoDeleteDependencies(ctx, request, "subnet"); err != nil {
			return ports.NetworkSubnetRecord{}, err
		}
		record.State = ports.NetworkResourceDeleted
		record.Reason = "deleted by local network profile"
		record.UpdatedAt = s.now().UTC()
		if err := s.upsertSubnet(ctx, record); err != nil {
			return ports.NetworkSubnetRecord{}, err
		}
		return record, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.subnets[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.NetworkResourceDeleted {
		return ports.NetworkSubnetRecord{}, ports.ErrNotFound
	}
	if err := s.ensureNoDeleteDependenciesLocked(request, "subnet"); err != nil {
		return ports.NetworkSubnetRecord{}, err
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
		Name:            strings.TrimSpace(request.Name),
		Description:     strings.TrimSpace(request.Description),
		Rules:           append([]ports.NetworkSecurityGroupRule(nil), request.Rules...),
		State:           ports.NetworkResourceAvailable,
		Reason:          "created by local network profile",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if providerConfigured {
		record.State = ports.NetworkResourcePending
		record.Reason = "pending provider apply"
	}
	s.securityGroup[record.SecurityGroupID] = record
	s.securityGroupIdem[idemKey] = record.SecurityGroupID
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
		return s.store.ListSecurityGroups(ctx, request.TenantID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ports.NetworkSecurityGroupRecord, 0, len(s.securityGroup))
	for _, record := range s.securityGroup {
		if record.TenantID == request.TenantID && record.State != ports.NetworkResourceDeleted {
			items = append(items, record)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items, nil
}

func (s *LocalNetworkService) GetSecurityGroup(ctx context.Context, request ports.NetworkResourceGetRequest) (ports.NetworkSecurityGroupRecord, error) {
	if s.store != nil {
		return s.store.GetSecurityGroup(ctx, request.TenantID, request.ResourceID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.securityGroup[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.NetworkResourceDeleted {
		return ports.NetworkSecurityGroupRecord{}, ports.ErrNotFound
	}
	return record, nil
}

func (s *LocalNetworkService) UpdateSecurityGroup(ctx context.Context, request ports.NetworkSecurityGroupUpdateRequest) (ports.NetworkSecurityGroupRecord, error) {
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.NetworkSecurityGroupRecord{}, err
	}
	if strings.TrimSpace(request.ResourceID) == "" {
		return ports.NetworkSecurityGroupRecord{}, fmt.Errorf("%w: security_group_id is required", ports.ErrInvalid)
	}
	s.mu.Lock()
	if record, ok := s.securityGroupUpdateIdem[idemKey]; ok {
		s.mu.Unlock()
		return record, nil
	}
	s.mu.Unlock()
	record, err := s.GetSecurityGroup(ctx, ports.NetworkResourceGetRequest{TenantID: request.TenantID, ResourceID: request.ResourceID})
	if err != nil {
		return ports.NetworkSecurityGroupRecord{}, err
	}
	record.Rules = append([]ports.NetworkSecurityGroupRule(nil), request.Rules...)
	if request.UpdateDescription {
		record.Description = strings.TrimSpace(request.Description)
	}
	record.UpdatedAt = s.now().UTC()
	record.Reason = "updated by local network profile"
	s.mu.Lock()
	if _, exists := s.securityGroup[record.SecurityGroupID]; exists {
		s.securityGroup[record.SecurityGroupID] = record
	}
	s.securityGroupUpdateIdem[idemKey] = record
	s.mu.Unlock()
	if err := s.upsertSecurityGroup(ctx, record); err != nil {
		return ports.NetworkSecurityGroupRecord{}, err
	}
	return record, nil
}

func (s *LocalNetworkService) DeleteSecurityGroup(ctx context.Context, request ports.NetworkResourceGetRequest) (ports.NetworkSecurityGroupRecord, error) {
	if s.store != nil {
		record, err := s.store.GetSecurityGroup(ctx, request.TenantID, request.ResourceID)
		if err != nil {
			return ports.NetworkSecurityGroupRecord{}, err
		}
		if err := s.ensureNoDeleteDependencies(ctx, request, "security-group"); err != nil {
			return ports.NetworkSecurityGroupRecord{}, err
		}
		record.State = ports.NetworkResourceDeleted
		record.Reason = "deleted by local network profile"
		record.UpdatedAt = s.now().UTC()
		if err := s.upsertSecurityGroup(ctx, record); err != nil {
			return ports.NetworkSecurityGroupRecord{}, err
		}
		return record, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.securityGroup[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.NetworkResourceDeleted {
		return ports.NetworkSecurityGroupRecord{}, ports.ErrNotFound
	}
	if err := s.ensureNoDeleteDependenciesLocked(request, "security-group"); err != nil {
		return ports.NetworkSecurityGroupRecord{}, err
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
	s.mu.Unlock()
	if _, err := s.lookupVPC(ctx, request.TenantID, record.VPCID); err != nil {
		return ports.NetworkLoadBalancerRecord{}, err
	}
	if record.SubnetID != "" {
		subnet, err := s.lookupSubnet(ctx, request.TenantID, record.SubnetID)
		if err != nil {
			return ports.NetworkLoadBalancerRecord{}, err
		}
		if subnet.VPCID != record.VPCID {
			return ports.NetworkLoadBalancerRecord{}, fmt.Errorf("%w: subnet_id %s does not belong to vpc_id %s", ports.ErrInvalid, record.SubnetID, record.VPCID)
		}
	}
	if providerConfigured {
		record.State = ports.NetworkResourcePending
		record.Reason = "pending provider apply"
	}
	s.mu.Lock()
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

func (s *LocalNetworkService) ListLoadBalancers(ctx context.Context, request ports.NetworkResourceListRequest) ([]ports.NetworkLoadBalancerRecord, error) {
	if s.store != nil {
		return s.store.ListLoadBalancers(ctx, request.TenantID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ports.NetworkLoadBalancerRecord, 0, len(s.loadBalancers))
	for _, record := range s.loadBalancers {
		if record.TenantID == request.TenantID && record.State != ports.NetworkResourceDeleted {
			items = append(items, record)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items, nil
}

func (s *LocalNetworkService) GetLoadBalancer(ctx context.Context, request ports.NetworkResourceGetRequest) (ports.NetworkLoadBalancerRecord, error) {
	if s.store != nil {
		return s.store.GetLoadBalancer(ctx, request.TenantID, request.ResourceID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.loadBalancers[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.NetworkResourceDeleted {
		return ports.NetworkLoadBalancerRecord{}, ports.ErrNotFound
	}
	return record, nil
}

func (s *LocalNetworkService) DeleteLoadBalancer(ctx context.Context, request ports.NetworkResourceGetRequest) (ports.NetworkLoadBalancerRecord, error) {
	if s.store != nil {
		record, err := s.store.GetLoadBalancer(ctx, request.TenantID, request.ResourceID)
		if err != nil {
			return ports.NetworkLoadBalancerRecord{}, err
		}
		if err := s.ensureNoDeleteDependencies(ctx, request, "load-balancer"); err != nil {
			return ports.NetworkLoadBalancerRecord{}, err
		}
		record.State = ports.NetworkResourceDeleted
		record.Reason = "deleted by local network profile"
		record.UpdatedAt = s.now().UTC()
		if err := s.upsertLoadBalancer(ctx, record); err != nil {
			return ports.NetworkLoadBalancerRecord{}, err
		}
		return record, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.loadBalancers[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.NetworkResourceDeleted {
		return ports.NetworkLoadBalancerRecord{}, ports.ErrNotFound
	}
	if err := s.ensureNoDeleteDependenciesLocked(request, "load-balancer"); err != nil {
		return ports.NetworkLoadBalancerRecord{}, err
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
	s.mu.Unlock()
	if _, err := s.lookupVPC(ctx, request.TenantID, strings.TrimSpace(request.VPCID)); err != nil {
		return ports.NetworkRouteRecord{}, err
	}
	if err := s.ensureRouteDoesNotConflict(ctx, request.TenantID, strings.TrimSpace(request.VPCID), strings.TrimSpace(request.DestinationCIDR)); err != nil {
		return ports.NetworkRouteRecord{}, err
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
	s.mu.Lock()
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

func (s *LocalNetworkService) ListRoutes(ctx context.Context, request ports.NetworkRouteListRequest) ([]ports.NetworkRouteRecord, error) {
	if s.store != nil {
		return s.store.ListRoutes(ctx, request)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ports.NetworkRouteRecord, 0, len(s.routes))
	for _, record := range s.routes {
		if record.TenantID != request.TenantID {
			continue
		}
		if strings.TrimSpace(request.VPCID) != "" && record.VPCID != strings.TrimSpace(request.VPCID) {
			continue
		}
		items = append(items, record)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}

func (s *LocalNetworkService) GetRoute(ctx context.Context, request ports.NetworkResourceGetRequest) (ports.NetworkRouteRecord, error) {
	if s.store != nil {
		return s.store.GetRoute(ctx, request.TenantID, request.ResourceID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.routes[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.NetworkResourceDeleted {
		return ports.NetworkRouteRecord{}, ports.ErrNotFound
	}
	return record, nil
}

func (s *LocalNetworkService) DeleteRoute(ctx context.Context, request ports.NetworkResourceGetRequest) (ports.NetworkRouteRecord, error) {
	if s.store != nil {
		record, err := s.store.GetRoute(ctx, request.TenantID, request.ResourceID)
		if err != nil {
			return ports.NetworkRouteRecord{}, err
		}
		record.State = ports.NetworkResourceDeleted
		if err := s.upsertRoute(ctx, record); err != nil {
			return ports.NetworkRouteRecord{}, err
		}
		return record, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.routes[request.ResourceID]
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

func validateGatewayInCIDR(gateway string, cidr string) error {
	gateway = strings.TrimSpace(gateway)
	if gateway == "" {
		return nil
	}
	prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil {
		return fmt.Errorf("%w: subnet cidr is invalid", ports.ErrInvalid)
	}
	address, err := netip.ParseAddr(gateway)
	if err != nil || !address.Is4() {
		return fmt.Errorf("%w: gateway must be a valid IPv4 address", ports.ErrInvalid)
	}
	if !prefix.Contains(address) {
		return fmt.Errorf("%w: gateway must be inside subnet cidr", ports.ErrInvalid)
	}
	return nil
}

func filterSubnetsByVPC(items []ports.NetworkSubnetRecord, vpcID string) []ports.NetworkSubnetRecord {
	vpcID = strings.TrimSpace(vpcID)
	if vpcID == "" {
		return items
	}
	filtered := make([]ports.NetworkSubnetRecord, 0, len(items))
	for _, item := range items {
		if item.VPCID == vpcID {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (s *LocalNetworkService) lookupVPC(ctx context.Context, tenantID, vpcID string) (ports.NetworkVPCRecord, error) {
	if s.store != nil {
		record, err := s.store.GetVPC(ctx, tenantID, vpcID)
		if err != nil {
			return ports.NetworkVPCRecord{}, fmt.Errorf("%w: vpc not found", ports.ErrNotFound)
		}
		if record.State == ports.NetworkResourceDeleted {
			return ports.NetworkVPCRecord{}, fmt.Errorf("%w: vpc not found", ports.ErrNotFound)
		}
		return record, nil
	}
	s.mu.RLock()
	record, ok := s.vpcs[vpcID]
	s.mu.RUnlock()
	if !ok || record.TenantID != tenantID || record.State == ports.NetworkResourceDeleted {
		return ports.NetworkVPCRecord{}, fmt.Errorf("%w: vpc not found", ports.ErrNotFound)
	}
	return record, nil
}

func (s *LocalNetworkService) lookupSubnet(ctx context.Context, tenantID, subnetID string) (ports.NetworkSubnetRecord, error) {
	if s.store != nil {
		record, err := s.store.GetSubnet(ctx, tenantID, subnetID)
		if err != nil {
			return ports.NetworkSubnetRecord{}, fmt.Errorf("%w: subnet not found", ports.ErrNotFound)
		}
		if record.State == ports.NetworkResourceDeleted {
			return ports.NetworkSubnetRecord{}, fmt.Errorf("%w: subnet not found", ports.ErrNotFound)
		}
		return record, nil
	}
	s.mu.RLock()
	record, ok := s.subnets[subnetID]
	s.mu.RUnlock()
	if !ok || record.TenantID != tenantID || record.State == ports.NetworkResourceDeleted {
		return ports.NetworkSubnetRecord{}, fmt.Errorf("%w: subnet not found", ports.ErrNotFound)
	}
	return record, nil
}

func (s *LocalNetworkService) ensureRouteDoesNotConflict(ctx context.Context, tenantID string, vpcID string, destinationCIDR string) error {
	routes, err := s.ListRoutes(ctx, ports.NetworkRouteListRequest{TenantID: tenantID, VPCID: vpcID})
	if err != nil {
		return err
	}
	for _, route := range routes {
		if route.State != ports.NetworkResourceDeleted && route.DestinationCIDR == destinationCIDR {
			return fmt.Errorf("%w: route destination %s already exists in VPC %s", ports.ErrConflict, destinationCIDR, vpcID)
		}
	}
	return nil
}

func (s *LocalNetworkService) ensureNoDeleteDependencies(ctx context.Context, request ports.NetworkResourceGetRequest, resourceKind string) error {
	if s.store == nil {
		return nil
	}
	reasons, err := s.store.CountDeleteDependencies(ctx, request, resourceKind)
	if err != nil {
		return err
	}
	if len(reasons) > 0 {
		return fmt.Errorf("%w: %s", ports.ErrConflict, strings.Join(reasons, "; "))
	}
	return nil
}

func (s *LocalNetworkService) ensureNoDeleteDependenciesLocked(request ports.NetworkResourceGetRequest, resourceKind string) error {
	reasons := make([]string, 0)
	switch resourceKind {
	case "vpc":
		for _, subnet := range s.subnets {
			if subnet.TenantID == request.TenantID && subnet.VPCID == request.ResourceID && subnet.State != ports.NetworkResourceDeleted {
				reasons = append(reasons, "VPC still has associated subnets")
				break
			}
		}
		for _, lb := range s.loadBalancers {
			if lb.TenantID == request.TenantID && lb.VPCID == request.ResourceID && lb.State != ports.NetworkResourceDeleted {
				reasons = append(reasons, "VPC still has associated load balancers")
				break
			}
		}
		for _, route := range s.routes {
			if route.TenantID == request.TenantID && route.VPCID == request.ResourceID && route.State != ports.NetworkResourceDeleted {
				reasons = append(reasons, "VPC still has associated routes")
				break
			}
		}
	case "subnet":
		for _, lb := range s.loadBalancers {
			if lb.TenantID == request.TenantID && lb.SubnetID == request.ResourceID && lb.State != ports.NetworkResourceDeleted {
				reasons = append(reasons, "Subnet is still used by load balancers")
				break
			}
		}
	}
	if len(reasons) > 0 {
		return fmt.Errorf("%w: %s", ports.ErrConflict, strings.Join(reasons, "; "))
	}
	return nil
}

var _ ports.NetworkService = (*LocalNetworkService)(nil)
