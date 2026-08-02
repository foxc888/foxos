import { describe, expect, it } from "vitest";
import { expireResource, initialResource, mergeResult, phaseLabel } from "../live-state";

describe("resource state transitions", () => {
  it.each([
    ["loading", "加载中"],
    ["live", "实时"],
    ["stale", "已过期"],
    ["unavailable", "不可用"],
  ] as const)("labels %s", (phase, label) => {
    expect(phaseLabel(phase)).toBe(label);
  });

  it("retains a last known value as stale after a failed refresh", () => {
    const initial = initialResource<{ online: boolean }>("test source");
    const live = mergeResult(initial, { ok: true, data: { online: true }, loadedAt: "2026-07-27T00:00:00.000Z" });
    const stale = mergeResult(live, { ok: false, error: "timeout", loadedAt: "2026-07-27T00:01:00.000Z" });
    expect(stale.phase).toBe("stale");
    expect(stale.data).toEqual({ online: true });
    expect(stale.updatedAt).toBe(live.updatedAt);
  });

  it("does not turn an uninitialised resource into a false online state", () => {
    const initial = initialResource<{ online: boolean }>("test source");
    const unavailable = mergeResult(initial, { ok: false, error: "not configured", loadedAt: "2026-07-27T00:00:00.000Z" });
    expect(unavailable.phase).toBe("unavailable");
    expect(unavailable.data).toBeUndefined();
  });

  it("expires live data after the freshness window", () => {
    const live = mergeResult(initialResource("test source"), { ok: true, data: 1, loadedAt: "2026-07-27T00:00:00.000Z" });
    expect(expireResource(live, Date.parse("2026-07-27T00:03:00.000Z"), 120_000).phase).toBe("stale");
    expect(expireResource(live, Date.parse("2026-07-27T00:01:00.000Z"), 120_000).phase).toBe("live");
  });
});
