import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { saveApiToken } from "../api";
import { AlertBackupOperations, MihomoOperations, SubscriptionOperations } from "../components/OperationsPanels";
import { server } from "./setup";

describe("Mihomo operations panel", () => {
  beforeEach(() => {
    saveApiToken("o".repeat(40));
    server.use(
      http.get("/api/v1/mihomo/draft", () => HttpResponse.json({ mode: "rule", mixedPort: 7890, allowLan: false, rules: ["MATCH,DIRECT"] })),
      http.get("/api/v1/mihomo/snapshots", () => HttpResponse.json([])),
      http.post("/api/v1/mihomo/config/preview", () => HttpResponse.json({ preview: { draft: { mode: "rule", mixedPort: 7890, allowLan: false, rules: ["MATCH,DIRECT"] }, digest: "a".repeat(64), yaml: "mode: rule", diff: "+mode", hasSecret: false }, confirmationToken: "confirm-token", expiresInSeconds: 300 })),
    );
  });

  it("requires preview and explicit acknowledgement before publishing", async () => {
    let publishCalls = 0;
    server.use(http.post("/api/v1/mihomo/config/apply", () => { publishCalls += 1; return HttpResponse.json({ status: "SUCCEEDED", snapshotId: "snapshot-1" }); }));
    render(<MihomoOperations notify={() => undefined} />);
    await screen.findByText("Mihomo 配置发布");
    fireEvent.click(screen.getByRole("button", { name: "生成预览" }));
    await screen.findByText("预览摘要");
    fireEvent.click(screen.getByRole("button", { name: "确认发布" }));
    expect(screen.getByRole("button", { name: "确认执行" })).toBeDisabled();
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("checkbox"));
    fireEvent.click(screen.getByRole("button", { name: "确认执行" }));
    await waitFor(() => expect(publishCalls).toBe(1));
  });

	it("reports a successful publish separately when snapshot readback fails", async () => {
		let publishCalls = 0;
		let snapshotReads = 0;
		const notify = vi.fn();
		server.use(
			http.get("/api/v1/mihomo/snapshots", () => {
				snapshotReads += 1;
				if (snapshotReads > 1) return HttpResponse.json({ error: "snapshots_unavailable", message: "快照列表回读失败" }, { status: 503 });
				return HttpResponse.json([]);
			}),
			http.post("/api/v1/mihomo/config/apply", () => {
				publishCalls += 1;
				return HttpResponse.json({ status: "SUCCEEDED", snapshotId: "snapshot-created" });
			}),
		);
		render(<MihomoOperations notify={notify} />);
		await screen.findByText("SQLite 草稿", { selector: ".operation-badge" });
		fireEvent.click(screen.getByRole("button", { name: "生成预览" }));
		await screen.findByText("预览摘要");
		fireEvent.click(screen.getByRole("button", { name: "确认发布" }));
		const dialog = screen.getByRole("dialog", { name: "确认发布 Mihomo 配置" });
		fireEvent.click(within(dialog).getByRole("checkbox"));
		fireEvent.click(within(dialog).getByRole("button", { name: "确认执行" }));

		await waitFor(() => expect(publishCalls).toBe(1));
		await waitFor(() => expect(notify).toHaveBeenLastCalledWith(expect.stringContaining("Mihomo 配置已发布"), "warning"));
		expect(String(notify.mock.calls.at(-1)?.[0])).toContain("快照列表回读未确认");
	});

	it("invalidates the preview and confirmation token when the draft changes", async () => {
		render(<MihomoOperations notify={() => undefined} />);
		await screen.findByText("Mihomo 配置发布");
		fireEvent.click(screen.getByRole("button", { name: "生成预览" }));
		expect(await screen.findByText("预览摘要")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "确认发布" })).toBeEnabled();

		fireEvent.change(screen.getByRole("spinbutton", { name: "Mixed Port" }), { target: { value: "7891" } });
		expect(screen.queryByText("预览摘要")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "确认发布" })).toBeDisabled();
	});

  it("discards a failed publish confirmation before reporting the error", async () => {
    let previewCalls = 0;
    let publishCalls = 0;
    server.use(
      http.post("/api/v1/mihomo/config/preview", () => {
        previewCalls += 1;
        return HttpResponse.json({ preview: { draft: { mode: "rule", mixedPort: 7890, allowLan: false, rules: ["MATCH,DIRECT"] }, digest: "a".repeat(64), yaml: "mode: rule", diff: "+mode", hasSecret: false }, confirmationToken: `confirm-${previewCalls}`, expiresInSeconds: 300 });
      }),
      http.post("/api/v1/mihomo/config/apply", () => {
        publishCalls += 1;
        return HttpResponse.json({ error: "confirmation_stale", message: "发布前态已变化" }, { status: 409 });
      }),
    );
    render(<MihomoOperations notify={() => undefined} />);
    await screen.findByText("Mihomo 配置发布");
    fireEvent.click(screen.getByRole("button", { name: "生成预览" }));
    await screen.findByText("预览摘要");
    fireEvent.click(screen.getByRole("button", { name: "确认发布" }));
    const dialog = screen.getByRole("dialog", { name: "确认发布 Mihomo 配置" });
    fireEvent.click(within(dialog).getByRole("checkbox"));
    fireEvent.click(within(dialog).getByRole("button", { name: "确认执行" }));

    await waitFor(() => expect(publishCalls).toBe(1));
    expect(screen.queryByRole("dialog", { name: "确认发布 Mihomo 配置" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "确认发布" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "生成预览" }));
    await waitFor(() => expect(previewCalls).toBe(2));
    expect(screen.getByRole("button", { name: "确认发布" })).toBeEnabled();
  });

  it("discards a failed snapshot restore confirmation", async () => {
    let planCalls = 0;
    let restoreCalls = 0;
    server.use(
      http.get("/api/v1/mihomo/snapshots", () => HttpResponse.json([{ id: "snapshot-a", digest: "b".repeat(64), label: "before", createdAt: "2026-07-27T00:00:00Z" }])),
      http.post("/api/v1/mihomo/snapshots/snapshot-a/restore/plan", () => {
        planCalls += 1;
        return HttpResponse.json({ plan: { action: "mihomo.restore", snapshotId: "snapshot-a" }, confirmationToken: `restore-${planCalls}`, expiresInSeconds: 300 });
      }),
      http.post("/api/v1/mihomo/snapshots/snapshot-a/restore", () => {
        restoreCalls += 1;
        return HttpResponse.json({ error: "restore_failed", message: "快照恢复失败" }, { status: 409 });
      }),
    );
    render(<MihomoOperations notify={() => undefined} />);
    const restoreButton = await screen.findByRole("button", { name: "恢复" });
    fireEvent.click(restoreButton);
    const dialog = await screen.findByRole("dialog", { name: "确认恢复 Mihomo 快照" });
    fireEvent.click(within(dialog).getByRole("checkbox"));
    fireEvent.click(within(dialog).getByRole("button", { name: "确认执行" }));

    await waitFor(() => expect(restoreCalls).toBe(1));
    expect(screen.queryByRole("dialog", { name: "确认恢复 Mihomo 快照" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "恢复" }));
    await waitFor(() => expect(planCalls).toBe(2));
  });

	it("keeps the editor closed after a 503 until a retry succeeds", async () => {
		let draftReads = 0;
		let previewCalls = 0;
		server.use(
			http.get("/api/v1/mihomo/draft", () => {
				draftReads += 1;
				if (draftReads === 1) return HttpResponse.json({ error: "mihomo_unavailable", message: "Mihomo Controller 未配置" }, { status: 503 });
				return HttpResponse.json({ mode: "rule", mixedPort: 7890, allowLan: false, rules: ["MATCH,DIRECT"] });
			}),
			http.post("/api/v1/mihomo/config/preview", () => {
				previewCalls += 1;
				return HttpResponse.json({});
			}),
		);
		render(<MihomoOperations notify={() => undefined} />);

		const alert = await screen.findByRole("alert");
		expect(alert).toHaveTextContent("Mihomo 配置服务不可用");
		expect(screen.getByText("不可用", { selector: ".operation-badge" })).toBeInTheDocument();
		expect(screen.getByRole("combobox", { name: "运行模式" })).toBeDisabled();
		expect(screen.getByRole("spinbutton", { name: "Mixed Port" })).toBeDisabled();
		expect(screen.getByRole("checkbox", { name: "允许局域网访问" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "保存草稿" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "生成预览" })).toBeDisabled();
		expect(previewCalls).toBe(0);

		fireEvent.click(screen.getByRole("button", { name: "重试加载" }));
		await screen.findByText("SQLite 草稿", { selector: ".operation-badge" });
		await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
		expect(screen.getByRole("checkbox", { name: "允许局域网访问" })).toBeEnabled();
		expect(screen.getByRole("button", { name: "保存草稿" })).toBeEnabled();
		expect(screen.getByRole("button", { name: "生成预览" })).toBeEnabled();
		expect(draftReads).toBe(2);
	});
});

describe("Subscription operations panel", () => {
  beforeEach(() => saveApiToken("s".repeat(40)));

  const subscription = { id: "source-a", name: "Primary", url: "https://example.invalid/redacted", enabled: true, interval: 3600 };

	it("marks a created subscription pending when the list readback fails", async () => {
		let reads = 0;
		const notify = vi.fn();
		server.use(
			http.get("/api/v1/subscriptions", () => {
				reads += 1;
				if (reads === 2) return HttpResponse.json({ error: "readback_unavailable", message: "列表回读失败" }, { status: 503 });
				return HttpResponse.json([]);
			}),
			http.post("/api/v1/subscriptions", async ({ request }) => HttpResponse.json({ id: "source-created", ...(await request.json() as object) }, { status: 201 })),
		);
		render(<SubscriptionOperations notify={notify} />);
		await waitFor(() => expect(reads).toBe(1));
		fireEvent.change(screen.getByRole("textbox", { name: "名称" }), { target: { value: "Created source" } });
		fireEvent.change(screen.getByRole("textbox", { name: "HTTPS URL" }), { target: { value: "https://example.invalid/new" } });
		fireEvent.click(screen.getByRole("button", { name: "添加" }));

		expect(await screen.findByText("Created source", { exact: true })).toBeInTheDocument();
		expect(screen.getByText("等待列表回读确认")).toBeInTheDocument();
		expect(notify).toHaveBeenCalledWith("列表回读失败", "warning");
			expect(notify).not.toHaveBeenCalledWith(expect.stringContaining("已保存"));
		});

	it("marks a created subscription pending when a successful readback omits it", async () => {
		const notify = vi.fn();
		server.use(
			http.get("/api/v1/subscriptions", () => HttpResponse.json([])),
			http.post("/api/v1/subscriptions", async ({ request }) => HttpResponse.json({ id: "source-missing", ...(await request.json() as object) }, { status: 201 })),
		);
		render(<SubscriptionOperations notify={notify} />);
		await screen.findByText("暂无订阅源");
		fireEvent.change(screen.getByRole("textbox", { name: "名称" }), { target: { value: "Missing source" } });
		fireEvent.change(screen.getByRole("textbox", { name: "HTTPS URL" }), { target: { value: "https://example.invalid/missing" } });
		fireEvent.click(screen.getByRole("button", { name: "添加" }));

		expect(await screen.findByText("Missing source", { exact: true })).toBeInTheDocument();
		expect(screen.getByText("等待列表回读确认")).toBeInTheDocument();
		expect(notify).toHaveBeenCalledWith(expect.stringContaining("列表回读未包含新订阅"), "warning");
		expect(notify).not.toHaveBeenCalledWith(expect.stringContaining("已保存"));
	});

	it("keeps a successful update distinct from a failed list readback", async () => {
		let reads = 0;
		let updateCalls = 0;
		const notify = vi.fn();
		const plan = { action: "subscription.update", subscriptionId: "source-a", digest: "a".repeat(64), nodeIds: ["node-a"], removedNodeIds: [], existingCount: 1, nodeCount: 1, addCount: 0, updateCount: 1, removeCount: 0, parseValidCount: 1, parseSkippedCount: 0, parseErrors: [], suspiciousReduction: false };
		server.use(
			http.get("/api/v1/subscriptions", () => {
				reads += 1;
				if (reads > 1) return HttpResponse.json({ error: "subscriptions_failed", message: "订阅列表回读失败" }, { status: 503 });
				return HttpResponse.json([subscription]);
			}),
			http.post("/api/v1/subscriptions/source-a/preview", () => HttpResponse.json({ digest: plan.digest, nodeCount: 1, nodes: [], plan, confirmationToken: "update-readback", expiresInSeconds: 300 })),
			http.post("/api/v1/subscriptions/source-a/update", () => {
				updateCalls += 1;
				return HttpResponse.json({ status: "SUCCEEDED", digest: plan.digest, nodeCount: 1 });
			}),
		);
		render(<SubscriptionOperations notify={notify} />);
		fireEvent.click(await screen.findByRole("button", { name: "更新" }));
		const dialog = await screen.findByRole("dialog", { name: "确认更新订阅" });
		fireEvent.click(within(dialog).getByRole("checkbox"));
		fireEvent.click(within(dialog).getByRole("button", { name: "确认更新订阅" }));

		await waitFor(() => expect(updateCalls).toBe(1));
		expect(await screen.findByText("等待列表回读确认")).toBeInTheDocument();
		expect(notify).toHaveBeenLastCalledWith(expect.stringContaining("订阅已更新"), "warning");
		expect(String(notify.mock.calls.at(-1)?.[0])).toContain("列表回读未确认");
	});

	it("removes a confirmed deletion locally when its list readback fails", async () => {
		let reads = 0;
		let deleteCalls = 0;
		const notify = vi.fn();
		server.use(
			http.get("/api/v1/subscriptions", () => {
				reads += 1;
				if (reads > 1) return HttpResponse.json({ error: "subscriptions_failed", message: "订阅列表回读失败" }, { status: 503 });
				return HttpResponse.json([subscription]);
			}),
			http.post("/api/v1/subscriptions/source-a/delete/plan", () => HttpResponse.json({ plan: { action: "subscription.delete", strategy: "detach", subscriptionId: "source-a", nodeIds: ["node-a"], nodeCount: 1 }, confirmationToken: "delete-readback", expiresInSeconds: 300, warnings: [] })),
			http.post("/api/v1/subscriptions/source-a/delete", () => {
				deleteCalls += 1;
				return HttpResponse.json({ status: "SUCCEEDED", strategy: "detach", subscriptionId: "source-a", nodeCount: 1 });
			}),
		);
		render(<SubscriptionOperations notify={notify} />);
		fireEvent.click(await screen.findByRole("button", { name: "删除订阅 Primary" }));
		fireEvent.click(screen.getByRole("button", { name: "生成删除计划" }));
		const dialog = await screen.findByRole("dialog", { name: "确认删除订阅" });
		fireEvent.click(within(dialog).getByRole("checkbox"));
		fireEvent.click(within(dialog).getByRole("button", { name: "确认删除订阅" }));

		await waitFor(() => expect(deleteCalls).toBe(1));
		await waitFor(() => expect(screen.queryByRole("button", { name: "删除订阅 Primary" })).not.toBeInTheDocument());
		expect(notify).toHaveBeenLastCalledWith(expect.stringContaining("订阅源已删除"), "warning");
		expect(String(notify.mock.calls.at(-1)?.[0])).toContain("列表回读未确认");
	});

  it("toggles only enabled state and uses the server readback", async () => {
    let body: unknown;
    server.use(
      http.get("/api/v1/subscriptions", () => HttpResponse.json([{ id: "source-a", name: "Primary", url: "https://example.invalid/redacted", enabled: true, interval: 3600 }])),
      http.patch("/api/v1/subscriptions/source-a/enabled", async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ id: "source-a", name: "Primary", url: "https://example.invalid/redacted", enabled: false, interval: 3600 });
      }),
    );
    render(<SubscriptionOperations notify={() => undefined} />);
    const toggle = await screen.findByRole("checkbox", { name: "停用订阅 Primary" });
    fireEvent.click(toggle);
    await waitFor(() => expect(body).toEqual({ enabled: false }));
    expect(await screen.findByRole("checkbox", { name: "启用订阅 Primary" })).not.toBeChecked();
  });

  for (const strategy of ["detach", "cascade"] as const) {
    it(`plans and confirms the ${strategy} deletion strategy`, async () => {
      let planBody: unknown;
      let deleteBody: unknown;
      let deleteCalls = 0;
      server.use(
        http.get("/api/v1/subscriptions", () => HttpResponse.json([subscription])),
        http.post("/api/v1/subscriptions/source-a/delete/plan", async ({ request }) => {
          planBody = await request.json();
          return HttpResponse.json({
            plan: { action: "subscription.delete", strategy, subscriptionId: "source-a", nodeIds: ["node-a", "node-b"], nodeCount: 2 },
            confirmationToken: `${strategy}-confirmation`,
            expiresInSeconds: 300,
            warnings: ["计划只允许执行一次"],
          });
        }),
        http.post("/api/v1/subscriptions/source-a/delete", async ({ request }) => {
          deleteCalls += 1;
          deleteBody = await request.json();
          return HttpResponse.json({ status: "SUCCEEDED", strategy, subscriptionId: "source-a", nodeCount: 2 });
        }),
      );

      render(<SubscriptionOperations notify={() => undefined} />);
      fireEvent.click(await screen.findByRole("button", { name: "删除订阅 Primary" }));
      const strategyDialog = screen.getByRole("dialog", { name: "选择节点处理方式" });
      fireEvent.click(within(strategyDialog).getByRole("radio", { name: strategy === "detach" ? /保留为手工节点/ : /级联删除来源节点/ }));
      fireEvent.click(within(strategyDialog).getByRole("button", { name: "生成删除计划" }));

      const confirmDialog = await screen.findByRole("dialog", { name: "确认删除订阅" });
      expect(planBody).toEqual({ strategy });
      expect(deleteCalls).toBe(0);
      expect(within(confirmDialog).getByRole("button", { name: "确认删除订阅" })).toBeDisabled();

      fireEvent.click(within(confirmDialog).getByRole("checkbox"));
      fireEvent.click(within(confirmDialog).getByRole("button", { name: "确认删除订阅" }));
      await waitFor(() => expect(deleteCalls).toBe(1));
      expect(deleteBody).toEqual({ strategy, confirmationToken: `${strategy}-confirmation` });
    });
  }

  it("blocks duplicate deletion submissions while execution is pending", async () => {
    let deleteCalls = 0;
    let releaseDelete: (() => void) | undefined;
    server.use(
      http.get("/api/v1/subscriptions", () => HttpResponse.json(deleteCalls ? [] : [subscription])),
      http.post("/api/v1/subscriptions/source-a/delete/plan", () => HttpResponse.json({
        plan: { action: "subscription.delete", strategy: "detach", subscriptionId: "source-a", nodeIds: ["node-a"], nodeCount: 1 },
        confirmationToken: "detach-confirmation",
        expiresInSeconds: 300,
        warnings: [],
      })),
      http.post("/api/v1/subscriptions/source-a/delete", async () => {
        deleteCalls += 1;
        await new Promise<void>((resolve) => { releaseDelete = resolve; });
        return HttpResponse.json({ status: "SUCCEEDED", strategy: "detach", subscriptionId: "source-a", nodeCount: 1 });
      }),
    );

    render(<SubscriptionOperations notify={() => undefined} />);
    fireEvent.click(await screen.findByRole("button", { name: "删除订阅 Primary" }));
    fireEvent.click(screen.getByRole("button", { name: "生成删除计划" }));
    const confirmDialog = await screen.findByRole("dialog", { name: "确认删除订阅" });
    fireEvent.click(within(confirmDialog).getByRole("checkbox"));
    const confirmButton = within(confirmDialog).getByRole("button", { name: "确认删除订阅" });
    fireEvent.click(confirmButton);
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "确认删除订阅" })).not.toBeInTheDocument());
    expect(screen.getByRole("button", { name: "删除订阅 Primary" })).toBeDisabled();
    expect(deleteCalls).toBe(1);
    releaseDelete?.();
    await waitFor(() => expect(screen.queryByRole("button", { name: "删除订阅 Primary" })).not.toBeInTheDocument());
  });

  it("requires a new update preview after a failed job", async () => {
    let previewCalls = 0;
    let updateCalls = 0;
    server.use(
      http.get("/api/v1/subscriptions", () => HttpResponse.json([subscription])),
      http.post("/api/v1/subscriptions/source-a/preview", () => {
        previewCalls += 1;
        return HttpResponse.json({
          digest: "a".repeat(64),
          nodeCount: 1,
          nodes: [],
          plan: { action: "subscription.update", subscriptionId: "source-a", digest: "a".repeat(64), nodeIds: ["node-a"], removedNodeIds: [], existingCount: 1, nodeCount: 1, addCount: 0, updateCount: 1, removeCount: 0, parseValidCount: 1, parseSkippedCount: 0, parseErrors: [], suspiciousReduction: false },
          confirmationToken: `update-${previewCalls}`,
          expiresInSeconds: 300,
        });
      }),
      http.post("/api/v1/subscriptions/source-a/update", () => {
        updateCalls += 1;
        return HttpResponse.json({ status: "QUEUED", job: { id: "job-update", kind: "subscription.update", status: "QUEUED", progress: 0, attempts: 0, createdAt: "2026-07-27T00:00:00Z", updatedAt: "2026-07-27T00:00:00Z" } }, { status: 202 });
      }),
      http.get("/api/v1/jobs/job-update", () => HttpResponse.json({ id: "job-update", kind: "subscription.update", status: "FAILED", progress: 100, attempts: 1, errorMessage: "来源已变化", createdAt: "2026-07-27T00:00:00Z", updatedAt: "2026-07-27T00:00:01Z" })),
    );
    render(<SubscriptionOperations notify={() => undefined} />);
    fireEvent.click(await screen.findByRole("button", { name: "更新" }));
    let dialog = await screen.findByRole("dialog", { name: "确认更新订阅" });
    fireEvent.click(within(dialog).getByRole("checkbox"));
    fireEvent.click(within(dialog).getByRole("button", { name: "确认更新订阅" }));

    await waitFor(() => expect(updateCalls).toBe(1));
    expect(screen.queryByRole("dialog", { name: "确认更新订阅" })).not.toBeInTheDocument();
    expect(screen.queryByText("Primary 预览")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "更新" }));
    dialog = await screen.findByRole("dialog", { name: "确认更新订阅" });
    expect(dialog).toBeInTheDocument();
    expect(previewCalls).toBe(2);
  });

  it("requires a new delete plan after a conflict", async () => {
    let planCalls = 0;
    let deleteCalls = 0;
    server.use(
      http.get("/api/v1/subscriptions", () => HttpResponse.json([subscription])),
      http.post("/api/v1/subscriptions/source-a/delete/plan", () => {
        planCalls += 1;
        return HttpResponse.json({ plan: { action: "subscription.delete", strategy: "cascade", subscriptionId: "source-a", nodeIds: ["node-a"], nodeCount: 1 }, confirmationToken: `delete-${planCalls}`, expiresInSeconds: 300, warnings: [] });
      }),
      http.post("/api/v1/subscriptions/source-a/delete", () => {
        deleteCalls += 1;
        return HttpResponse.json({ error: "subscription_nodes_referenced", message: "节点仍被引用" }, { status: 409 });
      }),
    );
    render(<SubscriptionOperations notify={() => undefined} />);
    fireEvent.click(await screen.findByRole("button", { name: "删除订阅 Primary" }));
    let strategyDialog = screen.getByRole("dialog", { name: "选择节点处理方式" });
    fireEvent.click(within(strategyDialog).getByRole("radio", { name: /级联删除来源节点/ }));
    fireEvent.click(within(strategyDialog).getByRole("button", { name: "生成删除计划" }));
    const confirmation = await screen.findByRole("dialog", { name: "确认删除订阅" });
    fireEvent.click(within(confirmation).getByRole("checkbox"));
    fireEvent.click(within(confirmation).getByRole("button", { name: "确认删除订阅" }));

    await waitFor(() => expect(deleteCalls).toBe(1));
    expect(screen.queryByRole("dialog", { name: "确认删除订阅" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "删除订阅 Primary" }));
    strategyDialog = screen.getByRole("dialog", { name: "选择节点处理方式" });
    fireEvent.click(within(strategyDialog).getByRole("radio", { name: /级联删除来源节点/ }));
    fireEvent.click(within(strategyDialog).getByRole("button", { name: "生成删除计划" }));
    await screen.findByRole("dialog", { name: "确认删除订阅" });
    expect(planCalls).toBe(2);
  });
});

describe("Backup restore operations", () => {
	beforeEach(() => saveApiToken("b".repeat(40)));

	it("discards a failed restore confirmation before allowing another plan", async () => {
		let planCalls = 0;
		let restoreCalls = 0;
		server.use(
			http.get("/api/v1/alerts", () => HttpResponse.json([])),
			http.get("/api/v1/backups", () => HttpResponse.json([{ id: "backup-a", label: "before", createdAt: "2026-07-27T00:00:00Z", database: "foxos.db", fileCount: 1, checksums: { "foxos.db": "a".repeat(64) } }])),
			http.post("/api/v1/backups/backup-a/restore/plan", () => {
				planCalls += 1;
				return HttpResponse.json({ plan: { action: "backup.restore", backupId: "backup-a", digest: "a".repeat(64), fileCount: 1, mihomo: false }, confirmationToken: `backup-${planCalls}`, expiresInSeconds: 300, warnings: [] });
			}),
			http.post("/api/v1/backups/backup-a/restore", () => {
				restoreCalls += 1;
				return HttpResponse.json({ error: "restore_failed", message: "备份恢复失败" }, { status: 409 });
			}),
		);
		render(<AlertBackupOperations notify={vi.fn()} />);
		fireEvent.click(await screen.findByRole("button", { name: "恢复" }));
		let dialog = await screen.findByRole("dialog", { name: "确认恢复备份" });
		fireEvent.click(within(dialog).getByRole("checkbox"));
		fireEvent.click(within(dialog).getByRole("button", { name: "确认恢复备份" }));
		await waitFor(() => expect(restoreCalls).toBe(1));
		expect(screen.queryByRole("dialog", { name: "确认恢复备份" })).not.toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "恢复" }));
		dialog = await screen.findByRole("dialog", { name: "确认恢复备份" });
		expect(dialog).toBeInTheDocument();
		expect(planCalls).toBe(2);
	});
});
