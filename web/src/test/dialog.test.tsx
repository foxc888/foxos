import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ConfirmDialog } from "../components/Dialog";

describe("ConfirmDialog", () => {
  it("requires an explicit impact acknowledgement before confirming", () => {
    const onConfirm = vi.fn();
    const onCancel = vi.fn();
    render(<ConfirmDialog confirmLabel="删除" description="会移除节点" impacts={["节点 A", "代理组引用"]} onCancel={onCancel} onConfirm={onConfirm} title="删除节点" />);

    expect(screen.getByRole("button", { name: "删除" })).toBeDisabled();
    fireEvent.click(screen.getByRole("checkbox"));
    expect(screen.getByRole("button", { name: "删除" })).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: "删除" }));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it("closes on Escape and returns focus to the opener", () => {
    const opener = document.createElement("button");
    opener.textContent = "open";
    document.body.append(opener);
    opener.focus();
    const onCancel = vi.fn();
    render(<ConfirmDialog confirmLabel="继续" description="描述" impacts={["影响"]} onCancel={onCancel} onConfirm={vi.fn()} title="确认" />);
    fireEvent.keyDown(document, { key: "Escape" });
    expect(onCancel).toHaveBeenCalledTimes(1);
    opener.remove();
  });
});
