package ports

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// 计费结算域的端口（ports）定义。
// 本文件只声明接口、领域结构体与哨兵错误；实现由
// postgres adapter（PostgresBillingStore）与 core adapter（MeteringClient）承载。
//
// 分层红线：用量数据一律经 BillingMeteringClient（Core OpenAPI GET /metering/usage/platform）
// 获取，禁止直查 Core 库 metering_usage_records（CLAUDE.md §3）。

// 计费哨兵错误：service 层映射为「CODE: detail」gRPC status，网关还原 HTTP 状态。

var (
	// ErrBillingInvalidPeriod 表示 period 非 YYYY-MM 或月份非法（HTTP 400）。
	ErrBillingInvalidPeriod = errors.New("BILLING_INVALID_PERIOD")

	// ErrBillingTenantNotFound 表示 Core 侧租户不存在（HTTP 404）。
	ErrBillingTenantNotFound = errors.New("BILLING_TENANT_NOT_FOUND")

	// ErrBillingInvoiceNotFound 表示账单不存在（HTTP 404）。
	ErrBillingInvoiceNotFound = errors.New("BILLING_INVOICE_NOT_FOUND")

	// ErrBillingInvoiceExists 表示该租户该期已有账单且幂等键不同（HTTP 409）。
	ErrBillingInvoiceExists = errors.New("BILLING_INVOICE_EXISTS")

	// ErrBillingStateConflict 表示账单已 settled/credited 再操作（HTTP 409）。
	ErrBillingStateConflict = errors.New("BILLING_STATE_CONFLICT")

	// ErrBillingActionInvalid 表示 action 非 settle/credit（HTTP 400）。
	ErrBillingActionInvalid = errors.New("BILLING_ACTION_INVALID")

	// ErrBillingAmountInvalid 表示调账金额为 0（HTTP 400）。
	ErrBillingAmountInvalid = errors.New("BILLING_AMOUNT_INVALID")

	// ErrBillingIdempotencyConflict 表示幂等键并发插入冲突（service 重查后按重放语义处理）。
	ErrBillingIdempotencyConflict = errors.New("BILLING_IDEMPOTENCY_CONFLICT")
)

// billingPeriodPattern 账期格式：YYYY-MM（月份合法性另行校验）。
var billingPeriodPattern = regexp.MustCompile(`^\d{4}-\d{2}$`)

// ValidateBillingPeriod 校验账期：YYYY-MM 且月份 01-12。
func ValidateBillingPeriod(period string) error {
	if !billingPeriodPattern.MatchString(period) {
		return fmt.Errorf("%w: period must match YYYY-MM", ErrBillingInvalidPeriod)
	}
	var month int
	if _, err := fmt.Sscanf(period[5:7], "%02d", &month); err != nil || month < 1 || month > 12 {
		return fmt.Errorf("%w: month must be 01-12", ErrBillingInvalidPeriod)
	}
	return nil
}

// BillingMonthWindow 返回账期对应的 Core 计量查询窗口（月初 00:00:00Z ~ 月末 23:59:59Z，UTC）。
func BillingMonthWindow(period string) (start, end time.Time, err error) {
	if err := ValidateBillingPeriod(period); err != nil {
		return time.Time{}, time.Time{}, err
	}
	start, err = time.Parse("2006-01", period)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: %v", ErrBillingInvalidPeriod, err)
	}
	end = start.AddDate(0, 1, 0).Add(-time.Second)
	return start.UTC(), end.UTC(), nil
}

// BillingInvoiceStatus 账单落库状态机：issued（生成即出账）→ settled | credited（终态）。
// overdue 仅为读取时展示态（issued 且 now > due_date），不落库。
type BillingInvoiceStatus string

const (
	BillingInvoiceIssued   BillingInvoiceStatus = "issued"
	BillingInvoiceSettled  BillingInvoiceStatus = "settled"
	BillingInvoiceCredited BillingInvoiceStatus = "credited"
)

// BillingRowStatus 行展示状态（overview 返回）：
// issued→current、issued 且 now>due_date→overdue（动态计算）、settled、credited；无账单→current。
type BillingRowStatus string

const (
	BillingRowCurrent  BillingRowStatus = "current"
	BillingRowOverdue  BillingRowStatus = "overdue"
	BillingRowSettled  BillingRowStatus = "settled"
	BillingRowCredited BillingRowStatus = "credited"
)

// ParseBillingRowFilter 解析行状态过滤：空=全部；非法值报 VALIDATION_FAILED。
func ParseBillingRowFilter(raw string) (BillingRowStatus, error) {
	switch BillingRowStatus(raw) {
	case "":
		return "", nil
	case BillingRowCurrent, BillingRowOverdue, BillingRowSettled, BillingRowCredited:
		return BillingRowStatus(raw), nil
	default:
		return "", fmt.Errorf("%w: status must be current, overdue, settled, or credited", ErrValidationFailed)
	}
}

// DeriveBillingRowStatus 行状态推导：有账单按落库状态 + overdue 动态计算；无账单为 current。
func DeriveBillingRowStatus(inv *BillingInvoice, now time.Time) BillingRowStatus {
	if inv == nil {
		return BillingRowCurrent
	}
	switch inv.Status {
	case BillingInvoiceSettled:
		return BillingRowSettled
	case BillingInvoiceCredited:
		return BillingRowCredited
	default: // issued
		if now.After(inv.DueDate.Add(24 * time.Hour).Add(-time.Second)) {
			// due_date 为日期（当日 23:59:59.999... 前不算逾期）；比较按日期粒度
			return BillingRowOverdue
		}
		return BillingRowCurrent
	}
}

// BillingOperationAction 操作流水动作（billing_operation_logs.action 枚举，
// 迁移 20260908_001_billing_operation_logs.sql CHECK 约束）。
const (
	BillingOpInvoiceGenerated  = "invoice.generated"
	BillingOpInvoiceSettled    = "invoice.settled"
	BillingOpInvoiceCredited   = "invoice.credited"
	BillingOpAdjustmentCreated = "adjustment.created"
)

// BillingOperationLogInput 是与业务写同一事务落库的流水内容。
// tenant_id / period / ref_id / created_at 由 store 从业务行回填（以落库事实为准）；
// 业务写失败回滚则流水一并回滚；幂等重放与 409 冲突路径不传入（不落流水）。
type BillingOperationLogInput struct {
	Action   string
	Message  string
	Operator string
}

// BillingOperationLog 表示一条操作流水（对应 billing_operation_logs 表一行）。
type BillingOperationLog struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Period    *string
	Action    string
	RefID     *uuid.UUID
	Message   string
	Operator  string
	CreatedAt time.Time
}

// BillingInvoice 表示一条账单（对应 billing_invoices 表一行）。
type BillingInvoice struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	Period         string
	No             string
	AmountUSD      float64
	Status         BillingInvoiceStatus
	DueDate        time.Time // date 粒度（UTC 零点）
	IssuedAt       time.Time
	SettledAt      *time.Time
	CreditedAt     *time.Time
	IdempotencyKey uuid.UUID
	CreatedAt      time.Time
}

// BillingAdjustment 表示一条调账记录（对应 billing_adjustments 表一行；金额可负）。
type BillingAdjustment struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	Period         string
	AmountUSD      float64
	Reason         string
	Operator       string
	IdempotencyKey uuid.UUID
	CreatedAt      time.Time
}

// BillingPricing 表示一行定价（对应 billing_pricing 表；代码零单价字面量，只读表）。
type BillingPricing struct {
	ResourceType  string  // Core metering resource_type（如 instance_gpu_seconds）
	DisplayMetric string  // 展示指标（如 gpu_hours）
	UnitCost      float64 // 每展示单位价格（USD）
}

// BillingUsageRecord 表示一条跨租户聚合用量（来自 Core 平台聚合接口）。
type BillingUsageRecord struct {
	TenantID      uuid.UUID
	ResourceType  string
	TotalQuantity float64
}

// CreateBillingInvoiceInput 是生成账单的入参。
type CreateBillingInvoiceInput struct {
	TenantID       uuid.UUID
	Period         string
	No             string
	AmountUSD      float64
	DueDate        time.Time
	IssuedAt       time.Time
	IdempotencyKey uuid.UUID
	// Operation 非 nil 时与账单插入同一事务落一条操作流水。
	Operation *BillingOperationLogInput
}

// CreateBillingAdjustmentInput 是写调账的入参。
type CreateBillingAdjustmentInput struct {
	TenantID       uuid.UUID
	Period         string
	AmountUSD      float64
	Reason         string
	Operator       string
	IdempotencyKey uuid.UUID
	// Operation 非 nil 时与调账插入同一事务落一条操作流水。
	Operation *BillingOperationLogInput
}

// BillingInvoiceAction 表示账单状态动作。
type BillingInvoiceAction string

const (
	BillingActionSettle BillingInvoiceAction = "settle"
	BillingActionCredit BillingInvoiceAction = "credit"
)

// ParseBillingInvoiceAction 解析 action：非法值报 ErrBillingActionInvalid。
func ParseBillingInvoiceAction(raw string) (BillingInvoiceAction, error) {
	switch BillingInvoiceAction(raw) {
	case BillingActionSettle, BillingActionCredit:
		return BillingInvoiceAction(raw), nil
	default:
		return "", fmt.Errorf("%w: action must be settle or credit", ErrBillingActionInvalid)
	}
}

// BillingStore 定义计费结算域的数据访问接口（tenant-service 自有库 billing_* 表）。
// 实现：services/tenant-service/internal/repo/adapters/postgres（PostgresBillingStore）。
type BillingStore interface {
	// ListInvoicesByTenant 返回该租户全部账单（issued_at 倒序）。
	ListInvoicesByTenant(ctx context.Context, tenantID uuid.UUID) ([]BillingInvoice, error)

	// GetInvoiceByPeriod 返回该租户该账期的账单；无账单返回 nil（不视为错误）。
	GetInvoiceByPeriod(ctx context.Context, tenantID uuid.UUID, period string) (*BillingInvoice, error)

	// GetInvoice 按主键查账单；不存在返回 ErrBillingInvoiceNotFound。
	GetInvoice(ctx context.Context, id uuid.UUID) (*BillingInvoice, error)

	// CountInvoicesByNoPrefix 统计账单号前缀匹配数（账单号 seq 生成用）。
	CountInvoicesByNoPrefix(ctx context.Context, prefix string) (int, error)

	// CreateInvoice 插入账单（status=issued）；(tenant_id, period) 或 no 冲突 → ErrBillingInvoiceExists。
	CreateInvoice(ctx context.Context, in CreateBillingInvoiceInput) (*BillingInvoice, error)

	// UpdateInvoiceStatus 以 status='issued' 为 CAS 前提应用状态迁移；不满足 → ErrBillingStateConflict。
	// op 非 nil 时与状态迁移同一事务落一条操作流水；CAS 失败（409/404）则事务回滚、不落流水。
	UpdateInvoiceStatus(ctx context.Context, id uuid.UUID, action BillingInvoiceAction, at time.Time, op *BillingOperationLogInput) (*BillingInvoice, error)

	// ListAdjustmentsByTenant 返回该租户全部调账（created_at 升序）。
	ListAdjustmentsByTenant(ctx context.Context, tenantID uuid.UUID) ([]BillingAdjustment, error)

	// GetAdjustmentByIdempotencyKey 按幂等键查调账；无则返回 nil。
	GetAdjustmentByIdempotencyKey(ctx context.Context, key uuid.UUID) (*BillingAdjustment, error)

	// CreateAdjustment 插入调账记录；幂等键冲突视为并发重放（由 service 先查后插兜底）。
	CreateAdjustment(ctx context.Context, in CreateBillingAdjustmentInput) (*BillingAdjustment, error)

	// GetCredit 返回该租户授信额度；无账户行返回 nil。
	GetCredit(ctx context.Context, tenantID uuid.UUID) (*float64, error)

	// ListPricing 返回全部定价行（代码零单价字面量）。
	ListPricing(ctx context.Context) ([]BillingPricing, error)

	// ListBillingTenantIDs 返回计费域有足迹（账单/调账/授信）的租户集合；
	// tenantFilter 非 nil 时仅返回该租户（存在足迹才返回，无足迹返回空）。
	ListBillingTenantIDs(ctx context.Context, tenantFilter *uuid.UUID) ([]uuid.UUID, error)

	// ListOperations 返回该租户操作流水（created_at 倒序、分页）；
	// total 为过滤后总条数（分页用）。无流水返回空切片（不视为错误）。
	ListOperations(ctx context.Context, tenantID uuid.UUID, limit, offset int) ([]BillingOperationLog, int, error)
}

// BillingMeteringClient 定义经 Core OpenAPI 获取平台跨租户用量的端口。
// 实现：services/tenant-service/internal/repo/adapters/core（MeteringClient）。
type BillingMeteringClient interface {
	// GetPlatformUsage 调用 Core GET /metering/usage/platform（月窗口 + group_by=tenant_id）；
	// tenantFilter 非 nil 时仅查询该租户（单租户钻取）。
	GetPlatformUsage(ctx context.Context, start, end time.Time, tenantFilter *uuid.UUID) ([]BillingUsageRecord, error)
}
