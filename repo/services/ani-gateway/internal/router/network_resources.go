package router

import (
	"context"
	"errors"
	"net/http"
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

func newNetworkAPI() *networkAPI {
	return newNetworkAPIWithService(nil)
}

func newNetworkAPIWithService(service ports.NetworkService) *networkAPI {
	if service == nil {
		service = runtimeadapter.NewLocalNetworkService()
	}
	return &networkAPI{service: service}
}

func registerNetworkResourcesWithService(v1 *route.RouterGroup, service ports.NetworkService) {
	api := newNetworkAPIWithService(service)
	v1.GET("/networks/vpcs", api.listVPCs)
	v1.POST("/networks/vpcs", api.createVPC)
	v1.GET("/networks/vpcs/:vpc_id", api.getVPC)
	v1.DELETE("/networks/vpcs/:vpc_id", api.deleteVPC)

	v1.GET("/networks/subnets", api.listSubnets)
	v1.POST("/networks/subnets", api.createSubnet)
	v1.GET("/networks/subnets/:subnet_id", api.getSubnet)
	v1.DELETE("/networks/subnets/:subnet_id", api.deleteSubnet)

	v1.GET("/networks/security-groups", api.listSecurityGroups)
	v1.POST("/networks/security-groups", api.createSecurityGroup)
	v1.GET("/networks/security-groups/:security_group_id", api.getSecurityGroup)
	v1.DELETE("/networks/security-groups/:security_group_id", api.deleteSecurityGroup)

	v1.GET("/networks/load-balancers", api.listLoadBalancers)
	v1.POST("/networks/load-balancers", api.createLoadBalancer)
	v1.GET("/networks/load-balancers/:load_balancer_id", api.getLoadBalancer)
	v1.DELETE("/networks/load-balancers/:load_balancer_id", api.deleteLoadBalancer)

	v1.GET("/networks/routes", api.listRoutes)
	v1.POST("/networks/routes", api.createRoute)
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
	records, err := api.service.ListVPCs(ctx, ports.NetworkResourceListRequest{TenantID: demoTenantID(c)})
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
	records, err := api.service.ListSubnets(ctx, ports.NetworkResourceListRequest{TenantID: demoTenantID(c)})
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
	records, err := api.service.ListSecurityGroups(ctx, ports.NetworkResourceListRequest{TenantID: demoTenantID(c)})
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
	records, err := api.service.ListLoadBalancers(ctx, ports.NetworkResourceListRequest{TenantID: demoTenantID(c)})
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
		TenantID: demoTenantID(c),
		VPCID:    c.Query("vpc_id"),
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
