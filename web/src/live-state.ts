import type { LoadResult } from "./api";

export type ResourcePhase = "loading" | "live" | "stale" | "unavailable";

export type ResourceState<T> = {
  phase: ResourcePhase;
  source: string;
  data?: T;
  updatedAt?: string;
  error?: string;
};

export function initialResource<T>(source: string): ResourceState<T> {
  return { phase: "loading", source };
}

export function markLoading<T>(state: ResourceState<T>): ResourceState<T> {
  return { ...state, phase: "loading", error: undefined };
}

export function mergeResult<T>(state: ResourceState<T>, result: LoadResult<T>): ResourceState<T> {
  if (result.ok) {
    return {
      phase: "live",
      source: state.source,
      data: result.data,
      updatedAt: result.loadedAt,
    };
  }
  if (state.data !== undefined) {
    return { ...state, phase: "stale", error: result.error };
  }
  return {
    phase: "unavailable",
    source: state.source,
    error: result.error,
  };
}

export function expireResource<T>(state: ResourceState<T>, now = Date.now(), maxAgeMs = 120_000): ResourceState<T> {
  if (state.phase !== "live" || !state.updatedAt) return state;
  const updated = Date.parse(state.updatedAt);
  if (!Number.isFinite(updated) || now - updated <= maxAgeMs) return state;
  return { ...state, phase: "stale", error: "数据超过刷新时限" };
}

export function phaseLabel(phase: ResourcePhase): string {
  switch (phase) {
    case "loading":
      return "加载中";
    case "live":
      return "实时";
    case "stale":
      return "已过期";
    case "unavailable":
      return "不可用";
  }
}
