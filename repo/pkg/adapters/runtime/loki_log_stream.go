package runtime

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// lokiStreamDefaults 是 StreamLogs 的默认参数值。
const (
	lokiStreamDefaultLimit    = 1000
	lokiStreamDefaultInterval = 2 * time.Second
	lokiStreamForwardLimit    = 1000 // forward 轮询单次查询上限
)

// StreamLogs 实现 Loki 日志的流式输出：首屏回放 + 增量轮询。
//
// 执行流三段式（方案 §3.3）：
//  1. 首屏回放：query_range direction=backward, start=now-24h, end=now, limit=req.Limit
//     结果反转为时间正序，逐条 sink() 写出（level 过滤生效），记录 lastTS 游标。
//  2. 增量跟随：循环等待 interval_seconds，query_range direction=forward,
//     start=lastTS+1ns, end=now, limit=1000，窗口内结果按时间排序后去重，
//     逐条 sink() 写出，更新 lastTS。
//  3. ctx 取消或 sink 返回 error 时立即退出。
//
// 复用 loki_log_store.go 的 buildLokiLogQL / parseLokiLogLine / decodeLokiResponse，
// 多租户隔离语义不变（namespace 精确匹配 + pod 正则）。
func (s *LokiLogStore) StreamLogs(ctx context.Context, req ports.InstanceLogStreamRequest, namespace string, sink func(ports.InstanceLogEntry) error) error {
	if err := validateInstanceObservationIdentity(req.TenantID, req.InstanceID); err != nil {
		return err
	}
	if strings.TrimSpace(namespace) == "" {
		return fmt.Errorf("%w: namespace is required for loki tenant isolation", ports.ErrInvalid)
	}

	limit := normalizeLimit(req.Limit, lokiStreamDefaultLimit, 1000)
	interval := lokiStreamDefaultInterval
	if req.IntervalSeconds > 0 {
		interval = time.Duration(req.IntervalSeconds) * time.Second
	}
	level := strings.TrimSpace(req.Level)
	logql := buildLokiLogQL(namespace, req.InstanceID)

	// ── 阶段 1：首屏回放（backward） ──────────────────────────────────
	now := s.now()
	startNs := now.Add(-24 * time.Hour).UnixNano()
	endNs := now.UnixNano()

	lokiResp, err := s.queryRange(ctx, logql, startNs, endNs, limit, "backward")
	if err != nil {
		return fmt.Errorf("loki stream replay failed: %w", err)
	}

	// backward 结果反转为时间正序
	entries := mapLokiStreamsBackwardToForward(lokiResp, level)
	var lastTS time.Time
	for _, entry := range entries {
		if err := sink(entry); err != nil {
			return nil // sink 断开 = 客户端断开，静默退出
		}
		lastTS = entry.Timestamp
	}

	// 回放为空时 lastTS = now
	if lastTS.IsZero() {
		lastTS = now
	}

	// ── 阶段 2：增量跟随（forward 轮询） ───────────────────────────────
	seen := make(map[string]bool, len(entries)) // (timestamp_ns+line) identity 去重
	for _, entry := range entries {
		seen[lokiEntryIdentity(entry)] = true
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		now = s.now()
		// start = lastTS + 1ns 保证不重复查已推送的行
		queryStart := lastTS.Add(1 * time.Nanosecond).UnixNano()
		queryEnd := now.UnixNano()
		if queryStart > queryEnd {
			// 时钟回拨或 lastTS 已超过 now，跳过本周期
			continue
		}

		lokiResp, err := s.queryRange(ctx, logql, queryStart, queryEnd, lokiStreamForwardLimit, "forward")
		if err != nil {
			// 瞬时失败不中断流，下一周期自愈
			continue
		}

		// forward 窗口内结果按时间排序后去重
		fwdEntries := mapLokiStreamsForward(lokiResp, level)
		sort.Slice(fwdEntries, func(i, j int) bool {
			return fwdEntries[i].Timestamp.Before(fwdEntries[j].Timestamp)
		})

		for _, entry := range fwdEntries {
			id := lokiEntryIdentity(entry)
			if seen[id] {
				continue
			}
			seen[id] = true
			// 跳过 timestamp <= lastTS 的行（游标边界保护）
			if !entry.Timestamp.After(lastTS) {
				continue
			}
			if err := sink(entry); err != nil {
				return nil // sink 断开，静默退出
			}
			lastTS = entry.Timestamp
		}
	}
}

// queryRange 调用 Loki /loki/api/v1/query_range 并解析响应。
func (s *LokiLogStore) queryRange(ctx context.Context, logql string, startNs, endNs int64, limit int, direction string) (lokiResponse, error) {
	params := url.Values{}
	params.Set("query", logql)
	params.Set("start", strconv.FormatInt(startNs, 10))
	params.Set("end", strconv.FormatInt(endNs, 10))
	params.Set("limit", strconv.Itoa(limit))
	params.Set("direction", direction)

	reqURL := s.baseURL + "/loki/api/v1/query_range?" + params.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return lokiResponse{}, fmt.Errorf("loki query request: %w", err)
	}

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return lokiResponse{}, fmt.Errorf("loki query failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return lokiResponse{}, fmt.Errorf("loki returned status %d", resp.StatusCode)
	}

	return decodeLokiResponse(resp.Body)
}

// mapLokiStreamsBackwardToForward 把 backward 结果反转为时间正序并做 level 过滤。
// backward 返回最新在前，反转后最旧在前（与日志展示习惯对齐）。
func mapLokiStreamsBackwardToForward(resp lokiResponse, level string) []ports.InstanceLogEntry {
	var items []ports.InstanceLogEntry
	for _, stream := range resp.Data.Result {
		container := stream.Stream["container"]
		for _, v := range stream.Values {
			if len(v) < 2 {
				continue
			}
			tsNsStr, line := v[0], v[1]
			tsInt, parseErr := strconv.ParseInt(tsNsStr, 10, 64)
			if parseErr != nil {
				continue
			}
			ts := time.Unix(0, tsInt).UTC()
			entry := parseLokiLogLine(line, ts, container)
			if level != "" && entry.Level != level {
				continue
			}
			items = append(items, entry)
		}
	}
	// backward 返回最新在前，反转为最旧在前
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items
}

// mapLokiStreamsForward 把 forward 结果映射为日志条目并做 level 过滤。
// forward 返回最旧在前，不需要反转，但多 stream 合并需调用方排序后去重。
func mapLokiStreamsForward(resp lokiResponse, level string) []ports.InstanceLogEntry {
	var items []ports.InstanceLogEntry
	for _, stream := range resp.Data.Result {
		container := stream.Stream["container"]
		for _, v := range stream.Values {
			if len(v) < 2 {
				continue
			}
			tsNsStr, line := v[0], v[1]
			tsInt, parseErr := strconv.ParseInt(tsNsStr, 10, 64)
			if parseErr != nil {
				continue
			}
			ts := time.Unix(0, tsInt).UTC()
			entry := parseLokiLogLine(line, ts, container)
			if level != "" && entry.Level != level {
				continue
			}
			items = append(items, entry)
		}
	}
	return items
}

// lokiEntryIdentity 构造日志条目的唯一标识（纳秒时间戳 + 行内容），用于跨周期去重。
func lokiEntryIdentity(entry ports.InstanceLogEntry) string {
	return strconv.FormatInt(entry.Timestamp.UnixNano(), 10) + "|" + entry.Message
}
