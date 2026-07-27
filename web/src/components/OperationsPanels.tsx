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
  saveMihomoDraft,
  updateSubscription,
  waitForJob,
  type Alert,
  type BackupManifest,
  type MihomoDraft,
  type MihomoPreview,
  type MihomoSnapshot,
  type Subscription,
  type SubscriptionUpdatePlan,
} from "../api";
import { ConfirmDialog } from "./Dialog";

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
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState<{ title: string; description: string; impacts: string[]; warnings: string[]; token: string; snapshotId?: string } | null>(null);

  const load = async () => {
    setLoading(true);
    try {
      const [stored, savedSnapshots] = await Promise.all([getMihomoDraft(), listMihomoSnapshots()]);
      setDraft(stored);
      setSnapshots(savedSnapshots);
    } catch (error) {
      notify(errorMessage(error, "Mihomo 草稿不可用"), "warning");
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => { void load(); }, []);

  const save = async () => {
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
    if (!preview || !previewToken) return;
    setBusy(true);
    try {
      const result = await applyMihomoConfig(draft, preview.digest, previewToken, "FoxOS 控制台发布");
      if (result.job) {
        const job = await waitForJob(result.job.id);
        if (job.status === "FAILED") throw new Error(job.errorMessage || "Mihomo 发布失败");
        notify(job.status === "ROLLED_BACK" ? "Mihomo 发布失败，已自动回滚到上一份快照" : "Mihomo 配置已发布并完成健康验证", job.status === "ROLLED_BACK" ? "warning" : "success");
      } else {
        notify(result.rolledBack ? "Mihomo 发布失败，已自动回滚" : "Mihomo 配置已发布并完成健康验证", result.rolledBack ? "warning" : "success");
      }
      setConfirm(null);
      setPreviewToken("");
      setSnapshots(await listMihomoSnapshots());
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
    setBusy(true);
    try {
      const result = await restoreMihomoSnapshot(confirm.snapshotId, confirm.token, "FoxOS 控制台恢复");
      if (result.job) {
        const job = await waitForJob(result.job.id);
        if (job.status === "FAILED") throw new Error(job.errorMessage || "快照恢复失败");
        notify(job.status === "ROLLED_BACK" ? "快照恢复失败，已自动回滚" : "Mihomo 快照已恢复", job.status === "ROLLED_BACK" ? "warning" : "success");
      } else {
        notify(result.rolledBack ? "快照恢复失败，已自动回滚" : "Mihomo 快照已恢复", result.rolledBack ? "warning" : "success");
      }
      setConfirm(null);
      await load();
    } catch (error) {
      notify(errorMessage(error, "快照恢复失败"), "warning");
    } finally {
      setBusy(false);
    }
  };
  return (
    <section className="panel operations-panel">
      <div className="panel-heading"><div><h2>Mihomo 配置发布</h2><p>草稿、脱敏 Diff、快照和任务状态</p></div><OperationBadge tone={loading ? "warning" : "ok"}>{loading ? "加载中" : "SQLite 草稿"}</OperationBadge></div>
      <div className="operation-form-grid">
        <label className="field"><span>运行模式</span><select value={draft.mode} onChange={(event) => setDraft((value) => ({ ...value, mode: event.target.value }))}><option value="rule">rule</option><option value="global">global</option><option value="direct">direct</option></select></label>
        <label className="field"><span>Mixed Port</span><input min={1} max={65535} type="number" value={draft.mixedPort || ""} onChange={(event) => setDraft((value) => ({ ...value, mixedPort: Number(event.target.value) }))} /></label>
        <label className="field operation-check"><input checked={draft.allowLan} type="checkbox" onChange={(event) => setDraft((value) => ({ ...value, allowLan: event.target.checked }))} /><span>允许局域网访问</span></label>
        <label className="field operation-wide"><span>额外规则（每行一条）</span><textarea rows={4} value={draft.rules.join("\n")} onChange={(event) => setDraft((value) => ({ ...value, rules: event.target.value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean) }))} /></label>
      </div>
      <div className="operation-actions">
        <button className="button secondary" disabled={busy || loading} onClick={() => void save()} type="button"><Save aria-hidden="true" size={16} />保存草稿</button>
        <button className="button primary" disabled={busy || loading} onClick={() => void previewConfig()} type="button"><Eye aria-hidden="true" size={16} />生成预览</button>
        <button className="button danger" disabled={busy || !previewToken} onClick={() => preview && setConfirm({ title: "确认发布 Mihomo 配置", description: "配置将原子替换、热重载并执行健康验证；失败会自动回滚。", impacts: ["SHA-256：" + preview.digest, "变更行数：" + preview.diff.split("\n").filter(Boolean).length, "Mihomo 运行配置和快照目录"], warnings: ["发布令牌只允许使用一次，预览变化后必须重新生成"], token: previewToken })} type="button"><Play aria-hidden="true" size={16} />确认发布</button>
      </div>
      {preview ? <div className="operation-preview" aria-live="polite"><div className="preview-heading"><span><FileDiff aria-hidden="true" size={16} />预览摘要</span><code>{preview.digest.slice(0, 16)}…</code></div><pre>{preview.yaml}</pre><details><summary>查看 Diff</summary><pre>{preview.diff || "没有配置变化"}</pre></details>{preview.hasSecret ? <p className="warning-note"><ShieldAlert aria-hidden="true" size={16} />预览已隐藏节点凭据；真实凭据只在本地运行配置中使用。</p> : null}</div> : <div className="operation-empty">保存草稿后生成预览，发布按钮会在获得一次性确认令牌后启用。</div>}
      <div className="snapshot-list"><div className="subheading"><h3>快照</h3><button aria-label="刷新 Mihomo 快照" className="icon-button" disabled={busy} onClick={() => void load()} title="刷新快照" type="button"><RefreshCw size={16} /></button></div>{snapshots.length ? snapshots.map((snapshot) => <div className="snapshot-row" key={snapshot.id}><div><strong>{snapshot.label || "未命名快照"}</strong><small>{snapshot.createdAt} · {snapshot.digest.slice(0, 12)}…</small></div><button className="button secondary compact" disabled={busy} onClick={() => void prepareRestore(snapshot)} type="button"><RotateCcw aria-hidden="true" size={14} />恢复</button></div>) : <span className="muted-text">暂无快照</span>}</div>
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
  const [confirm, setConfirm] = useState<{ action: "update" | "delete"; item: Subscription; token: string; plan?: SubscriptionUpdatePlan; nodeCount: number; warnings: string[] } | null>(null);
  const load = async () => {
    try { setItems(await listSubscriptions()); } catch (error) { notify(errorMessage(error, "订阅源不可用"), "warning"); }
  };
  useEffect(() => { void load(); }, []);
  const add = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    try {
      await createSubscription({ name, url, enabled: true, interval: 21600 });
      setName("");
      setURL("");
      await load();
      notify("订阅源已保存；尚未抓取节点");
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
      setConfirm({ action: "update", item, token: result.confirmationToken, plan: result.plan, nodeCount: result.nodeCount, warnings: ["预览令牌只能使用一次；远端内容变化后任务会失败并保留旧节点"] });
    } catch (error) {
      notify(errorMessage(error, "订阅更新计划失败，旧节点保持不变"), "warning");
    } finally { setBusy(false); }
  };
  const prepareDelete = async (item: Subscription) => {
    setBusy(true);
    try {
      const result = await planSubscriptionDelete(item.id);
      setConfirm({ action: "delete", item, token: result.confirmationToken, nodeCount: result.plan.nodeCount, warnings: result.warnings });
    } catch (error) {
      notify(errorMessage(error, "订阅删除计划失败"), "warning");
    } finally { setBusy(false); }
  };
  const update = async () => {
    if (!confirm?.plan) return;
    setBusy(true);
    try {
      const result = await updateSubscription(confirm.item.id, confirm.plan, confirm.token);
      if (result.job) {
        const job = await waitForJob(result.job.id);
        if (job.status !== "SUCCEEDED") throw new Error(job.errorMessage || "订阅更新失败，旧节点保持不变");
      }
      notify("订阅已更新，当前包含 " + confirm.plan.nodeCount + " 个去重节点");
      setConfirm(null);
      await load();
    } catch (error) {
      notify(errorMessage(error, "订阅更新失败，旧节点保持不变"), "warning");
    } finally { setBusy(false); }
  };
  const remove = async () => {
    if (!confirm) return;
    setBusy(true);
    try {
      const result = await deleteSubscription(confirm.item.id, confirm.token);
      notify(`订阅源及 ${result.nodeCount} 个未被引用的来源节点已删除`);
      setConfirm(null);
      setSelected(null);
      await load();
    } catch (error) {
      notify(errorMessage(error, "订阅删除失败"), "warning");
    } finally { setBusy(false); }
  };
  return (
    <section className="panel operations-panel">
      <div className="panel-heading"><div><h2>订阅源</h2><p>受限抓取、预览、去重前的人工确认</p></div><Upload aria-hidden="true" className="green-text" size={20} /></div>
      <form className="operation-inline-form" onSubmit={add}><label className="field"><span>名称</span><input required value={name} onChange={(event) => setName(event.target.value)} /></label><label className="field operation-grow"><span>HTTPS URL</span><input required inputMode="url" placeholder="https://…" value={url} onChange={(event) => setURL(event.target.value)} /></label><button className="button primary" disabled={busy} type="submit">添加</button></form>
      <div className="subscription-list">{items.length ? items.map((item) => <div className="subscription-row" key={item.id}><div><strong>{item.name}</strong><small>{item.url}</small><small>{item.lastError ? "最近失败：" + item.lastError : item.lastSuccessAt ? "最近成功：" + item.lastSuccessAt : "尚未成功更新"}</small></div><div className="row-actions"><button className="button secondary compact" disabled={busy} onClick={() => void inspect(item)} type="button"><Eye size={14} />预览</button><button className="button primary compact" disabled={busy} onClick={() => void prepareUpdate(item)} type="button"><RefreshCw size={14} />更新</button><button aria-label={"删除订阅 " + item.name} className="icon-button danger-icon" disabled={busy} onClick={() => void prepareDelete(item)} title="删除订阅" type="button"><Trash2 size={15} /></button></div></div>) : <div className="operation-empty">暂无订阅源</div>}</div>
      {selected && preview ? <div className="operation-callout" role="status"><strong>{selected.name} 预览</strong><span>{preview.nodeCount} 个节点 · {preview.digest.slice(0, 16)}…</span></div> : null}
      {confirm ? <ConfirmDialog busy={busy} confirmLabel={confirm.action === "update" ? "确认更新订阅" : "确认删除订阅"} description={confirm.action === "update" ? "任务会重新抓取并核对预览摘要，再原子替换该来源的节点。" : "订阅定义和未被引用的来源节点将作为一个事务删除。"} impacts={["订阅：" + confirm.item.name, "地址：" + confirm.item.url, confirm.action === "update" && confirm.plan ? `新增 ${confirm.plan.addCount}，更新 ${confirm.plan.updateCount}，移除 ${confirm.plan.removeCount}` : `删除范围：${confirm.nodeCount} 个来源节点`]} onCancel={() => !busy && setConfirm(null)} onConfirm={() => void (confirm.action === "update" ? update() : remove())} title={confirm.action === "update" ? "确认更新订阅" : "确认删除订阅"} warnings={confirm.warnings} /> : null}
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
    setBusy(true);
    try {
      const result = await restoreBackup(confirmBackup.item.id, confirmBackup.token);
      if (result.job) {
        const job = await waitForJob(result.job.id);
        if (job.status !== "SUCCEEDED") throw new Error(job.errorMessage || "备份恢复失败");
      }
      notify(confirmBackup.item.mihomo ? "备份已恢复，并完成 SQLite 完整性与 Mihomo 健康验证" : "SQLite 管理状态已恢复并完成完整性验证；未改动 Mihomo 配置");
      setConfirmBackup(null);
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
