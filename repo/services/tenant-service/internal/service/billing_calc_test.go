package service

import (
	"strings"
	"testing"
	"time"

	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

// 计费折算纯函数单测：换算系数（秒→小时）、单价折算、unavailable 过渡态、
// 余额推导（§6.3）、账单号/到期日/时区展示/CSV 转义。

func TestBuildUsageBreakdownMeteredFull(t *testing.T) {
	pricing := []ports.BillingPricing{
		{ResourceType: "instance_gpu_seconds", DisplayMetric: "gpu_hours", UnitCost: 1.2},
		{ResourceType: "instance_cpu_seconds", DisplayMetric: "cpu_hours", UnitCost: 0.05},
		{ResourceType: "instance_memory_gib_seconds", DisplayMetric: "memory_gb_hours", UnitCost: 0.02},
		{ResourceType: "token_total", DisplayMetric: "tokens", UnitCost: 0.000002},
		{ResourceType: "storage", DisplayMetric: "storage_gi", UnitCost: 0.02},
		{ResourceType: "kb", DisplayMetric: "kb_queries", UnitCost: 0.001},
	}
	usage := []ports.BillingUsageRecord{
		{ResourceType: "instance_gpu_seconds", TotalQuantity: 7200},        // 2 gpu_hours
		{ResourceType: "instance_cpu_seconds", TotalQuantity: 3600},        // 1 cpu_hour
		{ResourceType: "instance_memory_gib_seconds", TotalQuantity: 7200}, // 2 memory_gb_hours
		{ResourceType: "token_total", TotalQuantity: 1000000},              // 直接计
		{ResourceType: "unknown_type", TotalQuantity: 999},                 // 未映射指标 → 忽略
	}

	breakdown, total := buildUsageBreakdown(pricing, usage)
	if len(breakdown) != 6 {
		t.Fatalf("breakdown rows = %d, want 6（固定顺序）", len(breakdown))
	}
	wantOrder := []string{"gpu_hours", "cpu_hours", "memory_gb_hours", "tokens", "storage_gi", "kb_queries"}
	for i, metric := range wantOrder {
		if breakdown[i].GetMetric() != metric {
			t.Fatalf("row[%d] = %q, want %q", i, breakdown[i].GetMetric(), metric)
		}
	}
	// 换算系数：秒 → 小时（/3600）
	if got := breakdown[0].GetAmount(); got != 2 {
		t.Fatalf("gpu_hours amount = %v, want 2", got)
	}
	if got := breakdown[0].GetCost(); got != 2.4 {
		t.Fatalf("gpu_hours cost = %v, want 2.4", got)
	}
	if got := breakdown[1].GetAmount(); got != 1 {
		t.Fatalf("cpu_hours amount = %v, want 1", got)
	}
	// tokens 不换算：1e6 × 0.000002 = 2
	if got := breakdown[3].GetAmount(); got != 1000000 {
		t.Fatalf("tokens amount = %v, want 1000000", got)
	}
	// storage/kb 永远 unavailable（采集链路未落地，不伪造 0）
	for _, i := range []int{4, 5} {
		if breakdown[i].GetDataSource() != "unavailable" || breakdown[i].Amount != nil || breakdown[i].Cost != nil {
			t.Fatalf("row[%d] = %+v, want unavailable + null", i, breakdown[i])
		}
	}
	// 合计 = 2.4 + 0.05 + 0.04 + 2 = 4.49
	if total == nil || *total != 4.49 {
		t.Fatalf("total = %v, want 4.49", total)
	}
}

func TestBuildUsageBreakdownNoUsage(t *testing.T) {
	breakdown, total := buildUsageBreakdown([]ports.BillingPricing{
		{ResourceType: "instance_gpu_seconds", DisplayMetric: "gpu_hours", UnitCost: 1.2},
	}, nil)
	if total != nil {
		t.Fatalf("total = %v, want nil（无 metered 行不伪造 0）", total)
	}
	for _, row := range breakdown {
		if row.GetDataSource() != "unavailable" || row.Amount != nil || row.Cost != nil {
			t.Fatalf("row %s = %+v, want unavailable + null", row.GetMetric(), row)
		}
	}
}

func TestBuildUsageBreakdownUnpricedUsage(t *testing.T) {
	// 用量存在但定价表无该行 → 不折算（unavailable，计费以定价表驱动）
	breakdown, total := buildUsageBreakdown(nil, []ports.BillingUsageRecord{
		{ResourceType: "instance_gpu_seconds", TotalQuantity: 7200},
	})
	if total != nil {
		t.Fatalf("total = %v, want nil", total)
	}
	if got := breakdown[0]; got.GetDataSource() != "unavailable" || got.Amount != nil {
		t.Fatalf("gpu_hours row = %+v, want unavailable（未定价）", got)
	}
}

func TestDeriveBillingRowStatus(t *testing.T) {
	due := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	issued := ports.BillingInvoice{Status: ports.BillingInvoiceIssued, DueDate: due}

	// 到期当日（23:59:59 前）不算逾期
	if got := ports.DeriveBillingRowStatus(&issued, due.Add(23*time.Hour)); got != ports.BillingRowCurrent {
		t.Fatalf("due-day status = %q, want current", got)
	}
	// 次日起 overdue
	if got := ports.DeriveBillingRowStatus(&issued, due.Add(24*time.Hour)); got != ports.BillingRowOverdue {
		t.Fatalf("next-day status = %q, want overdue", got)
	}
	// 终态直接映射；无账单 → current
	settled := issued
	settled.Status = ports.BillingInvoiceSettled
	if got := ports.DeriveBillingRowStatus(&settled, due.Add(72*time.Hour)); got != ports.BillingRowSettled {
		t.Fatalf("settled status = %q", got)
	}
	credited := issued
	credited.Status = ports.BillingInvoiceCredited
	if got := ports.DeriveBillingRowStatus(&credited, due.Add(72*time.Hour)); got != ports.BillingRowCredited {
		t.Fatalf("credited status = %q", got)
	}
	if got := ports.DeriveBillingRowStatus(nil, time.Now()); got != ports.BillingRowCurrent {
		t.Fatalf("nil invoice status = %q, want current", got)
	}
}

func TestDeriveBalance(t *testing.T) {
	// credit 为 nil（无授信账户）→ nil
	if got := deriveBalance(nil, nil, nil); got != nil {
		t.Fatalf("balance = %v, want nil", got)
	}
	credit := 100.0
	if got := deriveBalance(&credit, nil, nil); got == nil || *got != 100 {
		t.Fatalf("balance = %v, want 100", got)
	}

	// issued 账单占用；settled 不占用
	invoices := []ports.BillingInvoice{
		{Period: "2026-09", AmountUSD: 30, Status: ports.BillingInvoiceIssued},
		{Period: "2026-08", AmountUSD: 20, Status: ports.BillingInvoiceSettled},
	}
	if got := deriveBalance(&credit, invoices, nil); got == nil || *got != 70 {
		t.Fatalf("balance = %v, want 70", got)
	}

	// 未出账调账生效；已出账（issued）期间的调账不重复扣
	adjustments := []ports.BillingAdjustment{
		{Period: "2026-09", AmountUSD: -5},  // 该期已有 issued 账单 → 不扣
		{Period: "2026-07", AmountUSD: -5},  // 无账单 → 生效
	}
	if got := deriveBalance(&credit, invoices, adjustments); got == nil || *got != 65 {
		t.Fatalf("balance = %v, want 65", got)
	}
}

func TestBillingInvoiceNoAndDueDate(t *testing.T) {
	if got := billingInvoiceNoPrefix("2026-09"); got != "INV-2609-" {
		t.Fatalf("prefix = %q, want INV-2609-", got)
	}
	if got := billingInvoiceNoPrefix("bad"); got != "INV-" {
		t.Fatalf("bad prefix = %q, want INV-", got)
	}
	if got := nextBillingInvoiceNo("2026-09", 0); got != "INV-2609-01" {
		t.Fatalf("no = %q, want INV-2609-01", got)
	}
	if got := nextBillingInvoiceNo("2026-09", 9); got != "INV-2609-10" {
		t.Fatalf("no = %q, want INV-2609-10", got)
	}

	// 到期日 = issued_at + 30 天（date 粒度 UTC）
	issued := time.Date(2026, 9, 1, 10, 30, 0, 0, time.UTC)
	want := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if got := billingDueDate(issued); !got.Equal(want) {
		t.Fatalf("due = %v, want %v", got, want)
	}

	// 日期/时间展示格式
	if got := formatBillingDate(issued); got != "2026-09-01" {
		t.Fatalf("date = %q", got)
	}
	if got := formatBillingDate(time.Time{}); got != "" {
		t.Fatalf("zero date = %q, want empty", got)
	}
	// Asia/Shanghai：UTC 00:30 → 08:30
	cstInput := time.Date(2026, 9, 1, 0, 30, 0, 0, time.UTC)
	if got := formatBillingTimestamp(cstInput); got != "2026-09-01 08:30:00" {
		t.Fatalf("timestamp = %q, want 2026-09-01 08:30:00", got)
	}

	if got := round2(1.005); got != 1.0 { // 四舍五入到 2 位（float 1.005 实际为 1.00499…）
		t.Fatalf("round2(1.005) = %v, want 1.0", got)
	}
	if got := round2(2.4 + 0.001); got != 2.4 {
		t.Fatalf("round2(2.401) = %v, want 2.4", got)
	}
}

func TestBillingCSVRendering(t *testing.T) {
	// trimFloat 最短浮点表示
	if got := trimFloat(1800); got != "1800" {
		t.Fatalf("trimFloat(1800) = %q", got)
	}
	if got := trimFloat(2.4); got != "2.4" {
		t.Fatalf("trimFloat(2.4) = %q", got)
	}
	// CSV 转义：逗号/引号
	if got := csvField("a,b"); got != `"a,b"` {
		t.Fatalf("csvField comma = %q", got)
	}
	if got := csvField(`say "hi"`); got != `"say ""hi"""` {
		t.Fatalf("csvField quote = %q", got)
	}
	if got := csvField("plain"); got != "plain" {
		t.Fatalf("csvField plain = %q", got)
	}
	// floatCSV：nil → 空
	if got := floatCSV(nil); got != "" {
		t.Fatalf("floatCSV(nil) = %q", got)
	}

	// 渲染：表头 + 每租户 breakdown 行
	cost := 2.4
	rows := []billingCSVRow{{
		tenantID:   "tid",
		tenantName: "acme",
		period:     "2026-09",
		status:     "current",
		usageCost:  &cost,
		breakdown: []*tenantv1.BillingUsageBreakdown{
			{Metric: "gpu_hours", Amount: floatPtr(2), UnitCost: floatPtr(1.2), Cost: &cost, DataSource: "metered"},
			{Metric: "tokens", DataSource: "unavailable"},
		},
	}}
	csv := renderBillingCSV(rows)
	header := "tenant_id,tenant_name,period,status,usage_cost_usd,credit_usd,balance_usd,invoice_no,due_date,metric,usage_amount,unit_cost,cost,data_source\r\n"
	if len(csv) < len(header) || csv[:len(header)] != header {
		t.Fatalf("csv header mismatch:\n%q", csv)
	}
	if !strings.Contains(csv, "tid,acme,2026-09,current,2.4,,,,,gpu_hours,2,1.2,2.4,metered") {
		t.Fatalf("csv missing metered row:\n%s", csv)
	}
	if !strings.Contains(csv, "tid,acme,2026-09,current,2.4,,,,,tokens,,,,unavailable") {
		t.Fatalf("csv missing unavailable row:\n%s", csv)
	}
}

func floatPtr(f float64) *float64 { return &f }
