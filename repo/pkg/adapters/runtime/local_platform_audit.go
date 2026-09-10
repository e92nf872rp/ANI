package runtime

import (
	"context"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// localPlatformAuditSamples 是 local/dev profile 的确定性 write 审计假数据，
// 便于无真实集群时验证接口语义（排序/过滤/游标形态），不承载真实数据。
var localPlatformAuditSamples = []ports.PlatformAuditLogItem{
	{
		AuditID:   "local-audit-00000001",
		Timestamp: time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC),
		Verb:      "create",
		User: ports.PlatformAuditUser{
			Username: "system:serviceaccount:ani-system:ani-gateway",
			Groups:   []string{"system:serviceaccounts", "system:serviceaccounts:ani-system"},
		},
		Resource: ports.PlatformAuditResource{
			Namespace: "ani-system",
			Resource:  "deployments",
			Name:      "ani-gateway",
		},
		ResponseCode: 201,
		Detail: ports.PlatformAuditDetail{
			RequestURI: "/api/v1/namespaces/ani-system/deployments?token=***",
			UserAgent:  "kubectl/v1.30.0",
		},
	},
	{
		AuditID:   "local-audit-00000002",
		Timestamp: time.Date(2026, 9, 10, 7, 59, 40, 0, time.UTC),
		Verb:      "delete",
		User: ports.PlatformAuditUser{
			Username: "system:masters",
			Groups:   []string{"system:masters"},
		},
		Resource: ports.PlatformAuditResource{
			Namespace: "ani-test2",
			Resource:  "pods",
			Name:      "temp-batch-job-xyz",
		},
		ResponseCode: 200,
		Detail: ports.PlatformAuditDetail{
			RequestURI: "/api/v1/namespaces/ani-test2/pods/temp-batch-job-xyz",
			UserAgent:  "kubectl/v1.30.0",
		},
	},
}

// LocalPlatformAudit local/dev profile 降级实现：返回固定 2 条 write 审计行，
// 按时间窗口与 label 过滤做确定性裁剪；DevProfile 走 local（real_provider=false）。
type LocalPlatformAudit struct {
	now func() time.Time
}

// 编译时断言 LocalPlatformAudit 实现 ports.PlatformAuditService。
var _ ports.PlatformAuditService = (*LocalPlatformAudit)(nil)

// NewLocalPlatformAudit 创建本地确定性审计实现。
func NewLocalPlatformAudit() *LocalPlatformAudit {
	return &LocalPlatformAudit{now: time.Now}
}

// QueryAuditLogs 返回确定性假数据（固定 2 条 write 审计行）。
// 过滤口径：时间窗口（TimeFrom/TimeTo）、verb、user、resource_type、namespace。
// 本地固定数据条数不足一页上限，NextAfter 恒为空（无更多页）。
func (s *LocalPlatformAudit) QueryAuditLogs(_ context.Context, query ports.PlatformAuditLogQuery) (ports.PlatformAuditLogResult, error) {
	end := s.now().UTC()
	if query.TimeTo != nil {
		end = query.TimeTo.UTC()
	}
	start := end.Add(-defaultAuditWindow)
	if query.TimeFrom != nil {
		start = query.TimeFrom.UTC()
	}

	items := make([]ports.PlatformAuditLogItem, 0, len(localPlatformAuditSamples))
	for _, item := range localPlatformAuditSamples {
		if item.Timestamp.Before(start) || item.Timestamp.After(end) {
			continue
		}
		if query.Verb != "" && item.Verb != query.Verb {
			continue
		}
		if query.User != "" && item.User.Username != query.User {
			continue
		}
		if query.ResourceType != "" && item.Resource.Resource != query.ResourceType {
			continue
		}
		if query.Namespace != "" && item.Resource.Namespace != query.Namespace {
			continue
		}
		items = append(items, item)
	}
	// 本地模拟真实语义：固定数据按时间倒序（samples 已倒序）。

	return ports.PlatformAuditLogResult{
		Items:     items,
		NextAfter: "",
		// 本地近似总量随过滤裁剪：total_approx 反映过滤后条数（与真实语义一致）。
		TotalApprox: int64(len(items)),
		DevProfile: ports.DevProfileInfo{
			Mode:         "local",
			Provider:     "local-platform-audit",
			RealProvider: false,
			Reason:       "local profile returns fixed deterministic audit samples; it is not a real k8s audit execution",
		},
	}, nil
}
