package service

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// 计费结算域折算与余额推导（方案 §6.3 / §6.4）。
// 单价零代码字面量：所有 unit_cost 只来自 billing_pricing 表行（ports.BillingPricing）。

// billingDisplayMetrics 是 breakdown 固定输出顺序（方案 §4.1：6 行）。
var billingDisplayMetrics = []string{
	"gpu_hours", "cpu_hours", "memory_gb_hours", "tokens", "storage_gi", "kb_queries",
}

// billingResourceMetrics 是 Core metering resource_type → 展示指标映射（§6.4 折算表）。
var billingResourceMetrics = map[string]string{
	"instance_gpu_seconds":        "gpu_hours",
	"instance_cpu_seconds":        "cpu_hours",
	"instance_memory_gib_seconds": "memory_gb_hours",
	"token_total":                 "tokens",
}

// billingHourMetrics 是按 /3600 换算成小时的展示指标（其余直乘）。
var billingHourMetrics = map[string]bool{
	"gpu_hours": true, "cpu_hours": true, "memory_gb_hours": true,
}

// billingAlwaysUnavailable 是采集源缺失、永远 unavailable 的展示指标
//（storage/kb：Core resource_type 枚举与采集链路均未落地，§4.1/§8）。
var billingAlwaysUnavailable = map[string]bool{
	"storage_gi": true, "kb_queries": true,
}

// buildUsageBreakdown 将 Core 平台用量与定价表折算为 breakdown 6 行。
// 返回值 totalCost 为 metered 行 cost 合计（无任何 metered 行时为 nil，不伪造 0）。
// 口径：
//   - 该 resource_type 在 Core 返回中存在 → metered（量可为 0）；
//     无记录 → unavailable（含 Token 落库链路未落地期，§6.4 过渡态）；
//   - storage_gi/kb_queries 永远 unavailable（amount/cost 为 null，不伪造 0）；
//   - 未定价资源不折算（出现在用量但无定价行时跳过，计费以定价表驱动）。
func buildUsageBreakdown(pricing []ports.BillingPricing, usage []ports.BillingUsageRecord) (breakdown []*tenantv1.BillingUsageBreakdown, totalCost *float64) {
	// 步骤 1：定价按展示指标索引；用量按展示指标累加（同 metric 多行求和）
	pricingByMetric := make(map[string]ports.BillingPricing, len(pricing))
	for _, p := range pricing {
		pricingByMetric[p.DisplayMetric] = p
	}
	usageByMetric := make(map[string]float64)
	for _, u := range usage {
		metric, ok := billingResourceMetrics[u.ResourceType]
		if !ok {
			continue
		}
		usageByMetric[metric] += u.TotalQuantity
	}

	// 步骤 2：按固定顺序组装 6 行
	total := 0.0
	anyMetered := false
	for _, metric := range billingDisplayMetrics {
		row := &tenantv1.BillingUsageBreakdown{Metric: metric}
		if p, ok := pricingByMetric[metric]; ok {
			cost := p.UnitCost
			row.UnitCost = &cost
		}

		// 步骤 2a：采集源缺失指标永远 unavailable
		if billingAlwaysUnavailable[metric] {
			row.DataSource = billingDataSourceUnavailable
			breakdown = append(breakdown, row)
			continue
		}

		// 步骤 2b：无用量记录 → unavailable（amount/cost 为 null，不伪造 0）
		quantity, hasUsage := usageByMetric[metric]
		if !hasUsage {
			row.DataSource = billingDataSourceUnavailable
			breakdown = append(breakdown, row)
			continue
		}

		// 步骤 2c：有用量记录 → metered；换算展示单位并折算成本
		if p, ok := pricingByMetric[metric]; !ok {
			// 未定价资源不折算：展示量但无成本（计费以定价表驱动）
			row.DataSource = billingDataSourceUnavailable
			breakdown = append(breakdown, row)
			continue
		} else {
			amount := quantity
			if billingHourMetrics[metric] {
				amount = quantity / 3600
			}
			row.Amount = &amount
			row.UnitCost = &p.UnitCost
			cost := round2(amount * p.UnitCost)
			row.Cost = &cost
			total += cost
			anyMetered = true
		}
		row.DataSource = billingDataSourceMetered
		breakdown = append(breakdown, row)
	}

	// 步骤 3：合计（无 metered 行 → null）
	if anyMetered {
		sum := round2(total)
		totalCost = &sum
	}
	return breakdown, totalCost
}

// deriveBalance 按方案 §6.3 读取时推导余额：
//
//	balance = credit − Σ(未清账单 amount) + Σ(该期无账单的调账 amount)
//
// credit 为 nil（无授信账户）时返回 nil；调账需传该租户全部调账（按每条调账的
// period 判断该期是否已出账单）。
func deriveBalance(credit *float64, invoices []ports.BillingInvoice, adjustments []ports.BillingAdjustment) *float64 {
	if credit == nil {
		return nil
	}

	// 步骤 1：未清账单占用（issued 扣减；settled/credited 不再占用）
	balance := *credit
	issuedPeriods := make(map[string]bool)
	for _, inv := range invoices {
		if inv.Status == ports.BillingInvoiceIssued {
			balance -= inv.AmountUSD
			issuedPeriods[inv.Period] = true
		}
	}

	// 步骤 2：未出账调账先行生效（该 period 已出账单的调账不重复扣减）
	for _, adj := range adjustments {
		if !issuedPeriods[adj.Period] {
			balance += adj.AmountUSD
		}
	}
	b := round2(balance)
	return &b
}

// billingInvoiceNoPrefix 返回账期对应的账单号前缀（INV-{YYMM}-，如 INV-2609-）。
func billingInvoiceNoPrefix(period string) string {
	if len(period) != 7 { // YYYY-MM
		return "INV-"
	}
	return fmt.Sprintf("INV-%s%s-", period[2:4], period[5:7])
}

// nextBillingInvoiceNo 生成下一个账单号：INV-{YYMM}-{seq:02d}。
func nextBillingInvoiceNo(period string, count int) string {
	return fmt.Sprintf("%s%02d", billingInvoiceNoPrefix(period), count+1)
}

// billingDueDate 计算到期日：issued_at + 30 天（方案 §4.3 策略常量），date 粒度（UTC 零点）。
func billingDueDate(issuedAt time.Time) time.Time {
	return issuedAt.AddDate(0, 0, 30).UTC().Truncate(24 * time.Hour)
}

// formatBillingTimestamp 展示时间：YYYY-MM-DD HH:mm:ss（Asia/Shanghai；契约非 RFC3339）。
func formatBillingTimestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(billingDisplayLocation).Format("2006-01-02 15:04:05")
}

// billingDisplayLocation 是契约展示时区（Asia/Shanghai）。
var billingDisplayLocation = time.FixedZone("Asia/Shanghai", 8*3600)

// formatBillingDate 展示日期：YYYY-MM-DD。
func formatBillingDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

// billingDataSource 是 usage_breakdown.data_source 取值。
const (
	billingDataSourceMetered     = "metered"
	billingDataSourceUnavailable = "unavailable"
)

// timestampOf time.Time → protobuf Timestamp（nil 安全：零值返回 nil）。
func timestampOf(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t.UTC())
}

// round2 金额四舍五入到 2 位小数。
func round2(f float64) float64 {
	return math.Round(f*100) / 100
}

// invoiceToProto 账单实体 → BillingInvoice proto。
func invoiceToProto(inv ports.BillingInvoice) *tenantv1.BillingInvoice {
	out := &tenantv1.BillingInvoice{
		Id:       inv.ID.String(),
		No:       inv.No,
		Period:   inv.Period,
		AmountUsd: inv.AmountUSD,
		Status:   string(inv.Status),
		DueDate:  formatBillingDate(inv.DueDate),
		IssuedAt: timestampOf(inv.IssuedAt),
	}
	if inv.SettledAt != nil {
		out.SettledAt = timestampOf(*inv.SettledAt)
	}
	if inv.CreditedAt != nil {
		out.CreditedAt = timestampOf(*inv.CreditedAt)
	}
	return out
}

// adjustmentToProto 调账实体 → BillingAdjustment proto。
func adjustmentToProto(adj ports.BillingAdjustment) *tenantv1.BillingAdjustment {
	return &tenantv1.BillingAdjustment{
		Id:        adj.ID.String(),
		TenantId:  adj.TenantID.String(),
		Period:    adj.Period,
		AmountUsd: adj.AmountUSD,
		Reason:    adj.Reason,
		Operator:  adj.Operator,
		CreatedAt: timestampOf(adj.CreatedAt),
	}
}

// renderBillingCSV 渲染对账 CSV（§10.7：全扁平 + 每租户 breakdown 明细行）。
// 列 = 表格 9 列 + 用量明细 5 列（metric/usage_amount/unit_cost/cost/data_source）。
func renderBillingCSV(rows []billingCSVRow) string {
	var sb strings.Builder
	sb.WriteString("tenant_id,tenant_name,period,status,usage_cost_usd,credit_usd,balance_usd,invoice_no,due_date,metric,usage_amount,unit_cost,cost,data_source\r\n")
	for _, row := range rows {
		for _, bd := range row.breakdown {
			sb.WriteString(csvField(row.tenantID))
			sb.WriteByte(',')
			sb.WriteString(csvField(row.tenantName))
			sb.WriteByte(',')
			sb.WriteString(csvField(row.period))
			sb.WriteByte(',')
			sb.WriteString(csvField(row.status))
			sb.WriteByte(',')
			sb.WriteString(csvField(floatCSV(row.usageCost)))
			sb.WriteByte(',')
			sb.WriteString(csvField(floatCSV(row.credit)))
			sb.WriteByte(',')
			sb.WriteString(csvField(floatCSV(row.balance)))
			sb.WriteByte(',')
			sb.WriteString(csvField(row.invoiceNo))
			sb.WriteByte(',')
			sb.WriteString(csvField(row.dueDate))
			sb.WriteByte(',')
			sb.WriteString(csvField(bd.GetMetric()))
			sb.WriteByte(',')
			sb.WriteString(csvField(floatCSV(bd.Amount)))
			sb.WriteByte(',')
			sb.WriteString(csvField(floatCSV(bd.UnitCost)))
			sb.WriteByte(',')
			sb.WriteString(csvField(floatCSV(bd.Cost)))
			sb.WriteByte(',')
			sb.WriteString(csvField(bd.GetDataSource()))
			sb.WriteString("\r\n")
		}
	}
	return sb.String()
}

// billingCSVRow 是 CSV 渲染的一租户输入（每租户展开 breakdown 多行）。
type billingCSVRow struct {
	tenantID   string
	tenantName string
	period     string
	status     string
	usageCost  *float64
	credit     *float64
	balance    *float64
	invoiceNo  string
	dueDate    string
	breakdown  []*tenantv1.BillingUsageBreakdown
}

// floatCSV optional float → CSV 单元格（nil → 空）。
func floatCSV(v *float64) string {
	if v == nil {
		return ""
	}
	return trimFloat(*v)
}

// trimFloat 输出最短浮点表示（避免 1800 打成 1800.000000）。
func trimFloat(f float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.8f", f), "0"), ".")
}

// csvField CSV 字段转义：含逗号/引号/换行时双引号包裹。
func csvField(s string) string {
	if strings.ContainsAny(s, ",\"\r\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// sortBillingInvoicesDesc 账单按 issued_at 倒序（store 已排序，防御性再排）。
func sortBillingInvoicesDesc(invoices []ports.BillingInvoice) {
	sort.SliceStable(invoices, func(i, j int) bool {
		return invoices[i].IssuedAt.After(invoices[j].IssuedAt)
	})
}
