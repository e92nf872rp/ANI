package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

// BillingService 是 gRPC BillingService server（BOSS 租户计费结算，方案 v0.3）。
// 用量数据一律经 ports.BillingMeteringClient（Core OpenAPI GET /metering/usage/platform），
// 自有数据经 ports.BillingStore（billing_* 4 表）；单价只读 billing_pricing 表（零代码字面量）。
type BillingService struct {
	// 嵌入未实现接口，确保 proto 新增 RPC 后本结构仍能向后兼容（栅栏模式）。
	tenantv1.UnimplementedBillingServiceServer

	store    ports.BillingStore          // billing_invoices/adjustments/credit_accounts/pricing
	metering ports.BillingMeteringClient // Core 平台跨租户用量聚合
	tenants  ports.TenantSvcClient       // Core 租户 API（存在性校验 + 名称映射）
}

// NewBillingService 构造计费结算 gRPC 服务实例。
func NewBillingService(store ports.BillingStore, metering ports.BillingMeteringClient, tenants ports.TenantSvcClient) *BillingService {
	return &BillingService{store: store, metering: metering, tenants: tenants}
}

// Register 向 gRPC server 注册本服务（services/pkg/bootstrap.RunGRPC 会调用）。
func (s *BillingService) Register(server *grpc.Server) {
	tenantv1.RegisterBillingServiceServer(server, s)
}

// GetBillingOverview 返回账务总览行（表格 9 列 + 抽屉内嵌子数据）。
// 行集合 = billing 足迹租户 ∪ 当期有用量租户；overdue 读取时动态计算（不落库）。
func (s *BillingService) GetBillingOverview(ctx context.Context, req *tenantv1.GetBillingOverviewRequest) (*tenantv1.GetBillingOverviewResponse, error) {
	const action = "billing.get_overview"

	// 步骤 1：解析 period（空 → 当月 UTC）/ status 过滤 / tenant 过滤
	period, err := resolveBillingPeriod(req.GetPeriod())
	if err != nil {
		return nil, err
	}
	rowFilter, err := ports.ParseBillingRowFilter(req.GetStatus())
	if err != nil {
		return nil, mapStoreError(err)
	}
	var tenantFilter *uuid.UUID
	if raw := strings.TrimSpace(req.GetTenantId()); raw != "" {
		id, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "tenant_id must be a uuid")
		}
		tenantFilter = &id
	}

	// 步骤 2：月窗口 + Core 平台用量（Core 不可用 → CORE_UNAVAILABLE）
	start, end, err := ports.BillingMonthWindow(period)
	if err != nil {
		return nil, mapStoreError(err)
	}
	usage, err := s.metering.GetPlatformUsage(ctx, start, end, tenantFilter)
	if err != nil {
		return nil, mapStoreError(err)
	}
	usageByTenant := groupBillingUsage(usage)

	// 步骤 3：行集合 = billing 足迹租户 ∪ 当期有用量租户
	tenantIDs, err := s.store.ListBillingTenantIDs(ctx, tenantFilter)
	if err != nil {
		return nil, mapStoreError(err)
	}
	tenantSet := make(map[uuid.UUID]bool, len(tenantIDs))
	for _, id := range tenantIDs {
		tenantSet[id] = true
	}
	for tid, records := range usageByTenant {
		if tenantFilter != nil && tid != *tenantFilter {
			continue
		}
		if len(records) > 0 {
			tenantSet[tid] = true
		}
	}

	// 步骤 4：租户名称映射（Core available-tenants；查询失败降级为 tenant_id 字符串，不阻塞总览）
	names := s.billingTenantNames(ctx)

	// 步骤 5：逐租户组装行（定价一次读取共享）
	pricing, err := s.store.ListPricing(ctx)
	if err != nil {
		return nil, mapStoreError(err)
	}
	now := time.Now()
	items := make([]*tenantv1.BillingOverviewItem, 0, len(tenantSet))
	for tid := range tenantSet {
		item, itemErr := s.buildOverviewItem(ctx, tid, period, now, names[tid], pricing, usageByTenant[tid])
		if itemErr != nil {
			return nil, mapStoreError(itemErr)
		}
		if rowFilter != "" && item.GetStatus() != string(rowFilter) {
			continue
		}
		items = append(items, item)
	}
	sortBillingItems(items)

	return &tenantv1.GetBillingOverviewResponse{Items: items, Total: int64(len(items))}, nil
}

// ExportBillingOverview 渲染账务对账 CSV（列 = 表格 9 列 + breakdown 扁平行）。
func (s *BillingService) ExportBillingOverview(ctx context.Context, req *tenantv1.ExportBillingOverviewRequest) (*tenantv1.ExportBillingOverviewResponse, error) {
	// 步骤 1：复用 overview 组装（同 query 口径）
	overview, err := s.GetBillingOverview(ctx, &tenantv1.GetBillingOverviewRequest{
		Period:   req.GetPeriod(),
		TenantId: req.GetTenantId(),
		Status:   req.GetStatus(),
	})
	if err != nil {
		return nil, err
	}

	// 步骤 2：展开为 CSV 行（每租户 breakdown 多行）
	rows := make([]billingCSVRow, 0, len(overview.GetItems()))
	for _, item := range overview.GetItems() {
		rows = append(rows, billingCSVRow{
			tenantID:   item.GetTenantId(),
			tenantName: item.GetTenantName(),
			period:     item.GetPeriod(),
			status:     item.GetStatus(),
			usageCost:  item.UsageCostUsd,
			credit:     item.CreditUsd,
			balance:    item.BalanceUsd,
			invoiceNo:  item.GetInvoiceNo(),
			dueDate:    item.GetDueDate(),
			breakdown:  item.GetUsageBreakdown(),
		})
	}

	// 步骤 3：渲染并按账期命名附件
	period, _ := resolveBillingPeriod(req.GetPeriod())
	return &tenantv1.ExportBillingOverviewResponse{
		Csv:      renderBillingCSV(rows),
		Filename: "billing-overview-" + period + ".csv",
	}, nil
}

// GenerateInvoice 生成账单（生成即出账 status=issued）：
// 同 idempotency_key 重放返回原账单；(tenant_id, period) 已有账单且 key 不同 → 409。
func (s *BillingService) GenerateInvoice(ctx context.Context, req *tenantv1.GenerateInvoiceRequest) (*tenantv1.BillingInvoice, error) {
	const action = "billing.generate_invoice"

	// 步骤 1：校验 tenant_id / period / idempotency_key
	tenantID, key, err := s.validateBillingWriteInput(ctx, req.GetTenantId(), req.GetPeriod(), req.GetIdempotencyKey(), action)
	if err != nil {
		return nil, err
	}
	period := strings.TrimSpace(req.GetPeriod())

	// 步骤 2：幂等/冲突预检（(tenant_id, period) 唯一：一期一单）
	existing, err := s.store.GetInvoiceByPeriod(ctx, tenantID, period)
	if err != nil && !errors.Is(err, ports.ErrBillingInvoiceNotFound) {
		return nil, mapStoreError(err)
	}
	if existing != nil {
		if existing.IdempotencyKey == key {
			return invoiceToProto(*existing), nil // 同 key 重放
		}
		return nil, businessError(codes.AlreadyExists, ports.ErrBillingInvoiceExists, "invoice already exists for tenant and period")
	}

	// 步骤 3：金额 = 当期用量成本（Core 聚合 × pricing）+ 当期调账合计
	amount, err := s.computeInvoiceAmount(ctx, tenantID, period)
	if err != nil {
		return nil, mapStoreError(err)
	}

	// 步骤 4：账单号 seq + 到期日（issued_at + 30 天）+ 操作流水（与插入同一事务）
	now := time.Now()
	seq, err := s.store.CountInvoicesByNoPrefix(ctx, billingInvoiceNoPrefix(period))
	if err != nil {
		return nil, mapStoreError(err)
	}
	no := nextBillingInvoiceNo(period, seq)
	in := ports.CreateBillingInvoiceInput{
		TenantID:       tenantID,
		Period:         period,
		No:             no,
		AmountUSD:      amount,
		DueDate:        billingDueDate(now),
		IssuedAt:       now,
		IdempotencyKey: key,
		Operation: &ports.BillingOperationLogInput{
			Action:   ports.BillingOpInvoiceGenerated,
			Message:  fmt.Sprintf("生成账单 %s $%.2f", no, amount),
			Operator: strings.TrimSpace(req.GetOperator()),
		},
	}
	inv, err := s.store.CreateInvoice(ctx, in)
	if err != nil {
		return s.handleCreateInvoiceConflict(ctx, in, key, err)
	}
	return invoiceToProto(*inv), nil
}

// InvoiceAction 账单状态动作（settle 结清 / credit 授信冲抵；终态重复动作 → 409）。
func (s *BillingService) InvoiceAction(ctx context.Context, req *tenantv1.InvoiceActionRequest) (*tenantv1.BillingInvoice, error) {
	const action = "billing.invoice_action"

	// 步骤 1：校验 invoice_id / action / idempotency_key
	invoiceID, err := parseBillingUUID(req.GetInvoiceId(), "invoice_id")
	if err != nil {
		return nil, err
	}
	billingAction, err := ports.ParseBillingInvoiceAction(req.GetAction())
	if err != nil {
		return nil, mapStoreError(err)
	}
	if _, err := parseBillingUUID(req.GetIdempotencyKey(), "idempotency_key"); err != nil {
		return nil, err
	}

	// 步骤 2：预读账单组装流水摘要（no/amount 为不变字段；不存在 → 404 提前返回）
	inv, err := s.store.GetInvoice(ctx, invoiceID)
	if err != nil {
		return nil, mapStoreError(err)
	}

	// 步骤 3：CAS 状态迁移（仅 issued 可迁移；已终态 → 409）；
	// CAS 失败由 store 事务回滚、不落流水（幂等冲突不产生操作历史）。
	opAction, verb := ports.BillingOpInvoiceSettled, "结清"
	if billingAction == ports.BillingActionCredit {
		opAction, verb = ports.BillingOpInvoiceCredited, "授信冲抵"
	}
	updated, err := s.store.UpdateInvoiceStatus(ctx, invoiceID, billingAction, time.Now(), &ports.BillingOperationLogInput{
		Action:   opAction,
		Message:  fmt.Sprintf("账单 %s %s $%.2f", inv.No, verb, inv.AmountUSD),
		Operator: strings.TrimSpace(req.GetOperator()),
	})
	if err != nil {
		return nil, mapStoreError(err)
	}
	return invoiceToProto(*updated), nil
}

// DeleteInvoice 软删除账单（仅 issued 可删；终态 409；已删除/不存在 404）。
// 删除与 invoice.deleted 操作流水同一事务落库；CAS 失败回滚则不落流水。
// 无幂等键（DELETE 非创建类写）：重复删除同一账单幂等返回 404。
func (s *BillingService) DeleteInvoice(ctx context.Context, req *tenantv1.DeleteInvoiceRequest) (*tenantv1.BillingInvoice, error) {
	// 步骤 1：校验 invoice_id
	invoiceID, err := parseBillingUUID(req.GetInvoiceId(), "invoice_id")
	if err != nil {
		return nil, err
	}

	// 步骤 2：预读账单组装流水摘要（no/amount 为不变字段；不存在 → 404 提前返回）
	inv, err := s.store.GetInvoice(ctx, invoiceID)
	if err != nil {
		return nil, mapStoreError(err)
	}

	// 步骤 3：CAS 软删（仅活跃 issued 行可删；终态 → 409；已删除 → 404）；
	// CAS 失败由 store 事务回滚、不落流水（404/409 不产生操作历史）。
	deleted, err := s.store.SoftDeleteInvoice(ctx, invoiceID, time.Now(), &ports.BillingOperationLogInput{
		Action:   ports.BillingOpInvoiceDeleted,
		Message:  fmt.Sprintf("删除账单 %s $%.2f（软删除，可重新出账）", inv.No, inv.AmountUSD),
		Operator: strings.TrimSpace(req.GetOperator()),
	})
	if err != nil {
		return nil, mapStoreError(err)
	}
	return invoiceToProto(*deleted), nil
}

// CreateAdjustment 写调账记录（金额可负、不可为 0）；同幂等键重放返回已有记录。
func (s *BillingService) CreateAdjustment(ctx context.Context, req *tenantv1.CreateAdjustmentRequest) (*tenantv1.BillingAdjustment, error) {
	const action = "billing.create_adjustment"

	// 步骤 1：校验 tenant_id / period / idempotency_key
	tenantID, key, err := s.validateBillingWriteInput(ctx, req.GetTenantId(), req.GetPeriod(), req.GetIdempotencyKey(), action)
	if err != nil {
		return nil, err
	}
	period := strings.TrimSpace(req.GetPeriod())

	// 步骤 2：金额与原因校验（可负、不可为 0；reason 必填 ≤512）
	amount := req.GetAmountUsd()
	if amount == 0 {
		return nil, businessError(codes.InvalidArgument, ports.ErrBillingAmountInvalid, "amount_usd must not be zero")
	}
	reason := strings.TrimSpace(req.GetReason())
	if reason == "" {
		return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "reason required")
	}
	if len(reason) > 512 {
		return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "reason must be at most 512 characters")
	}
	operator := strings.TrimSpace(req.GetOperator())

	// 步骤 3：幂等预检（同 key 重放返回已有记录）
	existing, err := s.store.GetAdjustmentByIdempotencyKey(ctx, key)
	if err != nil {
		return nil, mapStoreError(err)
	}
	if existing != nil {
		return adjustmentToProto(*existing), nil
	}

	// 步骤 4：写调账 + 操作流水（同一事务）；幂等键并发冲突 → 重查后按重放语义返回（不落流水）
	adj, err := s.store.CreateAdjustment(ctx, ports.CreateBillingAdjustmentInput{
		TenantID:       tenantID,
		Period:         period,
		AmountUSD:      amount,
		Reason:         reason,
		Operator:       operator,
		IdempotencyKey: key,
		Operation: &ports.BillingOperationLogInput{
			Action:   ports.BillingOpAdjustmentCreated,
			Message:  fmt.Sprintf("调账 %s（%s）", formatBillingSignedUSD(amount), reason),
			Operator: operator,
		},
	})
	if err != nil {
		if errors.Is(err, ports.ErrBillingIdempotencyConflict) {
			replayed, replayErr := s.store.GetAdjustmentByIdempotencyKey(ctx, key)
			if replayErr != nil || replayed == nil {
				return nil, mapStoreError(err)
			}
			return adjustmentToProto(*replayed), nil
		}
		return nil, mapStoreError(err)
	}
	return adjustmentToProto(*adj), nil
}

// ListBillingOperations 返回租户计费操作流水（抽屉「操作历史」Tab 数据源）。
// 流水与业务写同一事务落库；幂等重放与 409 冲突不产生流水。
// limit 默认 50、上限 200；offset 默认 0。
func (s *BillingService) ListBillingOperations(ctx context.Context, req *tenantv1.ListBillingOperationsRequest) (*tenantv1.ListBillingOperationsResponse, error) {
	// 步骤 1：校验 tenant_id（流水查询不做 Core 存在性校验，避免 Core 故障阻塞历史查看）
	tenantID, err := parseBillingUUID(req.GetTenantId(), "tenant_id")
	if err != nil {
		return nil, err
	}

	// 步骤 2：分页参数钳制
	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := int(req.GetOffset())
	if offset < 0 {
		offset = 0
	}

	// 步骤 3：查询并映射（created_at 倒序）
	logs, total, err := s.store.ListOperations(ctx, tenantID, limit, offset)
	if err != nil {
		return nil, mapStoreError(err)
	}
	items := make([]*tenantv1.BillingOperationLog, 0, len(logs))
	for _, l := range logs {
		items = append(items, billingOperationToProto(l))
	}
	return &tenantv1.ListBillingOperationsResponse{Items: items, Total: int64(total)}, nil
}

// ── 内部流程（勿穿插到上方 RPC 中间）──────────────────────────────────────

// resolveBillingPeriod 解析账期：空 → 当月（UTC）；非法 → BILLING_INVALID_PERIOD。
func resolveBillingPeriod(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Now().UTC().Format("2006-01"), nil
	}
	if err := ports.ValidateBillingPeriod(raw); err != nil {
		return "", mapStoreError(err)
	}
	return raw, nil
}

// parseBillingUUID 校验必填 UUID 字段。
func parseBillingUUID(raw, field string) (uuid.UUID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return uuid.Nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, field+" required")
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, field+" must be a uuid")
	}
	return id, nil
}

// validateBillingWriteInput 校验写接口公共入参并经 Core 校验租户存在（404）。
func (s *BillingService) validateBillingWriteInput(ctx context.Context, rawTenantID, period, rawKey, action string) (uuid.UUID, uuid.UUID, error) {
	// 步骤 1：tenant_id / idempotency_key 格式
	tenantID, err := parseBillingUUID(rawTenantID, "tenant_id")
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	key, err := parseBillingUUID(rawKey, "idempotency_key")
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}

	// 步骤 2：period 格式
	if err := ports.ValidateBillingPeriod(strings.TrimSpace(period)); err != nil {
		return uuid.Nil, uuid.Nil, mapStoreError(err)
	}

	// 步骤 3：经 Core 租户 API 校验存在性（不存在 → 404 BILLING_TENANT_NOT_FOUND）
	if _, err := s.tenants.GetTenant(ctx, tenantID); err != nil {
		if errors.Is(err, ports.ErrTenantNotFound) {
			return uuid.Nil, uuid.Nil, businessError(codes.NotFound, ports.ErrBillingTenantNotFound, "tenant not found")
		}
		return uuid.Nil, uuid.Nil, mapStoreError(err)
	}
	return tenantID, key, nil
}

// computeInvoiceAmount 汇总该租户该账期金额：用量成本（Core 聚合 × pricing）+ 当期调账合计。
// 无可用计量数据时用量成本按 0（调账仍计入）。
func (s *BillingService) computeInvoiceAmount(ctx context.Context, tenantID uuid.UUID, period string) (float64, error) {
	// 步骤 1：月窗口 + 单租户用量
	start, end, err := ports.BillingMonthWindow(period)
	if err != nil {
		return 0, err
	}
	filter := tenantID
	usage, err := s.metering.GetPlatformUsage(ctx, start, end, &filter)
	if err != nil {
		return 0, err
	}

	// 步骤 2：定价折算（metered cost 合计；无 metered 行 → 0）
	pricing, err := s.store.ListPricing(ctx)
	if err != nil {
		return 0, err
	}
	_, totalCost := buildUsageBreakdown(pricing, usage)

	// 步骤 3：当期调账合计（可负）
	adjustments, err := s.store.ListAdjustmentsByTenant(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	adjSum := 0.0
	for _, adj := range adjustments {
		if adj.Period == period {
			adjSum += adj.AmountUSD
		}
	}

	// 步骤 4：金额 = 用量成本 + 调账合计（各自 round2 后合计再 round2）
	usagePart := 0.0
	if totalCost != nil {
		usagePart = *totalCost
	}
	return round2(usagePart + adjSum), nil
}

// handleCreateInvoiceConflict 处理 CreateInvoice 唯一冲突：
// 重查 (tenant_id, period) → 有账单按 key 区分重放/409；无账单说明并发撞号（no 冲突）→ seq 递增重试一次。
func (s *BillingService) handleCreateInvoiceConflict(ctx context.Context, in ports.CreateBillingInvoiceInput, key uuid.UUID, err error) (*tenantv1.BillingInvoice, error) {
	// 步骤 1：非唯一冲突原样上抛
	if !errors.Is(err, ports.ErrBillingInvoiceExists) {
		return nil, mapStoreError(err)
	}

	// 步骤 2：重查该期账单 → 按 key 区分重放 / 409
	existing, getErr := s.store.GetInvoiceByPeriod(ctx, in.TenantID, in.Period)
	if getErr == nil && existing != nil {
		if existing.IdempotencyKey == key {
			return invoiceToProto(*existing), nil
		}
		return nil, businessError(codes.AlreadyExists, ports.ErrBillingInvoiceExists, "invoice already exists for tenant and period")
	}

	// 步骤 3：该期无账单说明并发撞号（no 唯一冲突）→ seq 递增重试一次
	seq, seqErr := s.store.CountInvoicesByNoPrefix(ctx, billingInvoiceNoPrefix(in.Period))
	if seqErr != nil {
		return nil, mapStoreError(seqErr)
	}
	in.No = nextBillingInvoiceNo(in.Period, seq+1)
	if in.Operation != nil {
		// 撞号重试换号后同步刷新流水摘要中的账单号
		in.Operation.Message = fmt.Sprintf("生成账单 %s $%.2f", in.No, in.AmountUSD)
	}
	retried, createErr := s.store.CreateInvoice(ctx, in)
	if createErr != nil {
		return nil, businessError(codes.AlreadyExists, ports.ErrBillingInvoiceExists, "invoice number conflict, please retry")
	}
	return invoiceToProto(*retried), nil
}

// billingTenantNames 拉取 Core 租户名称映射（失败降级空 map，名称回退 tenant_id）。
func (s *BillingService) billingTenantNames(ctx context.Context) map[uuid.UUID]string {
	names := make(map[uuid.UUID]string)
	bound, err := s.tenants.ListAvailableTenants(ctx)
	if err != nil {
		return names
	}
	for _, t := range bound {
		displayName := strings.TrimSpace(t.DisplayName)
		if displayName != "" {
			names[t.ID] = displayName
			continue
		}
		if name := strings.TrimSpace(t.Name); name != "" {
			names[t.ID] = name
		}
	}
	return names
}

// buildOverviewItem 组装单租户总览行（9 列 + 抽屉内嵌子数据）。
func (s *BillingService) buildOverviewItem(ctx context.Context, tenantID uuid.UUID, period string, now time.Time, tenantName string, pricing []ports.BillingPricing, usage []ports.BillingUsageRecord) (*tenantv1.BillingOverviewItem, error) {
	// 步骤 1：账单（当期 + 全部倒序）与调账（全部）
	currentInvoice, err := s.store.GetInvoiceByPeriod(ctx, tenantID, period)
	if err != nil && !errors.Is(err, ports.ErrBillingInvoiceNotFound) {
		return nil, err
	}
	invoices, err := s.store.ListInvoicesByTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	adjustments, err := s.store.ListAdjustmentsByTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	// 步骤 2：授信与余额推导（§6.3）
	credit, err := s.store.GetCredit(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	// 步骤 3：用量折算 breakdown（单价只读 pricing 表）
	breakdown, usageCost := buildUsageBreakdown(pricing, usage)

	// 步骤 4：行状态（overdue 动态计算，不落库）
	rowStatus := ports.DeriveBillingRowStatus(currentInvoice, now)

	// 步骤 5：当期调账过滤（行内只展示当期）
	periodAdjustments := make([]*tenantv1.BillingAdjustment, 0)
	for _, adj := range adjustments {
		if adj.Period == period {
			periodAdjustments = append(periodAdjustments, adjustmentToProto(adj))
		}
	}

	// 步骤 6：组装行
	item := &tenantv1.BillingOverviewItem{
		TenantId:       tenantID.String(),
		TenantName:     tenantName,
		Period:         period,
		Status:         string(rowStatus),
		UsageBreakdown: breakdown,
		Invoices:       make([]*tenantv1.BillingInvoice, 0, len(invoices)),
		Adjustments:    periodAdjustments,
		UpdatedAt:      timestampOf(now),
	}
	if tenantName == "" {
		item.TenantName = tenantID.String()
	}
	if usageCost != nil {
		item.UsageCostUsd = usageCost
	}
	if credit != nil {
		item.CreditUsd = credit
		item.BalanceUsd = deriveBalance(credit, invoices, adjustments)
	}
	if currentInvoice != nil {
		no := currentInvoice.No
		due := formatBillingDate(currentInvoice.DueDate)
		item.InvoiceNo = &no
		item.DueDate = &due
	}
	sortBillingInvoicesDesc(invoices)
	for _, inv := range invoices {
		item.Invoices = append(item.Invoices, invoiceToProto(inv))
	}
	return item, nil
}

// groupBillingUsage 用量按租户分组。
func groupBillingUsage(usage []ports.BillingUsageRecord) map[uuid.UUID][]ports.BillingUsageRecord {
	out := make(map[uuid.UUID][]ports.BillingUsageRecord)
	for _, u := range usage {
		out[u.TenantID] = append(out[u.TenantID], u)
	}
	return out
}

// sortBillingItems 总览行按租户 ID 排序（稳定输出）。
func sortBillingItems(items []*tenantv1.BillingOverviewItem) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].GetTenantId() < items[j-1].GetTenantId(); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}
