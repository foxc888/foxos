import { describe, expect, it } from "vitest";

describe("test runner", () => {
  it("loads DOM matchers", () => expect(document.body).toBeInTheDocument());
});
