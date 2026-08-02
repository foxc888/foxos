import { FormEvent, useEffect, useState } from "react";
import {
  AlertTriangle,
  Archive,
  Check,
  Eye,
  FileDiff,
  Play,
  RefreshCw,
  RotateCcw,
  Save,
  ShieldAlert,
  Trash2,
  Upload,
} from "lucide-react";
import {
  acknowledgeAlert,
  applyMihomoConfig,
  createBackup,
  createSubscription,
  deleteSubscription,
  getMihomoDraft,
  listAlerts,
  listBackups,
  listMihomoSnapshots,
  listSubscriptions,
  planBackupRestore,
  planMihomoRestore,
  planSubscriptionDelete,
  previewMihomoConfig,
  previewSubscription,
  restoreBackup,
  restoreMihomoSnapshot,
  setSubscriptionEnabled,
  saveMihomoDraft,
  updateSubscription,
  waitForJob,
  type Alert,
  type BackupManifest,
  type MihomoDraft,
  type MihomoPreview,
  type MihomoSnapshot,
  type Subscription,
	type SubscriptionDeleteStrategy,
  type SubscriptionUpdatePlan,
} from "../api";
import { ConfirmDialog, Dialog } from "./Dialog";

type Notice = (message: string, tone?: "success" | "warning") => void;
const emptyDraft: MihomoDraft = { mode: "rule", mixedPort: 7890, allowLan: false, rules: ["MATCH,DIRECT"] };

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

function OperationBadge({ children, tone = "muted" }: { children: React.ReactNode; tone?: "ok" | "warning" | "muted" }) {
  return <span className={"operation-badge " + tone}>{children}</span>;
}

export function MihomoOperations({ notify }: { notify: Notice }) {
  const [draft, setDraft] = useState<MihomoDraft>(emptyDraft);
  const [preview, setPreview] = useState<MihomoPreview | null>(null);
  const [previewToken, setPreviewToken] = useState("");
  const [snapshots, setSnapshots] = useState<MihomoSnapshot[]>([]);
  const [loading, setLoading] = useState(true);
	const [loadError, setLoadError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState<{ title: string; description: string; impacts: string[]; warnings: string[]; token: string; snapshotId?: string } | null>(null);

	const updateDraft = (update: (current: MihomoDraft) => MihomoDraft) => {
		setDraft(update);
		setPreview(null);
		setPreviewToken("");
		setConfirm((current) => current?.snapshotId ? current : null);
	};

  const load = async () => {
    setLoading(true);
		setLoadError(null);
    try {
      const [stored, savedSnapshots] = await Promise.all([getMihomoDraft(), listMihomoSnapshots()]);
      setDraft(stored);
      setSnapshots(savedSnapshots);
    } catch (error) {
			const message = errorMessage(error, "Mihomo 草稿不可用");
			setLoadError(message);
			setPreview(null);
			setPreviewToken("");
			setSnapshots([]);
			notify(message, "warning");
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => { void load(); }, []);
	const controlsDisabled = busy || loading || loadError !== null;

  const save = async () => {
		if (controlsDisabled) return;
    setBusy(true);
    try {
      const stored = await saveMihomoDraft(draft);
      setDraft(stored);
      setPreview(null);
      setPreviewToken("");
      notify("Mihomo 草稿已保存，尚未写入运行配置");
    } catch (error) {
      notify(errorMessage(error, "Mihomo 草稿保存失败"), "warning");
    } finally {
      setBusy(false);
    }
  };
  const previewConfig = async () => {
		if (controlsDisabled) return;
    setBusy(true);
    try {
      const result = await previewMihomoConfig(draft);
      setPreview(result.preview);
      setPreviewToken(result.confirmationToken);
      notify("已生成脱敏预览与变更摘要");
    } catch (error) {
      notify(errorMessage(error, "Mihomo 配置预览失败"), "warning");
    } finally {
      setBusy(false);
    }
  };
  const apply = async () => {
    if (!confirm || confirm.snapshotId || !preview || !previewToken) return;
    const execution = {
      draft: { ...draft, rules: [...draft.rules] },
      digest: preview.digest,
      token: confirm.token,
    };
    setConfirm(null);
    setPreview(null);
    setPreviewToken("");
    setBusy(true);
    try {
      const result = await applyMihomoConfig(execution.draft, execution.digest, execution.token, "FoxOS 控制台发布");
      let outcome: { message: string; tone: "success" | "warning" };
      if (result.job) {
        const job = await waitForJob(result.job.id);
        if (job.status === "FAILED") throw new Error(job.errorMessage || "Mihomo 发布失败");
        outcome = job.status === "ROLLED_BACK"
          ? { message: "Mihomo 发布失败，已自动回滚到上一份快照", tone: "warning" }
          : { message: "Mihomo 配置已发布并完成健康验证", tone: "success" };
      } else {
        outcome = result.rolledBack
          ? { message: "Mihomo 发布失败，已自动回滚", tone: "warning" }
          : { message: "Mihomo 配置已发布并完成健康验证", tone: "success" };
      }
      try {
        setSnapshots(await listMihomoSnapshots());
        notify(outcome.message, outcome.tone);
      } catch (error) {
        notify(`${outcome.message}；快照列表回读未确认：${errorMessage(error, "快照列表回读失败")}`, "warning");
      }
    } catch (error) {
      notify(errorMessage(error, "Mihomo 发布失败"), "warning");
    } finally {
      setBusy(false);
    }
  };
  const prepareRestore = async (snapshot: MihomoSnapshot) => {
    try {
      const result = await planMihomoRestore(snapshot.id);
      setConfirm({
        title: "确认恢复 Mihomo 快照",
        description: "运行中的 Mihomo 配置将被原子替换，并执行热重载与健康校验。",
        impacts: ["快照：" + snapshot.id, "摘要：" + snapshot.digest, "失败时恢复当前运行配置"],
        warnings: ["恢复不会修改 RouterOS、MosDNS 或数据库设备策略"],
        token: result.confirmationToken,
        snapshotId: snapshot.id,
      });
    } catch (error) {
      notify(errorMessage(error, "快照恢复计划失败"), "warning");
    }
  };
  const restore = async () => {
    if (!confirm?.snapshotId) return;
    const execution = { snapshotId: confirm.snapshotId, token: confirm.token };
    setConfirm(null);
    setBusy(true);
    try {
      const result = await restoreMihomoSnapshot(execution.snapshotId, execution.token, "FoxOS 控制台恢复");
      if (result.job) {
        const job = await waitForJob(result.job.id);
        if (job.status === "FAILED") throw new Error(job.errorMessage || "快照恢复失败");
        notify(job.status === "ROLLED_BACK" ? "快照恢复失败，已自动回滚" : "Mihomo 快照已恢复", job.status === "ROLLED_BACK" ? "warning" : "success");
      } else {
        notify(result.rolledBack ? "快照恢复失败，已自动回滚" : "Mihomo 快照已恢复", result.rolledBack ? "warning" : "success");
      }
      await load();
    } catch (error) {
      notify(errorMessage(error, "快照恢复失败"), "warning");
    } finally {
      setBusy(false);
    }
  };
  return (
    <section className="panel operations-panel" id="mihomo-publish">
			<div className="panel-heading"><div><h2>Mihomo 配置发布</h2><p>草稿、脱敏 Diff、快照和任务状态</p></div><OperationBadge tone={loading || loadError ? "warning" : "ok"}>{loading ? "加载中" : loadError ? "不可用" : "SQLite 草稿"}</OperationBadge></div>
			{loadError ? <div className="warning-note operation-availability" role="alert"><AlertTriangle aria-hidden="true" size={16} /><span>Mihomo 配置服务不可用：{loadError}。恢复并成功回读前不会开放草稿编辑或发布。</span><button className="button secondary compact" disabled={busy || loading} onClick={() => void load()} type="button"><RefreshCw aria-hidden="true" size={14} />重试加载</button></div> : null}
      <div className="operation-form-grid">
				<label className="field"><span>运行模式</span><select disabled={controlsDisabled} value={draft.mode} onChange={(event) => updateDraft((value) => ({ ...value, mode: event.target.value }))}><option value="rule">rule</option><option value="global">global</option><option value="direct">direct</option></select></label>
				<label className="field"><span>Mixed Port</span><input disabled={controlsDisabled} min={1} max={65535} type="number" value={draft.mixedPort || ""} onChange={(event) => updateDraft((value) => ({ ...value, mixedPort: Number(event.target.value) }))} /></label>
				<label className="field operation-check"><input checked={draft.allowLan} disabled={controlsDisabled} type="checkbox" onChange={(event) => updateDraft((value) => ({ ...value, allowLan: event.target.checked }))} /><span>允许局域网访问</span></label>
				<label className="field operation-wide"><span>额外规则（每行一条）</span><textarea disabled={controlsDisabled} rows={4} value={draft.rules.join("\n")} onChange={(event) => updateDraft((value) => ({ ...value, rules: event.target.value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean) }))} /></label>
      </div>
      <div className="operation-actions">
				<button className="button secondary" disabled={controlsDisabled} onClick={() => void save()} type="button"><Save aria-hidden="true" size={16} />保存草稿</button>
				<button className="button primary" disabled={controlsDisabled} onClick={() => void previewConfig()} type="button"><Eye aria-hidden="true" size={16} />生成预览</button>
				<button className="button danger" disabled={controlsDisabled || !previewToken} onClick={() => preview && setConfirm({ title: "确认发布 Mihomo 配置", description: "配置将原子替换、热重载并执行健康验证；失败会自动回滚。", impacts: ["SHA-256：" + preview.digest, "变更行数：" + preview.diff.split("\n").filter(Boolean).length, "Mihomo 运行配置和快照目录"], warnings: ["发布令牌只允许使用一次，预览变化后必须重新生成"], token: previewToken })} type="button"><Play aria-hidden="true" size={16} />确认发布</button>
      </div>
			{loadError ? null : preview ? <div className="operation-preview" aria-live="polite"><div className="preview-heading"><span><FileDiff aria-hidden="true" size={16} />预览摘要</span><code>{preview.digest.slice(0, 16)}…</code></div><pre>{preview.yaml}</pre><details><summary>查看 Diff</summary><pre>{preview.diff || "没有配置变化"}</pre></details>{preview.hasSecret ? <p className="warning-note"><ShieldAlert aria-hidden="true" size={16} />预览已隐藏节点凭据；真实凭据只在本地运行配置中使用。</p> : null}</div> : <div className="operation-empty">保存草稿后生成预览，发布按钮会在获得一次性确认令牌后启用。</div>}
			<div className="snapshot-list"><div className="subheading"><h3>快照</h3><button aria-label="刷新 Mihomo 快照" className="icon-button" disabled={busy || loading} onClick={() => void load()} title="刷新快照" type="button"><RefreshCw size={16} /></button></div>{snapshots.length ? snapshots.map((snapshot) => <div className="snapshot-row" key={snapshot.id}><div><strong>{snapshot.label || "未命名快照"}</strong><small>{snapshot.createdAt} · {snapshot.digest.slice(0, 12)}…</small></div><button className="button secondary compact" disabled={controlsDisabled} onClick={() => void prepareRestore(snapshot)} type="button"><RotateCcw aria-hidden="true" size={14} />恢复</button></div>) : <span className="muted-text">{loadError ? "快照不可用" : "暂无快照"}</span>}</div>
      {confirm ? <ConfirmDialog busy={busy} confirmLabel="确认执行" description={confirm.description} impacts={confirm.impacts} onCancel={() => !busy && setConfirm(null)} onConfirm={() => void (confirm.snapshotId ? restore() : apply())} title={confirm.title} warnings={confirm.warnings} /> : null}
    </section>
  );
}

export function SubscriptionOperations({ notify }: { notify: Notice }) {
  const [items, setItems] = useState<Subscription[]>([]);
  const [name, setName] = useState("");
  const [url, setURL] = useState("");
  const [selected, setSelected] = useState<Subscription | null>(null);
  const [preview, setPreview] = useState<{ digest: string; nodeCount: number; plan: SubscriptionUpdatePlan; token: string } | null>(null);
  const [busy, setBusy] = useState(false);
	const [pendingReadbacks, setPendingReadbacks] = useState<Set<string>>(() => new Set());
	const [deleteChoice, setDeleteChoice] = useState<{ item: Subscription; strategy: SubscriptionDeleteStrategy } | null>(null);
	const [confirm, setConfirm] = useState<{ action: "update" | "delete"; item: Subscription; token: string; strategy?: SubscriptionDeleteStrategy; plan?: SubscriptionUpdatePlan; nodeCount: number; warnings: string[] } | null>(null);
  const load = async () => {
		const loaded = await listSubscriptions();
		setItems(loaded);
		setPendingReadbacks(new Set());
		return loaded;
  };
	useEffect(() => { void load().catch((error) => notify(errorMessage(error, "订阅源不可用"), "warning")); }, []);
  const add = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    try {
				const created = await createSubscription({ name, url, enabled: true, interval: 21600 });
      setName("");
      setURL("");
				try {
					const readback = await listSubscriptions();
					if (!readback.some((item) => item.id === created.id)) {
						throw new Error("订阅源已创建，但列表回读未包含新订阅；已标记为待确认");
					}
					setItems(readback);
					setPendingReadbacks((current) => {
						if (!current.has(created.id)) return current;
						const next = new Set(current);
						next.delete(created.id);
						return next;
					});
					notify("订阅源已保存并完成列表回读；尚未抓取节点");
			} catch (error) {
				setItems((current) => current.some((item) => item.id === created.id) ? current : [created, ...current]);
				setPendingReadbacks((current) => new Set(current).add(created.id));
				notify(errorMessage(error, "订阅源已创建，但列表回读失败；已标记为待确认"), "warning");
			}
    } catch (error) {
      notify(errorMessage(error, "订阅源保存失败"), "warning");
    } finally { setBusy(false); }
  };
  const inspect = async (item: Subscription) => {
    setBusy(true);
    try {
      const result = await previewSubscription(item.id);
      setSelected(item);
      setPreview({ digest: result.digest, nodeCount: result.nodeCount, plan: result.plan, token: result.confirmationToken });
    } catch (error) {
      notify(errorMessage(error, "订阅预览失败，旧节点保持不变"), "warning");
    } finally { setBusy(false); }
  };
  const prepareUpdate = async (item: Subscription) => {
    setBusy(true);
    try {
      const result = await previewSubscription(item.id);
      setSelected(item);
      setPreview({ digest: result.digest, nodeCount: result.nodeCount, plan: result.plan, token: result.confirmationToken });
			const warnings = ["预览令牌只能使用一次；远端内容变化后任务会失败并保留旧节点"];
			if (result.plan.parseSkippedCount > 0) warnings.push(`有 ${result.plan.parseSkippedCount} 个无效条目被跳过；确认后仅提交有效节点`);
			if (result.plan.suspiciousReduction) warnings.push("节点数量异常缩减；自动更新已禁止，只有本次显式确认可继续");
      setConfirm({ action: "update", item, token: result.confirmationToken, plan: result.plan, nodeCount: result.nodeCount, warnings });
    } catch (error) {
      notify(errorMessage(error, "订阅更新计划失败，旧节点保持不变"), "warning");
    } finally { setBusy(false); }
  };
	const prepareDelete = async (item: Subscription, strategy: SubscriptionDeleteStrategy) => {
    setBusy(true);
    try {
			const result = await planSubscriptionDelete(item.id, strategy);
			setDeleteChoice(null);
			setConfirm({ action: "delete", item, token: result.confirmationToken, strategy, nodeCount: result.plan.nodeCount, warnings: result.warnings });
    } catch (error) {
      notify(errorMessage(error, "订阅删除计划失败"), "warning");
    } finally { setBusy(false); }
  };
  const update = async () => {
    if (!confirm?.plan) return;
    const execution = { item: confirm.item, plan: confirm.plan, token: confirm.token };
    setConfirm(null);
    setSelected(null);
    setPreview(null);
    setBusy(true);
    try {
      const result = await updateSubscription(execution.item.id, execution.plan, execution.token);
      if (result.job) {
        const job = await waitForJob(result.job.id);
        if (job.status !== "SUCCEEDED") throw new Error(job.errorMessage || "订阅更新失败，旧节点保持不变");
      }
      const successMessage = "订阅已更新，当前包含 " + execution.plan.nodeCount + " 个去重节点";
      try {
        const readback = await listSubscriptions();
        if (!readback.some((item) => item.id === execution.item.id)) {
          throw new Error("列表回读未包含已更新的订阅");
        }
        setItems(readback);
        setPendingReadbacks((current) => {
          if (!current.has(execution.item.id)) return current;
          const next = new Set(current);
          next.delete(execution.item.id);
          return next;
        });
        notify(successMessage);
      } catch (error) {
        setItems((current) => current.some((item) => item.id === execution.item.id) ? current : [execution.item, ...current]);
        setPendingReadbacks((current) => new Set(current).add(execution.item.id));
        notify(`${successMessage}；列表回读未确认：${errorMessage(error, "订阅列表回读失败")}`, "warning");
      }
    } catch (error) {
      notify(errorMessage(error, "订阅更新失败，旧节点保持不变"), "warning");
    } finally { setBusy(false); }
  };
  const remove = async () => {
		if (!confirm?.strategy) return;
    const execution = { item: confirm.item, strategy: confirm.strategy, token: confirm.token };
    setConfirm(null);
    setDeleteChoice(null);
    setBusy(true);
    try {
				const result = await deleteSubscription(execution.item.id, execution.strategy, execution.token);
      const successMessage = result.strategy === "detach" ? `订阅源已删除，${result.nodeCount} 个节点已保留为手工节点` : `订阅源及 ${result.nodeCount} 个来源节点已删除`;
      setSelected(null);
      setPreview(null);
      setItems((current) => current.filter((item) => item.id !== execution.item.id));
      setPendingReadbacks((current) => {
        if (!current.has(execution.item.id)) return current;
        const next = new Set(current);
        next.delete(execution.item.id);
        return next;
      });
      try {
        const readback = await listSubscriptions();
        if (readback.some((item) => item.id === execution.item.id)) {
          throw new Error("列表回读仍包含已删除的订阅");
        }
        setItems(readback);
        notify(successMessage);
      } catch (error) {
        notify(`${successMessage}；列表回读未确认：${errorMessage(error, "订阅列表回读失败")}`, "warning");
      }
    } catch (error) {
      notify(errorMessage(error, "订阅删除失败"), "warning");
    } finally { setBusy(false); }
  };
  const toggleEnabled = async (item: Subscription) => {
    if (busy) return;
    setBusy(true);
    try {
      const stored = await setSubscriptionEnabled(item.id, !item.enabled);
      setItems((current) => current.map((candidate) => candidate.id === stored.id ? stored : candidate));
      notify(`${stored.name} 已${stored.enabled ? "启用" : "停用"}并完成服务端回读`);
    } catch (error) {
      notify(errorMessage(error, "订阅启停失败"), "warning");
    } finally {
      setBusy(false);
    }
  };
  return (
    <section className="panel operations-panel">
      <div className="panel-heading"><div><h2>订阅源</h2><p>受限抓取、预览、去重前的人工确认</p></div><Upload aria-hidden="true" className="green-text" size={20} /></div>
      <form className="operation-inline-form" onSubmit={add}><label className="field"><span>名称</span><input required value={name} onChange={(event) => setName(event.target.value)} /></label><label className="field operation-grow"><span>HTTPS URL</span><input required inputMode="url" placeholder="https://…" value={url} onChange={(event) => setURL(event.target.value)} /></label><button className="button primary" disabled={busy} type="submit">添加</button></form>
			<div className="subscription-list">{items.length ? items.map((item) => <div className="subscription-row" key={item.id}><div><strong>{item.name}</strong><small>{item.url}</small><small>{pendingReadbacks.has(item.id) ? "等待列表回读确认" : item.lastError ? "最近失败：" + item.lastError : item.lastSuccessAt ? "最近成功：" + item.lastSuccessAt : "尚未成功更新"}</small></div><div className="row-actions"><label className="toggle-control"><input aria-label={`${item.enabled ? "停用" : "启用"}订阅 ${item.name}`} checked={item.enabled} disabled={busy} onChange={() => void toggleEnabled(item)} type="checkbox" /><span>{item.enabled ? "已启用" : "已停用"}</span></label><button className="button secondary compact" disabled={busy} onClick={() => void inspect(item)} type="button"><Eye size={14} />预览</button><button className="button primary compact" disabled={busy || !item.enabled} onClick={() => void prepareUpdate(item)} type="button"><RefreshCw size={14} />更新</button><button aria-label={"删除订阅 " + item.name} className="icon-button danger-icon" disabled={busy} onClick={() => setDeleteChoice({ item, strategy: "detach" })} title="删除订阅" type="button"><Trash2 size={15} /></button></div></div>) : <div className="operation-empty">暂无订阅源</div>}</div>
			{selected && preview ? <div className="operation-callout" role="status"><strong>{selected.name} 预览</strong><span>{preview.nodeCount} 个去重节点 · 解析有效 {preview.plan.parseValidCount} · 跳过 {preview.plan.parseSkippedCount} · {preview.digest.slice(0, 16)}…</span></div> : null}
			{deleteChoice ? <Dialog footer={<><button className="button secondary" disabled={busy} onClick={() => setDeleteChoice(null)} type="button">取消</button><button className="button danger" disabled={busy} onClick={() => void prepareDelete(deleteChoice.item, deleteChoice.strategy)} type="button">生成删除计划</button></>} onClose={() => !busy && setDeleteChoice(null)} title="选择节点处理方式"><div aria-label="删除订阅后的节点处理方式" className="strategy-options" role="radiogroup"><label className="strategy-option"><input checked={deleteChoice.strategy === "detach"} name="subscription-delete-strategy" onChange={() => setDeleteChoice({ ...deleteChoice, strategy: "detach" })} type="radio" /><span><strong>保留为手工节点</strong><small>解除来源归属，现有代理组和策略引用保持有效</small></span></label><label className="strategy-option"><input checked={deleteChoice.strategy === "cascade"} name="subscription-delete-strategy" onChange={() => setDeleteChoice({ ...deleteChoice, strategy: "cascade" })} type="radio" /><span><strong>级联删除来源节点</strong><small>存在任何引用时服务器会拒绝并完整回滚</small></span></label></div></Dialog> : null}
			{confirm ? <ConfirmDialog busy={busy} confirmLabel={confirm.action === "update" ? "确认更新订阅" : "确认删除订阅"} description={confirm.action === "update" ? "任务会重新抓取并核对预览摘要，再原子替换该来源的节点。" : confirm.strategy === "detach" ? "订阅定义会删除，来源节点将在同一事务中转为手工节点。" : "订阅定义和全部来源节点将在同一事务中删除。"} impacts={["订阅：" + confirm.item.name, "地址：" + confirm.item.url, confirm.action === "update" && confirm.plan ? `新增 ${confirm.plan.addCount}，更新 ${confirm.plan.updateCount}，移除 ${confirm.plan.removeCount}` : `${confirm.strategy === "detach" ? "保留" : "删除"} ${confirm.nodeCount} 个来源节点`]} onCancel={() => !busy && setConfirm(null)} onConfirm={() => void (confirm.action === "update" ? update() : remove())} title={confirm.action === "update" ? "确认更新订阅" : "确认删除订阅"} warnings={confirm.warnings} /> : null}
    </section>
  );
}

export function AlertBackupOperations({ notify }: { notify: Notice }) {
  const [alerts, setAlerts] = useState<Alert[]>([]);
  const [backups, setBackups] = useState<BackupManifest[]>([]);
  const [busy, setBusy] = useState(false);
  const [confirmBackup, setConfirmBackup] = useState<{ item: BackupManifest; token: string; warnings: string[] } | null>(null);
  const load = async () => {
    try {
      const [nextAlerts, nextBackups] = await Promise.all([listAlerts(), listBackups()]);
      setAlerts(nextAlerts);
      setBackups(nextBackups);
    } catch (error) { notify(errorMessage(error, "告警或备份数据不可用"), "warning"); }
  };
  useEffect(() => { void load(); }, []);
  const acknowledge = async (id: string) => {
    try {
      await acknowledgeAlert(id);
      setAlerts((items) => items.map((item) => item.id === id ? { ...item, acknowledged: true } : item));
      notify("告警已确认");
    } catch (error) { notify(errorMessage(error, "告警确认失败"), "warning"); }
  };
  const backup = async () => {
    setBusy(true);
    try {
      const result = await createBackup("FoxOS 控制台备份");
      let manifest = result.manifest;
      if (result.job) {
        const job = await waitForJob(result.job.id);
        if (job.status !== "SUCCEEDED") throw new Error(job.errorMessage || "备份创建失败");
        const stored = job.result?.manifest;
        if (stored && typeof stored === "object" && "id" in stored) manifest = stored as unknown as BackupManifest;
      }
      await load();
      notify(manifest?.mihomo ? "SQLite 管理状态与 Mihomo 配置备份已创建" : manifest ? "SQLite 管理状态备份已创建；本次不含 Mihomo 配置" : "备份任务已成功；请在快照列表核对文件清单");
    } catch (error) { notify(errorMessage(error, "备份创建失败"), "warning"); }
    finally { setBusy(false); }
  };
  const prepareRestore = async (item: BackupManifest) => {
    try {
      const plan = await planBackupRestore(item.id);
      setConfirmBackup({ item, token: plan.confirmationToken, warnings: plan.warnings });
    } catch (error) { notify(errorMessage(error, "备份恢复计划失败"), "warning"); }
  };
  const restore = async () => {
    if (!confirmBackup) return;
		const execution = confirmBackup;
		setConfirmBackup(null);
    setBusy(true);
    try {
			const result = await restoreBackup(execution.item.id, execution.token);
      if (result.job) {
        const job = await waitForJob(result.job.id);
        if (job.status !== "SUCCEEDED") throw new Error(job.errorMessage || "备份恢复失败");
      }
			notify(execution.item.mihomo ? "备份已恢复，并完成 SQLite 完整性与 Mihomo 健康验证" : "SQLite 管理状态已恢复并完成完整性验证；未改动 Mihomo 配置");
      await load();
    } catch (error) { notify(errorMessage(error, "备份恢复失败"), "warning"); }
    finally { setBusy(false); }
  };
  return (
    <div className="operations-grid">
      <section className="panel operations-panel"><div className="panel-heading"><div><h2>告警中心</h2><p>未确认告警优先显示</p></div><AlertTriangle aria-hidden="true" className="yellow-text" size={20} /></div>{alerts.length ? alerts.map((alert) => <div className={"alert-row " + (alert.acknowledged ? "acknowledged" : "")} key={alert.id}><span className={"alert-severity " + alert.severity}>{alert.severity}</span><div><strong>{alert.title}</strong><span>{alert.message}</span><small>{alert.lastSeen}</small></div>{!alert.acknowledged ? <button className="button secondary compact" onClick={() => void acknowledge(alert.id)} type="button"><Check size={14} />确认</button> : <OperationBadge>已确认</OperationBadge>}</div>) : <div className="operation-empty">暂无未解决告警</div>}</section>
      <section className="panel operations-panel"><div className="panel-heading"><div><h2>备份与恢复</h2><p>SQLite、FoxOS 状态与 Mihomo 配置</p></div><Archive aria-hidden="true" className="blue-text" size={20} /></div><button className="button primary" disabled={busy} onClick={() => void backup()} type="button"><Save size={16} />创建备份</button><div className="backup-list">{backups.length ? backups.map((item) => <div className="backup-row" key={item.id}><div><strong>{item.label || "未命名备份"}</strong><small>{item.createdAt} · {item.database}{item.mihomo ? " + Mihomo" : ""}</small></div><button className="button danger compact" disabled={busy} onClick={() => void prepareRestore(item)} type="button"><RotateCcw size={14} />恢复</button></div>) : <div className="operation-empty">暂无备份</div>}</div></section>
      {confirmBackup ? <ConfirmDialog busy={busy} confirmLabel="确认恢复备份" description="当前 SQLite 管理数据会被备份内容替换。恢复后需要重新检查任务、Mihomo 和管理页面。" impacts={["备份：" + confirmBackup.item.id, "创建时间：" + confirmBackup.item.createdAt, "SQLite 管理数据库", confirmBackup.item.mihomo ? "Mihomo 配置快照" : "当前备份不含 Mihomo 文件"]} onCancel={() => !busy && setConfirmBackup(null)} onConfirm={() => void restore()} title="确认恢复备份" warnings={confirmBackup.warnings} /> : null}
    </div>
  );
}
