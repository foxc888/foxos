import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterAll, afterEach, beforeAll, vi } from "vitest";
import { setupServer } from "msw/node";

export const server = setupServer();

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => {
  cleanup();
  server.resetHandlers();
  vi.useRealTimers();
  window.sessionStorage.clear();
});
afterAll(() => server.close());

Object.defineProperty(window, "scrollTo", { value: vi.fn(), writable: true });
