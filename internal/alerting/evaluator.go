package alerting

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/mihomo"
	"github.com/foxc888/foxos/internal/mosdns"
	"github.com/foxc888/foxos/internal/routeros"
)

type Store interface {
	SaveAlert(context.Context, domain.Alert) error
	ResolveAlert(context.Context, string, time.Time) error
	TrackAlertSignal(context.Context, string, bool, time.Time) (int, error)
	AlertSignalKeys(context.Context, string) ([]string, error)
	Jobs(context.Context, int) ([]domain.Job, error)
	Subscriptions(context.Context) ([]domain.Subscription, error)
}

type RouterReader interface {
	Resource(context.Context) (routeros.Resource, error)
	Routes(context.Context) ([]routeros.Route, error)
	Containers(context.Context) ([]routeros.Container, error)
}

type MihomoReader interface {
	Status(context.Context) (mihomo.RuntimeStatus, error)
}

type MosDNSReader interface {
	Status(context.Context) mosdns.Status
}

type Evaluator struct {
	Store    Store
	RouterOS RouterReader
	Mihomo   MihomoReader
	MosDNS   MosDNSReader
	Now      func() time.Time
}

func (e Evaluator) Evaluate(ctx context.Context) error {
	if e.Store == nil {
		return errors.New("alert store is required")
	}
	now := time.Now().UTC()
	if e.Now != nil {
		now = e.Now().UTC()
	}
	var failures []error
	if e.RouterOS != nil {
		failures = appendError(failures, e.evaluateRouterOS(ctx, now))
	}
	if e.Mihomo != nil {
		failures = appendError(failures, e.evaluateMihomo(ctx, now))
	}
	if e.MosDNS != nil {
		failures = appendError(failures, e.evaluateMosDNS(ctx, now))
	}
	failures = appendError(failures, e.evaluateJobs(ctx, now))
	failures = appendError(failures, e.evaluateSubscriptions(ctx, now))
	return errors.Join(failures...)
}

func appendError(items []error, err error) []error {
	if err != nil {
		return append(items, err)
	}
	return items
}

func (e Evaluator) evaluateRouterOS(ctx context.Context, now time.Time) error {
	var failures []error
	routes, err := e.RouterOS.Routes(ctx)
	if err != nil {
		failures = appendError(failures, e.setCondition(ctx, now, "telemetry.routeros.routes", true, 1, domain.AlertWarning, "RouterOS 路由状态不可用", "无法读取路由表；FoxOS 未据此判断 WAN 离线"))
	} else {
		failures = appendError(failures, e.setCondition(ctx, now, "telemetry.routeros.routes", false, 1, domain.AlertWarning, "", ""))
		activeDefault := false
		for _, route := range routes {
			if (route.Dst == "0.0.0.0/0" || route.Dst == "::/0") && route.Active == "true" && route.Disabled != "true" && route.Gateway != "" {
				activeDefault = true
				break
			}
		}
		failures = appendError(failures, e.setCondition(ctx, now, "wan.default_route", !activeDefault, 2, domain.AlertCritical, "WAN 默认路由不可用", "RouterOS 连续两次未返回活动默认路由"))
	}

	containers, err := e.RouterOS.Containers(ctx)
	if err != nil {
		failures = appendError(failures, e.setCondition(ctx, now, "telemetry.routeros.containers", true, 1, domain.AlertWarning, "RouterOS 容器状态不可用", "无法读取 /container；FoxOS 未推断任何容器离线"))
	} else {
		failures = appendError(failures, e.setCondition(ctx, now, "telemetry.routeros.containers", false, 1, domain.AlertWarning, "", ""))
		for _, container := range containers {
			if !strings.HasPrefix(container.Comment, "foxos:") {
				continue
			}
			name := container.Name
			if name == "" {
				name = container.Comment
			}
			key := "container.offline." + shortKey(container.ID+"\x00"+container.Comment)
			message := fmt.Sprintf("FoxOS 所有权容器 %s 连续两次状态不是 running", boundedName(name))
			failures = appendError(failures, e.setCondition(ctx, now, key, !strings.EqualFold(container.Status, "running"), 2, domain.AlertCritical, "管理容器离线", message))
		}
	}

	resource, err := e.RouterOS.Resource(ctx)
	if err != nil {
		failures = appendError(failures, e.setCondition(ctx, now, "telemetry.routeros.resource", true, 1, domain.AlertWarning, "RouterOS 资源状态不可用", "无法读取 CPU、内存和磁盘状态"))
	} else {
		failures = appendError(failures, e.setCondition(ctx, now, "telemetry.routeros.resource", false, 1, domain.AlertWarning, "", ""))
		free, freeErr := strconv.ParseInt(resource.FreeHDD, 10, 64)
		total, totalErr := strconv.ParseInt(resource.TotalHDD, 10, 64)
		if freeErr == nil && totalErr == nil && total > 0 && free >= 0 {
			ratio := float64(free) / float64(total)
			low := free < 512<<20 || ratio < 0.10
			severity := domain.AlertWarning
			if free < 128<<20 || ratio < 0.05 {
				severity = domain.AlertCritical
			}
			message := fmt.Sprintf("RouterOS 可用磁盘 %.1f%%（%d MiB）", ratio*100, free>>20)
			failures = appendError(failures, e.setCondition(ctx, now, "disk.routeros.low", low, 1, severity, "RouterOS 磁盘空间不足", message))
		}
	}
	return errors.Join(failures...)
}

func (e Evaluator) evaluateMihomo(ctx context.Context, now time.Time) error {
	status, err := e.Mihomo.Status(ctx)
	if err != nil {
		return e.setCondition(ctx, now, "telemetry.mihomo.controller", true, 1, domain.AlertWarning, "Mihomo 运行状态不可用", "Controller 状态读取失败；FoxOS 未推断节点故障")
	}
	var failures []error
	failures = appendError(failures, e.setCondition(ctx, now, "telemetry.mihomo.controller", false, 1, domain.AlertWarning, "", ""))
	seen := make(map[string]struct{})
	for name, raw := range status.Proxies {
		proxy, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		kind, _ := proxy["type"].(string)
		if !runtimeNodeType(kind) {
			continue
		}
		alive, known := proxy["alive"].(bool)
		if !known {
			continue
		}
		key := "node.failure." + shortKey(name)
		seen[key] = struct{}{}
		message := fmt.Sprintf("Mihomo 连续三次报告节点 %s 不可用", boundedName(name))
		failures = appendError(failures, e.setCondition(ctx, now, key, !alive, 3, domain.AlertWarning, "代理节点持续失败", message))
	}
	keys, err := e.Store.AlertSignalKeys(ctx, "node.failure.")
	if err != nil {
		failures = append(failures, err)
	} else {
		for _, key := range keys {
			if _, exists := seen[key]; exists {
				continue
			}
			failures = appendError(failures, e.setCondition(ctx, now, key, false, 3, domain.AlertWarning, "", ""))
		}
	}
	return errors.Join(failures...)
}

func runtimeNodeType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "direct", "reject", "reject-drop", "pass", "compatible", "selector", "urltest", "fallback", "loadbalance", "relay":
		return false
	default:
		return true
	}
}

func (e Evaluator) evaluateMosDNS(ctx context.Context, now time.Time) error {
	status := e.MosDNS.Status(ctx)
	active := status.Configured && !status.Online
	return e.setCondition(ctx, now, "dns.mosdns.offline", active, 2, domain.AlertCritical, "MosDNS 异常", "MosDNS 连续两次 TCP 健康检查失败")
}

func (e Evaluator) evaluateJobs(ctx context.Context, now time.Time) error {
	jobs, err := e.Store.Jobs(ctx, 500)
	if err != nil {
		return e.setCondition(ctx, now, "telemetry.jobs", true, 1, domain.AlertWarning, "任务告警状态不可用", "无法读取持久任务结果")
	}
	var failures []error
	failures = appendError(failures, e.setCondition(ctx, now, "telemetry.jobs", false, 1, domain.AlertWarning, "", ""))
	latest := make(map[string]domain.Job)
	for _, job := range jobs {
		if _, exists := latest[job.Kind]; !exists {
			latest[job.Kind] = job
		}
	}
	labels := map[string]string{
		"mihomo.apply":        "Mihomo 配置发布",
		"mihomo.restore":      "Mihomo 配置回滚",
		"routeros.egress":     "设备出口策略",
		"backup.restore":      "FoxOS 备份恢复",
		"subscription.update": "订阅更新",
	}
	for kind, label := range labels {
		job, exists := latest[kind]
		failed := exists && (job.Status == domain.JobFailed || job.Status == domain.JobRolledBack)
		rolledBack := exists && job.Status == domain.JobRolledBack
		keySuffix := strings.ReplaceAll(kind, ".", "_")
		message := label + "的最近任务失败"
		if job.ErrorClass != "" {
			message += "，错误分类：" + job.ErrorClass
		}
		severity := domain.AlertCritical
		if kind == "subscription.update" || rolledBack {
			severity = domain.AlertWarning
		}
		failures = appendError(failures, e.setCondition(ctx, now, "config.apply_failed."+keySuffix, failed, 1, severity, "配置应用失败", message))
		failures = appendError(failures, e.setCondition(ctx, now, "config.auto_rollback."+keySuffix, rolledBack, 1, domain.AlertWarning, "配置已自动回滚", label+"失败后已进入 ROLLED_BACK 状态"))
	}
	return errors.Join(failures...)
}

func (e Evaluator) evaluateSubscriptions(ctx context.Context, now time.Time) error {
	items, err := e.Store.Subscriptions(ctx)
	if err != nil {
		return e.setCondition(ctx, now, "telemetry.subscriptions", true, 1, domain.AlertWarning, "订阅状态不可用", "无法读取订阅更新时间")
	}
	var failures []error
	failures = appendError(failures, e.setCondition(ctx, now, "telemetry.subscriptions", false, 1, domain.AlertWarning, "", ""))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		key := "subscription.expired." + shortKey(item.ID)
		seen[key] = struct{}{}
		overdue := false
		if item.Enabled && item.Interval > 0 {
			if !item.LastSuccessAt.IsZero() {
				overdue = now.Sub(item.LastSuccessAt) > 2*time.Duration(item.Interval)*time.Second
			} else if !item.CreatedAt.IsZero() {
				overdue = now.Sub(item.CreatedAt) > time.Duration(item.Interval)*time.Second
			}
		}
		message := fmt.Sprintf("订阅 %s 已超过更新周期，旧节点仍保持不变", boundedName(item.Name))
		failures = appendError(failures, e.setCondition(ctx, now, key, overdue, 1, domain.AlertWarning, "订阅更新已过期", message))
	}
	keys, err := e.Store.AlertSignalKeys(ctx, "subscription.expired.")
	if err != nil {
		failures = append(failures, err)
	} else {
		for _, key := range keys {
			if _, exists := seen[key]; !exists {
				failures = appendError(failures, e.setCondition(ctx, now, key, false, 1, domain.AlertWarning, "", ""))
			}
		}
	}
	return errors.Join(failures...)
}

func (e Evaluator) setCondition(ctx context.Context, now time.Time, key string, active bool, threshold int, severity domain.AlertSeverity, title, message string) error {
	count, err := e.Store.TrackAlertSignal(ctx, key, active, now)
	if err != nil {
		return err
	}
	if !active {
		return e.Store.ResolveAlert(ctx, key, now)
	}
	if count < threshold {
		return nil
	}
	return e.Store.SaveAlert(ctx, domain.Alert{ID: "alert-" + shortKey(key), Key: key, Severity: severity, Title: title, Message: message, FirstSeen: now, LastSeen: now})
}

func shortKey(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:8])
}

func boundedName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "未命名资源"
	}
	if len(value) > 128 {
		return value[:128]
	}
	return value
}

type Monitor struct {
	cancel context.CancelFunc
	wait   sync.WaitGroup
}

func Start(parent context.Context, evaluator Evaluator, interval time.Duration, onError func(error)) (*Monitor, error) {
	if evaluator.Store == nil || interval < time.Second {
		return nil, errors.New("alert evaluator and interval are required")
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	monitor := &Monitor{cancel: cancel}
	monitor.wait.Add(1)
	go func() {
		defer monitor.wait.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			evaluateCtx, evaluateCancel := context.WithTimeout(ctx, 30*time.Second)
			err := evaluator.Evaluate(evaluateCtx)
			evaluateCancel()
			if err != nil && onError != nil {
				onError(err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return monitor, nil
}

func (m *Monitor) Close() {
	if m == nil {
		return
	}
	m.cancel()
	m.wait.Wait()
}
