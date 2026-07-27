import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { beforeEach, describe, expect, it } from "vitest";
import { saveApiToken } from "../api";
import { MihomoOperations, SubscriptionOperations } from "../components/OperationsPanels";
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
});

describe("Subscription operations panel", () => {
  beforeEach(() => saveApiToken("s".repeat(40)));

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
});
