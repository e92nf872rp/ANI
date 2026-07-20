package router

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

type networkAPI struct {
	service ports.NetworkService
}

type networkCreateVPCRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Name           string `json:"name"`
	CIDR           string `json:"cidr"`
}

type networkCreateSubnetRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	VPCID          string `json:"vpc_id"`
	Name           string `json:"name"`
	CIDR           string `json:"cidr"`
	Gateway        string `json:"gateway"`
}

type networkCreateSecurityGroupRequest struct {
	IdempotencyKey string                     `json:"idempotency_key"`
	Name           string                     `json:"name"`
	Description    string                     `json:"description"`
	Rules          []networkSecurityGroupRule `json:"rules"`
}

type networkSecurityGroupRule struct {
	Direction string `json:"direction"`
	Protocol  string `json:"protocol"`
	PortRange string `json:"port_range"`
	CIDR      string `json:"cidr"`
	Action    string `json:"action"`
}

type networkCreateLoadBalancerRequest struct {
	IdempotencyKey string                    `json:"idempotency_key"`
	Name           string                    `json:"name"`
	VPCID          string                    `json:"vpc_id"`
	SubnetID       string                    `json:"subnet_id"`
	Scheme         string                    `json:"scheme"`
	Listeners      []networkLBListenerRecord `json:"listeners"`
}

type networkCreateRouteRequest struct {
	IdempotencyKey  string `json:"idempotency_key"`
	VPCID           string `json:"vpc_id"`
	DestinationCIDR string `json:"destination_cidr"`
	NextHopType     string `json:"next_hop_type"`
	NextHopID       string `json:"next_hop_id"`
	Description     string `json:"description,omitempty"`
}

type networkCreateSecurityGroupRuleRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Priority       int    `json:"priority"`
	Direction      string `json:"direction"`
	Protocol       string `json:"protocol"`
	PortRange      string `json:"port_range"`
	CIDR           string `json:"cidr"`
	Action         string `json:"action"`
	Description    string `json:"description,omitempty"`
}

type networkUpdateSecurityGroupRuleRequest struct {
	Priority    int    `json:"priority,omitempty"`
	Direction   string `json:"direction,omitempty"`
	Protocol    string `json:"protocol,omitempty"`
	PortRange   string `json:"port_range,omitempty"`
	CIDR        string `json:"cidr,omitempty"`
	Action      string `json:"action,omitempty"`
	Description string `json:"description,omitempty"`
}

type networkCreateSecurityGroupBindingRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	TargetType     string `json:"target_type"`
	TargetID       string `json:"target_id"`
}

type networkLBListenerRecord struct {
	Protocol   string `json:"protocol"`
	Port       int32  `json:"port"`
	TargetPort int32  `json:"target_port"`
}

type networkVPCResponse struct {
	ID         string                 `json:"id"`
	TenantID   string                 `json:"tenant_id"`
	Name       string                 `json:"name"`
	CIDR       string                 `json:"cidr"`
	State      string                 `json:"state"`
	Reason     string                 `json:"reason,omitempty"`
	DevProfile coreDevProfileResponse `json:"dev_profile"`
	CreatedAt  string                 `json:"created_at"`
	UpdatedAt  string                 `json:"updated_at"`
}

type networkSubnetResponse struct {
	ID         string                 `json:"id"`
	TenantID   string                 `json:"tenant_id"`
	VPCID      string                 `json:"vpc_id"`
	Name       string                 `json:"name"`
	CIDR       string                 `json:"cidr"`
	Gateway    string                 `json:"gateway,omitempty"`
	State      string                 `json:"state"`
	Reason     string                 `json:"reason,omitempty"`
	DevProfile coreDevProfileResponse `json:"dev_profile"`
	CreatedAt  string                 `json:"created_at"`
	UpdatedAt  string                 `json:"updated_at"`
}

type networkSecurityGroupResponse struct {
	ID          string                     `json:"id"`
	TenantID    string                     `json:"tenant_id"`
	Name        string                     `json:"name"`
	Description string                     `json:"description,omitempty"`
	Rules       []networkSecurityGroupRule `json:"rules"`
	State       string                     `json:"state"`
	Reason      string                     `json:"reason,omitempty"`
	DevProfile  coreDevProfileResponse     `json:"dev_profile"`
	CreatedAt   string                     `json:"created_at"`
	UpdatedAt   string                     `json:"updated_at"`
}

type networkLoadBalancerResponse struct {
	ID         string                    `json:"id"`
	TenantID   string                    `json:"tenant_id"`
	Name       string                    `json:"name"`
	VPCID      string                    `json:"vpc_id"`
	SubnetID   string                    `json:"subnet_id,omitempty"`
	Scheme     string                    `json:"scheme"`
	VIP        string                    `json:"vip,omitempty"`
	Listeners  []networkLBListenerRecord `json:"listeners"`
	State      string                    `json:"state"`
	Reason     string                    `json:"reason,omitempty"`
	DevProfile coreDevProfileResponse    `json:"dev_profile"`
	CreatedAt  string                    `json:"created_at"`
	UpdatedAt  string                    `json:"updated_at"`
}

type networkRouteResponse struct {
	ID              string                 `json:"id"`
	VPCID           string                 `json:"vpc_id"`
	DestinationCIDR string                 `json:"destination_cidr"`
	NextHopType     string                 `json:"next_hop_type"`
	NextHopID       string                 `json:"next_hop_id"`
	Description     string                 `json:"description,omitempty"`
	CreatedAt       string                 `json:"created_at"`
	DevProfile      coreDevProfileResponse `json:"dev_profile"`
}

type networkOverviewResourceSummaryResponse struct {
	Kind      string `json:"kind"`
	Total     int    `json:"total"`
	Available int    `json:"available"`
	Pending   int    `json:"pending"`
	Failed    int    `json:"failed"`
	Deleting  int    `json:"deleting"`
}

type networkOverviewCapabilityResponse struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Status      string `json:"status"`
	Path        string `json:"path,omitempty"`
	Description string `json:"description,omitempty"`
}

type networkOverviewRelationshipResponse struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Relation string `json:"relation"`
}

type networkOverviewDeleteRiskResponse struct {
	Kind string `json:"kind"`
	Risk string `json:"risk"`
}

type networkOverviewResponse struct {
	Resources     []networkOverviewResourceSummaryResponse `json:"resources"`
	Capabilities  []networkOverviewCapabilityResponse      `json:"capabilities"`
	CreateOrder   []string                                 `json:"create_order"`
	Relationships []networkOverviewRelationshipResponse    `json:"relationships"`
	DeleteRisks   []networkOverviewDeleteRiskResponse      `json:"delete_risks"`
}

type networkSubnetIPAllocationResponse struct {
	ID           string `json:"id"`
	SubnetID     string `json:"subnet_id"`
	IPAddress    string `json:"ip_address"`
	ResourceType string `json:"resource_type,omitempty"`
	ResourceID   string `json:"resource_id,omitempty"`
	State        string `json:"state"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}

type networkSecurityGroupRuleResponse struct {
	ID              string `json:"id"`
	SecurityGroupID string `json:"security_group_id"`
	Priority        int    `json:"priority"`
	Direction       string `json:"direction"`
	Protocol        string `json:"protocol"`
	PortRange       string `json:"port_range"`
	CIDR            string `json:"cidr"`
	Action          string `json:"action"`
	Description     string `json:"description,omitempty"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at,omitempty"`
}

type networkSecurityGroupBindingResponse struct {
	ID              string `json:"id"`
	SecurityGroupID string `json:"security_group_id"`
	TargetType      string `json:"target_type"`
	TargetID        string `json:"target_id"`
	CreatedAt       string `json:"created_at"`
}

func newNetworkAPI() *networkAPI {
	return newNetworkAPIWithService(nil)
}

func newNetworkAPIWithService(service ports.NetworkService) *networkAPI {
	if service == nil {
		service = runtimeadapter.NewLocalNetworkService()
	}
	return &networkAPI{service: service}
}

func registerNetworkResources(v1 *route.RouterGroup) {
	registerNetworkResourcesWithService(v1, nil)
}

func registerNetworkResourcesWithService(v1 *route.RouterGroup, service ports.NetworkService) {
	api := newNetworkAPIWithService(service)
	v1.GET("/networks/overview", api.getOverview)

	v1.GET("/networks/vpcs", api.listVPCs)
	v1.POST("/networks/vpcs", api.createVPC)
	v1.GET("/networks/vpcs/:vpc_id", api.getVPC)
	v1.DELETE("/networks/vpcs/:vpc_id", api.deleteVPC)

	v1.GET("/networks/subnets", api.listSubnets)
	v1.POST("/networks/subnets", api.createSubnet)
	v1.GET("/networks/subnets/:subnet_id", api.getSubnet)
	v1.DELETE("/networks/subnets/:subnet_id", api.deleteSubnet)
	v1.GET("/networks/subnets/:subnet_id/ip-allocations", api.listSubnetIPAllocations)

	v1.GET("/networks/security-groups", api.listSecurityGroups)
	v1.POST("/networks/security-groups", api.createSecurityGroup)
	v1.GET("/networks/security-groups/:security_group_id", api.getSecurityGroup)
	v1.DELETE("/networks/security-groups/:security_group_id", api.deleteSecurityGroup)
	v1.GET("/networks/security-groups/:security_group_id/rules", api.listSecurityGroupRules)
	v1.POST("/networks/security-groups/:security_group_id/rules", api.createSecurityGroupRule)
	v1.GET("/networks/security-groups/:security_group_id/rules/:rule_id", api.getSecurityGroupRule)
	v1.PUT("/networks/security-groups/:security_group_id/rules/:rule_id", api.updateSecurityGroupRule)
	v1.DELETE("/networks/security-groups/:security_group_id/rules/:rule_id", api.deleteSecurityGroupRule)
	v1.GET("/networks/security-groups/:security_group_id/bindings", api.listSecurityGroupBindings)
	v1.POST("/networks/security-groups/:security_group_id/bindings", api.createSecurityGroupBinding)
	v1.DELETE("/networks/security-groups/:security_group_id/bindings/:binding_id", api.deleteSecurityGroupBinding)

	v1.GET("/networks/load-balancers", api.listLoadBalancers)
	v1.POST("/networks/load-balancers", api.createLoadBalancer)
	v1.GET("/networks/load-balancers/:load_balancer_id", api.getLoadBalancer)
	v1.DELETE("/networks/load-balancers/:load_balancer_id", api.deleteLoadBalancer)

	v1.GET("/networks/routes", api.listRoutes)
	v1.POST("/networks/routes", api.createRoute)
	v1.GET("/networks/routes/:route_id", api.getRoute)
	v1.DELETE("/networks/routes/:route_id", api.deleteRoute)
}

func (api *networkAPI) getOverview(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.GetOverview(ctx, ports.NetworkOverviewRequest{TenantID: demoTenantID(c)})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusOK, networkOverviewFromRecord(record))
}

func (api *networkAPI) createVPC(ctx context.Context, c *app.RequestContext) {
	var req networkCreateVPCRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid vpc request")
		return
	}
	record, err := api.service.CreateVPC(ctx, ports.NetworkVPCCreateRequest{
		TenantID:       demoTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		Name:           req.Name,
		CIDR:           req.CIDR,
	})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusCreated, networkVPCFromRecord(record))
}

func (api *networkAPI) listVPCs(ctx context.Context, c *app.RequestContext) {
	records, err := api.service.ListVPCs(ctx, ports.NetworkResourceListRequest{
		TenantID: demoTenantID(c),
		Name:     c.Query("name"),
		State:    ports.NetworkResourceState(c.Query("state")),
	})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	items := make([]networkVPCResponse, 0, len(records))
	for _, record := range records {
		items = append(items, networkVPCFromRecord(record))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items), "next_cursor": nil})
}

func (api *networkAPI) getVPC(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.GetVPC(ctx, ports.NetworkResourceGetRequest{TenantID: demoTenantID(c), ResourceID: c.Param("vpc_id")})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusOK, networkVPCFromRecord(record))
}

func (api *networkAPI) deleteVPC(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.DeleteVPC(ctx, ports.NetworkResourceGetRequest{TenantID: demoTenantID(c), ResourceID: c.Param("vpc_id")})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusOK, networkVPCFromRecord(record))
}

func (api *networkAPI) createSubnet(ctx context.Context, c *app.RequestContext) {
	var req networkCreateSubnetRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid subnet request")
		return
	}
	record, err := api.service.CreateSubnet(ctx, ports.NetworkSubnetCreateRequest{
		TenantID:       demoTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		VPCID:          req.VPCID,
		Name:           req.Name,
		CIDR:           req.CIDR,
		Gateway:        req.Gateway,
	})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusCreated, networkSubnetFromRecord(record))
}

func (api *networkAPI) listSubnets(ctx context.Context, c *app.RequestContext) {
	records, err := api.service.ListSubnets(ctx, ports.NetworkResourceListRequest{
		TenantID: demoTenantID(c),
		VPCID:    c.Query("vpc_id"),
		State:    ports.NetworkResourceState(c.Query("state")),
	})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	items := make([]networkSubnetResponse, 0, len(records))
	for _, record := range records {
		items = append(items, networkSubnetFromRecord(record))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items), "next_cursor": nil})
}

func (api *networkAPI) getSubnet(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.GetSubnet(ctx, ports.NetworkResourceGetRequest{TenantID: demoTenantID(c), ResourceID: c.Param("subnet_id")})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusOK, networkSubnetFromRecord(record))
}

func (api *networkAPI) deleteSubnet(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.DeleteSubnet(ctx, ports.NetworkResourceGetRequest{TenantID: demoTenantID(c), ResourceID: c.Param("subnet_id")})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusOK, networkSubnetFromRecord(record))
}

func (api *networkAPI) listSubnetIPAllocations(ctx context.Context, c *app.RequestContext) {
	records, err := api.service.ListSubnetIPAllocations(ctx, ports.NetworkSubnetIPAllocationListRequest{
		TenantID:     demoTenantID(c),
		SubnetID:     c.Param("subnet_id"),
		State:        c.Query("state"),
		ResourceType: c.Query("resource_type"),
	})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	items := make([]networkSubnetIPAllocationResponse, 0, len(records))
	for _, record := range records {
		items = append(items, networkSubnetIPAllocationFromRecord(record))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items), "next_cursor": nil})
}

func (api *networkAPI) createSecurityGroup(ctx context.Context, c *app.RequestContext) {
	var req networkCreateSecurityGroupRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid security group request")
		return
	}
	record, err := api.service.CreateSecurityGroup(ctx, ports.NetworkSecurityGroupCreateRequest{
		TenantID:       demoTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		Name:           req.Name,
		Description:    req.Description,
		Rules:          networkRulesToPorts(req.Rules),
	})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusCreated, networkSecurityGroupFromRecord(record))
}

func (api *networkAPI) listSecurityGroups(ctx context.Context, c *app.RequestContext) {
	records, err := api.service.ListSecurityGroups(ctx, ports.NetworkResourceListRequest{
		TenantID: demoTenantID(c),
		Name:     c.Query("name"),
		State:    ports.NetworkResourceState(c.Query("state")),
	})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	items := make([]networkSecurityGroupResponse, 0, len(records))
	for _, record := range records {
		items = append(items, networkSecurityGroupFromRecord(record))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items), "next_cursor": nil})
}

func (api *networkAPI) getSecurityGroup(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.GetSecurityGroup(ctx, ports.NetworkResourceGetRequest{TenantID: demoTenantID(c), ResourceID: c.Param("security_group_id")})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusOK, networkSecurityGroupFromRecord(record))
}

func (api *networkAPI) deleteSecurityGroup(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.DeleteSecurityGroup(ctx, ports.NetworkResourceGetRequest{TenantID: demoTenantID(c), ResourceID: c.Param("security_group_id")})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusOK, networkSecurityGroupFromRecord(record))
}

func (api *networkAPI) listSecurityGroupRules(ctx context.Context, c *app.RequestContext) {
	records, err := api.service.ListSecurityGroupRules(ctx, ports.NetworkSecurityGroupRuleListRequest{
		TenantID:        demoTenantID(c),
		SecurityGroupID: c.Param("security_group_id"),
		Direction:       c.Query("direction"),
		Protocol:        c.Query("protocol"),
	})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	items := make([]networkSecurityGroupRuleResponse, 0, len(records))
	for _, record := range records {
		items = append(items, networkSecurityGroupRuleFromRecord(record))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items), "next_cursor": nil})
}

func (api *networkAPI) createSecurityGroupRule(ctx context.Context, c *app.RequestContext) {
	var req networkCreateSecurityGroupRuleRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid security group rule request")
		return
	}
	record, err := api.service.CreateSecurityGroupRule(ctx, ports.NetworkSecurityGroupRuleCreateRequest{
		TenantID:        demoTenantID(c),
		SecurityGroupID: c.Param("security_group_id"),
		IdempotencyKey:  req.IdempotencyKey,
		Priority:        req.Priority,
		Direction:       req.Direction,
		Protocol:        req.Protocol,
		PortRange:       req.PortRange,
		CIDR:            req.CIDR,
		Action:          req.Action,
		Description:     req.Description,
	})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusCreated, networkSecurityGroupRuleFromRecord(record))
}

func (api *networkAPI) getSecurityGroupRule(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.GetSecurityGroupRule(ctx, ports.NetworkSecurityGroupRuleGetRequest{
		TenantID:        demoTenantID(c),
		SecurityGroupID: c.Param("security_group_id"),
		RuleID:          c.Param("rule_id"),
	})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusOK, networkSecurityGroupRuleFromRecord(record))
}

func (api *networkAPI) updateSecurityGroupRule(ctx context.Context, c *app.RequestContext) {
	var req networkUpdateSecurityGroupRuleRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid security group rule request")
		return
	}
	record, err := api.service.UpdateSecurityGroupRule(ctx, ports.NetworkSecurityGroupRuleUpdateRequest{
		TenantID:        demoTenantID(c),
		SecurityGroupID: c.Param("security_group_id"),
		RuleID:          c.Param("rule_id"),
		Priority:        req.Priority,
		Direction:       req.Direction,
		Protocol:        req.Protocol,
		PortRange:       req.PortRange,
		CIDR:            req.CIDR,
		Action:          req.Action,
		Description:     req.Description,
	})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusOK, networkSecurityGroupRuleFromRecord(record))
}

func (api *networkAPI) deleteSecurityGroupRule(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.DeleteSecurityGroupRule(ctx, ports.NetworkSecurityGroupRuleGetRequest{
		TenantID:        demoTenantID(c),
		SecurityGroupID: c.Param("security_group_id"),
		RuleID:          c.Param("rule_id"),
	})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusOK, networkSecurityGroupRuleFromRecord(record))
}

func (api *networkAPI) listSecurityGroupBindings(ctx context.Context, c *app.RequestContext) {
	records, err := api.service.ListSecurityGroupBindings(ctx, ports.NetworkSecurityGroupBindingListRequest{
		TenantID:        demoTenantID(c),
		SecurityGroupID: c.Param("security_group_id"),
		TargetType:      c.Query("target_type"),
		TargetID:        c.Query("target_id"),
	})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	items := make([]networkSecurityGroupBindingResponse, 0, len(records))
	for _, record := range records {
		items = append(items, networkSecurityGroupBindingFromRecord(record))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items), "next_cursor": nil})
}

func (api *networkAPI) createSecurityGroupBinding(ctx context.Context, c *app.RequestContext) {
	var req networkCreateSecurityGroupBindingRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid security group binding request")
		return
	}
	record, err := api.service.CreateSecurityGroupBinding(ctx, ports.NetworkSecurityGroupBindingCreateRequest{
		TenantID:        demoTenantID(c),
		SecurityGroupID: c.Param("security_group_id"),
		IdempotencyKey:  req.IdempotencyKey,
		TargetType:      req.TargetType,
		TargetID:        req.TargetID,
	})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusCreated, networkSecurityGroupBindingFromRecord(record))
}

func (api *networkAPI) deleteSecurityGroupBinding(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.DeleteSecurityGroupBinding(ctx, ports.NetworkSecurityGroupBindingDeleteRequest{
		TenantID:        demoTenantID(c),
		SecurityGroupID: c.Param("security_group_id"),
		BindingID:       c.Param("binding_id"),
	})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusOK, networkSecurityGroupBindingFromRecord(record))
}

func (api *networkAPI) createLoadBalancer(ctx context.Context, c *app.RequestContext) {
	var req networkCreateLoadBalancerRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid load balancer request")
		return
	}
	record, err := api.service.CreateLoadBalancer(ctx, ports.NetworkLoadBalancerCreateRequest{
		TenantID:       demoTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		Name:           req.Name,
		VPCID:          req.VPCID,
		SubnetID:       req.SubnetID,
		Scheme:         req.Scheme,
		Listeners:      networkListenersToPorts(req.Listeners),
	})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusCreated, networkLoadBalancerFromRecord(record))
}

func (api *networkAPI) listLoadBalancers(ctx context.Context, c *app.RequestContext) {
	records, err := api.service.ListLoadBalancers(ctx, ports.NetworkResourceListRequest{
		TenantID: demoTenantID(c),
		VPCID:    c.Query("vpc_id"),
		State:    ports.NetworkResourceState(c.Query("state")),
		Scheme:   c.Query("scheme"),
	})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	items := make([]networkLoadBalancerResponse, 0, len(records))
	for _, record := range records {
		items = append(items, networkLoadBalancerFromRecord(record))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items), "next_cursor": nil})
}

func (api *networkAPI) getLoadBalancer(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.GetLoadBalancer(ctx, ports.NetworkResourceGetRequest{TenantID: demoTenantID(c), ResourceID: c.Param("load_balancer_id")})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusOK, networkLoadBalancerFromRecord(record))
}

func (api *networkAPI) deleteLoadBalancer(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.DeleteLoadBalancer(ctx, ports.NetworkResourceGetRequest{TenantID: demoTenantID(c), ResourceID: c.Param("load_balancer_id")})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusOK, networkLoadBalancerFromRecord(record))
}

func (api *networkAPI) createRoute(ctx context.Context, c *app.RequestContext) {
	var req networkCreateRouteRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid route request")
		return
	}
	record, err := api.service.CreateRoute(ctx, ports.NetworkRouteCreateRequest{
		TenantID:        demoTenantID(c),
		IdempotencyKey:  req.IdempotencyKey,
		VPCID:           req.VPCID,
		DestinationCIDR: req.DestinationCIDR,
		NextHopType:     req.NextHopType,
		NextHopID:       req.NextHopID,
		Description:     req.Description,
	})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusCreated, networkRouteFromRecord(record))
}

func (api *networkAPI) listRoutes(ctx context.Context, c *app.RequestContext) {
	records, err := api.service.ListRoutes(ctx, ports.NetworkRouteListRequest{
		TenantID:    demoTenantID(c),
		VPCID:       c.Query("vpc_id"),
		NextHopType: c.Query("next_hop_type"),
	})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	items := make([]networkRouteResponse, 0, len(records))
	for _, record := range records {
		items = append(items, networkRouteFromRecord(record))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items), "next_cursor": nil})
}

func (api *networkAPI) getRoute(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.GetRoute(ctx, ports.NetworkResourceGetRequest{TenantID: demoTenantID(c), ResourceID: c.Param("route_id")})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusOK, networkRouteFromRecord(record))
}

func (api *networkAPI) deleteRoute(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.DeleteRoute(ctx, ports.NetworkResourceGetRequest{TenantID: demoTenantID(c), ResourceID: c.Param("route_id")})
	if err != nil {
		writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusOK, networkRouteFromRecord(record))
}

func networkVPCFromRecord(record ports.NetworkVPCRecord) networkVPCResponse {
	return networkVPCResponse{
		ID:         record.VPCID,
		TenantID:   record.TenantID,
		Name:       record.Name,
		CIDR:       record.CIDR,
		State:      string(record.State),
		Reason:     record.Reason,
		DevProfile: localCoreDevProfile("local-network-service", "Core dev/local profile; provider execution is gated separately"),
		CreatedAt:  networkTime(record.CreatedAt),
		UpdatedAt:  networkTime(record.UpdatedAt),
	}
}

func networkSubnetFromRecord(record ports.NetworkSubnetRecord) networkSubnetResponse {
	return networkSubnetResponse{
		ID:         record.SubnetID,
		TenantID:   record.TenantID,
		VPCID:      record.VPCID,
		Name:       record.Name,
		CIDR:       record.CIDR,
		Gateway:    record.Gateway,
		State:      string(record.State),
		Reason:     record.Reason,
		DevProfile: localCoreDevProfile("local-network-service", "Core dev/local profile; provider execution is gated separately"),
		CreatedAt:  networkTime(record.CreatedAt),
		UpdatedAt:  networkTime(record.UpdatedAt),
	}
}

func networkSecurityGroupFromRecord(record ports.NetworkSecurityGroupRecord) networkSecurityGroupResponse {
	return networkSecurityGroupResponse{
		ID:          record.SecurityGroupID,
		TenantID:    record.TenantID,
		Name:        record.Name,
		Description: record.Description,
		Rules:       networkRulesFromPorts(record.Rules),
		State:       string(record.State),
		Reason:      record.Reason,
		DevProfile:  localCoreDevProfile("local-network-service", "Core dev/local profile; provider execution is gated separately"),
		CreatedAt:   networkTime(record.CreatedAt),
		UpdatedAt:   networkTime(record.UpdatedAt),
	}
}

func networkLoadBalancerFromRecord(record ports.NetworkLoadBalancerRecord) networkLoadBalancerResponse {
	return networkLoadBalancerResponse{
		ID:         record.LoadBalancerID,
		TenantID:   record.TenantID,
		Name:       record.Name,
		VPCID:      record.VPCID,
		SubnetID:   record.SubnetID,
		Scheme:     record.Scheme,
		VIP:        record.VIP,
		Listeners:  networkListenersFromPorts(record.Listeners),
		State:      string(record.State),
		Reason:     record.Reason,
		DevProfile: localCoreDevProfile("local-network-service", "Core dev/local profile; provider execution is gated separately"),
		CreatedAt:  networkTime(record.CreatedAt),
		UpdatedAt:  networkTime(record.UpdatedAt),
	}
}

func networkRouteFromRecord(record ports.NetworkRouteRecord) networkRouteResponse {
	devProfile := localCoreDevProfile("local-network-service", "Core dev/local profile; route provider execution is gated separately")
	if record.RealProvider {
		provider := record.Provider
		if provider == "" {
			provider = "kubeovn-network-provider"
		}
		devProfile = coreDevProfileResponse{
			Mode:         "real",
			Provider:     provider,
			RealProvider: true,
			Reason:       "Network route was applied through the configured network provider",
		}
	}
	return networkRouteResponse{
		ID:              record.RouteID,
		VPCID:           record.VPCID,
		DestinationCIDR: record.DestinationCIDR,
		NextHopType:     record.NextHopType,
		NextHopID:       record.NextHopID,
		Description:     record.Description,
		CreatedAt:       networkTime(record.CreatedAt),
		DevProfile:      devProfile,
	}
}

func networkOverviewFromRecord(record ports.NetworkOverviewRecord) networkOverviewResponse {
	kinds := []string{"vpc", "subnet", "security_group", "load_balancer", "route"}
	resources := make([]networkOverviewResourceSummaryResponse, 0, len(kinds))
	for _, kind := range kinds {
		summary := record.Resources[kind]
		if summary.Kind == "" {
			summary.Kind = kind
		}
		resources = append(resources, networkOverviewResourceSummaryResponse{
			Kind:      summary.Kind,
			Total:     summary.Total,
			Available: summary.Available,
			Pending:   summary.Pending,
			Failed:    summary.Failed,
			Deleting:  summary.Deleting,
		})
	}
	capabilities := make([]networkOverviewCapabilityResponse, 0, len(record.Capabilities))
	for _, capability := range record.Capabilities {
		capabilities = append(capabilities, networkOverviewCapabilityResponse{
			Key:         capability.Key,
			Label:       capability.Label,
			Status:      capability.Status,
			Path:        capability.Path,
			Description: capability.Description,
		})
	}
	relationships := make([]networkOverviewRelationshipResponse, 0, len(record.Relationships))
	for _, relationship := range record.Relationships {
		relationships = append(relationships, networkOverviewRelationshipResponse{
			Source:   relationship.Source,
			Target:   relationship.Target,
			Relation: relationship.Relation,
		})
	}
	deleteRisks := make([]networkOverviewDeleteRiskResponse, 0, len(record.DeleteRisks))
	for _, risk := range record.DeleteRisks {
		deleteRisks = append(deleteRisks, networkOverviewDeleteRiskResponse{Kind: risk.Kind, Risk: risk.Risk})
	}
	return networkOverviewResponse{
		Resources:     resources,
		Capabilities:  capabilities,
		CreateOrder:   append([]string(nil), record.CreateOrder...),
		Relationships: relationships,
		DeleteRisks:   deleteRisks,
	}
}

func networkSubnetIPAllocationFromRecord(record ports.NetworkSubnetIPAllocationRecord) networkSubnetIPAllocationResponse {
	return networkSubnetIPAllocationResponse{
		ID:           record.AllocationID,
		SubnetID:     record.SubnetID,
		IPAddress:    record.IPAddress,
		ResourceType: record.ResourceType,
		ResourceID:   record.ResourceID,
		State:        record.State,
		CreatedAt:    networkTime(record.CreatedAt),
		UpdatedAt:    networkTime(record.UpdatedAt),
	}
}

func networkSecurityGroupRuleFromRecord(record ports.NetworkSecurityGroupRuleRecord) networkSecurityGroupRuleResponse {
	return networkSecurityGroupRuleResponse{
		ID:              record.RuleID,
		SecurityGroupID: record.SecurityGroupID,
		Priority:        record.Priority,
		Direction:       record.Direction,
		Protocol:        record.Protocol,
		PortRange:       record.PortRange,
		CIDR:            record.CIDR,
		Action:          record.Action,
		Description:     record.Description,
		CreatedAt:       networkTime(record.CreatedAt),
		UpdatedAt:       networkTime(record.UpdatedAt),
	}
}

func networkSecurityGroupBindingFromRecord(record ports.NetworkSecurityGroupBindingRecord) networkSecurityGroupBindingResponse {
	return networkSecurityGroupBindingResponse{
		ID:              record.BindingID,
		SecurityGroupID: record.SecurityGroupID,
		TargetType:      record.TargetType,
		TargetID:        record.TargetID,
		CreatedAt:       networkTime(record.CreatedAt),
	}
}

func networkRulesToPorts(items []networkSecurityGroupRule) []ports.NetworkSecurityGroupRule {
	rules := make([]ports.NetworkSecurityGroupRule, 0, len(items))
	for _, item := range items {
		rules = append(rules, ports.NetworkSecurityGroupRule{
			Direction: item.Direction,
			Protocol:  item.Protocol,
			PortRange: item.PortRange,
			CIDR:      item.CIDR,
			Action:    item.Action,
		})
	}
	return rules
}

func networkRulesFromPorts(items []ports.NetworkSecurityGroupRule) []networkSecurityGroupRule {
	rules := make([]networkSecurityGroupRule, 0, len(items))
	sort.Slice(items, func(i, j int) bool {
		if items[i].Priority == items[j].Priority {
			return items[i].Direction < items[j].Direction
		}
		return items[i].Priority < items[j].Priority
	})
	for _, item := range items {
		rules = append(rules, networkSecurityGroupRule{
			Direction: item.Direction,
			Protocol:  item.Protocol,
			PortRange: item.PortRange,
			CIDR:      item.CIDR,
			Action:    item.Action,
		})
	}
	return rules
}

func networkListenersToPorts(items []networkLBListenerRecord) []ports.NetworkLoadBalancerListener {
	listeners := make([]ports.NetworkLoadBalancerListener, 0, len(items))
	for _, item := range items {
		listeners = append(listeners, ports.NetworkLoadBalancerListener{
			Protocol:   item.Protocol,
			Port:       item.Port,
			TargetPort: item.TargetPort,
		})
	}
	return listeners
}

func networkListenersFromPorts(items []ports.NetworkLoadBalancerListener) []networkLBListenerRecord {
	listeners := make([]networkLBListenerRecord, 0, len(items))
	for _, item := range items {
		listeners = append(listeners, networkLBListenerRecord{
			Protocol:   item.Protocol,
			Port:       item.Port,
			TargetPort: item.TargetPort,
		})
	}
	return listeners
}

func networkTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func writeNetworkError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		writeDemoError(c, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrConflict):
		writeDemoError(c, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, ports.ErrInvalid):
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	default:
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	}
}
