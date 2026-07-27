import { FormEvent, useCallback, useEffect, useState } from "react";
import { AlertTriangle, Calculator, RefreshCw } from "lucide-react";
import {
  ApiError,
  type DHCPAddressPlan,
  type DHCPExpansionPlan,
  executeDHCPExpansion,
  getDHCPAddressPlan,
  previewDHCPExpansion,
} from "../api";
import { ConfirmDialog } from "./Dialog";

type Notice = (message: string, tone?: "success" | "warning") => void;

function errorMessage(error: unknown, fallback: string): string {
  if (error instanceof ApiError) return error.message;
  return error instanceof Error ? error.message : fallback;
}

function riskLabel(risk: string): string {
  if (risk === "exhausted") return "已耗尽";
  if (risk === "critical") return "临近耗尽";
  if (risk === "warning") return "容量偏高";
  if (risk === "normal") return "正常";
  return "未确认";
}

export function DHCPPlannerPanel({ notify }: { notify: Notice }) {
  const [addressPlan, setAddressPlan] = useState<DHCPAddressPlan | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [serverName, setServerName] = useState("");
  const [requestedCapacity, setRequestedCapacity] = useState("");
  const [proposedRanges, setProposedRanges] = useState("");
  const [preview, setPreview] = useState<DHCPExpansionPlan | null>(null);
  const [confirmationToken, setConfirmationToken] = useState("");

  const load = useCallback(async (announce = false) => {
    setLoading(true);
    try {
      const result = await getDHCPAddressPlan();
      setAddressPlan(result.plan);
      setServerName((current) => current || result.plan.servers.find((item) => item.ready)?.serverName || "");
      if (announce) notify("DHCP 地址状态已重新读取");
    } catch (error) {
      notify(errorMessage(error, "DHCP 地址规划数据不可用"), "warning");
    } finally {
      setLoading(false);
    }
  }, [notify]);

  useEffect(() => {
    void load(false);
  }, [load]);

  const generatePreview = async (event: FormEvent) => {
    event.preventDefault();
    const capacity = Number(requestedCapacity);
    if (!Number.isInteger(capacity) || capacity < 1) {
      notify("目标动态容量必须是正整数", "warning");
      return;
    }
    setBusy(true);
    setPreview(null);
    setConfirmationToken("");
    try {
      const result = await previewDHCPExpansion({ serverName, requestedCapacity: capacity, proposedRanges: proposedRanges.trim() });
      setPreview(result.plan);
      setConfirmationToken(result.confirmationToken);
      notify(result.plan.executable ? "扩容精确计划已生成，请核对后确认" : "已生成容量预览；当前方案不可自动执行", result.plan.executable ? "success" : "warning");
    } catch (error) {
      notify(errorMessage(error, "DHCP 扩容预览失败"), "warning");
    } finally {
      setBusy(false);
    }
  };

  const execute = async () => {
    if (!preview || !confirmationToken) return;
    setBusy(true);
    try {
      await executeDHCPExpansion(preview, confirmationToken);
      setPreview(null);
      setConfirmationToken("");
      await load(false);
      notify("DHCP 地址池已执行、回读并写入审计");
    } catch (error) {
      notify(errorMessage(error, "DHCP 扩容失败；请重新读取状态后生成新计划"), "warning");
    } finally {
      setBusy(false);
    }
  };

  const selected = addressPlan?.servers.find((item) => item.serverName === serverName);

  return (
    <section className="panel dhcp-planner-panel">
      <div className="panel-heading">
        <div><h2>DHCP 地址规划</h2><p>容量包含范围首尾；静态、基础设施和冲突地址从动态容量中排除</p></div>
        <button aria-label="刷新 DHCP 地址规划" className="icon-button" disabled={loading || busy} onClick={() => void load(true)} title="刷新 DHCP 地址规划" type="button"><RefreshCw aria-hidden="true" size={16} /></button>
      </div>

      {addressPlan?.servers.length ? <div className="capacity-grid">{addressPlan.servers.map((server) => (
        <article className="capacity-item" key={server.serverName}>
          <div className="capacity-title"><strong>{server.serverName}</strong><span className={`risk-badge ${server.risk}`}>{riskLabel(server.risk)}</span></div>
          <small>{server.interface} · {server.network || "网段未确认"}</small>
          {server.ready ? <dl className="capacity-metrics">
            <div><dt>配置范围</dt><dd>{server.configuredCapacity}</dd></div>
            <div><dt>安全动态容量</dt><dd>{server.dynamicCapacity}</dd></div>
            <div><dt>动态占用</dt><dd>{server.dynamicOccupied}</dd></div>
            <div><dt>剩余</dt><dd>{server.remaining}</dd></div>
            <div><dt>池内排除</dt><dd>{server.excludedWithinPool}</dd></div>
            <div><dt>静态保留剩余</dt><dd>{server.reservationRemaining}</dd></div>
          </dl> : <p className="warning-note"><AlertTriangle aria-hidden="true" size={15} />{server.error || "该 DHCP server 暂不可分析"}</p>}
          {server.conflicts.length ? <details><summary>{server.conflicts.length} 个冲突或保留项</summary><ul className="impact-list compact-list">{server.conflicts.map((conflict, index) => <li key={`${conflict.address || conflict.kind}-${index}`}>{conflict.address ? `${conflict.address} · ` : ""}{conflict.detail}</li>)}</ul></details> : null}
        </article>
      ))}</div> : <div className="operation-empty">{loading ? "正在读取 RouterOS 地址资源…" : "没有可分析的 DHCP server"}</div>}

      <form className="dhcp-plan-form" onSubmit={generatePreview}>
        <label className="field"><span>DHCP server</span><select disabled={busy || !addressPlan?.servers.length} required value={serverName} onChange={(event) => { setServerName(event.target.value); setPreview(null); setConfirmationToken(""); }}><option value="">选择 server</option>{addressPlan?.servers.map((server) => <option disabled={!server.ready} key={server.serverName} value={server.serverName}>{server.serverName}</option>)}</select></label>
        <label className="field"><span>目标安全动态容量</span><input disabled={busy || !selected?.ready} inputMode="numeric" min={1} required type="number" value={requestedCapacity} onChange={(event) => setRequestedCapacity(event.target.value)} /></label>
        <label className="field dhcp-ranges-field"><span>完整拟议范围</span><input disabled={busy || !selected?.ready} placeholder="10.0.0.50-10.0.0.200" value={proposedRanges} onChange={(event) => setProposedRanges(event.target.value)} /></label>
        <button className="button primary" disabled={busy || loading || !selected?.ready} type="submit"><Calculator aria-hidden="true" size={16} />{busy ? "正在规划…" : "生成精确计划"}</button>
      </form>

      {preview ? <div className="dhcp-preview" aria-live="polite">
        <div><strong>{preview.executable ? "可执行计划" : "容量预览"}</strong><span>{preview.before.dynamicCapacity} → {preview.after?.dynamicCapacity ?? preview.requestedCapacity}</span></div>
        {preview.suggestedRanges.length ? <p>可用候选：{preview.suggestedRanges.map((range) => `${range.start}-${range.end}（${range.capacity}）`).join("，")}</p> : null}
        {preview.warnings.map((warning) => <p className="warning-note" key={warning}><AlertTriangle aria-hidden="true" size={15} />{warning}</p>)}
        {preview.alternatives.map((alternative) => <div className="capacity-alternative" key={alternative.strategy}><strong>{alternative.strategy === "expand-to-/23" ? "扩大至 /23" : "VLAN 拆分"} · {alternative.capacity}</strong><ul>{alternative.impact.map((impact) => <li key={impact}>{impact}</li>)}</ul></div>)}
      </div> : null}

      {preview?.executable && confirmationToken ? <ConfirmDialog
        busy={busy}
        confirmLabel="确认扩展地址池"
        description="执行前会重新读取完整 RouterOS 状态并核对摘要；只修改带精确 FoxOS 所有权标记的地址池。"
        impacts={[`DHCP server：${preview.serverName}`, `地址池：${preview.poolName || "未命名"}`, `范围：${preview.currentRanges} → ${preview.proposedRanges}`, `安全动态容量：${preview.before.dynamicCapacity} → ${preview.after?.dynamicCapacity}`]}
        onCancel={() => { if (!busy) { setPreview(null); setConfirmationToken(""); } }}
        onConfirm={() => void execute()}
        title="确认 DHCP 地址池扩容"
        warnings={preview.warnings}
      /> : null}
    </section>
  );
}
