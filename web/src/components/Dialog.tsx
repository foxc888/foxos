import { useEffect, useId, useRef, useState } from "react";
import { AlertTriangle, X } from "lucide-react";

type DialogProps = {
  title: string;
  eyebrow?: string;
  onClose: () => void;
  children: React.ReactNode;
  footer?: React.ReactNode;
};

const focusableSelector = [
  "button:not([disabled])",
  "[href]",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  "[tabindex]:not([tabindex='-1'])",
].join(",");

export function Dialog({ title, eyebrow, onClose, children, footer }: DialogProps) {
  const titleId = useId();
  const panelRef = useRef<HTMLDivElement>(null);
  const closeRef = useRef(onClose);

  useEffect(() => {
    closeRef.current = onClose;
  }, [onClose]);

  useEffect(() => {
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const panel = panelRef.current;
    const first = panel?.querySelector<HTMLElement>("[data-autofocus], " + focusableSelector);
    first?.focus();

    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        closeRef.current();
        return;
      }
      if (event.key !== "Tab" || !panel) return;
      const focusable = Array.from(panel.querySelectorAll<HTMLElement>(focusableSelector));
      if (focusable.length === 0) {
        event.preventDefault();
        panel.focus();
        return;
      }
      const firstElement = focusable[0];
      const lastElement = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === firstElement) {
        event.preventDefault();
        lastElement.focus();
      } else if (!event.shiftKey && document.activeElement === lastElement) {
        event.preventDefault();
        firstElement.focus();
      }
    };

    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      opener?.focus();
    };
  }, []);

  return (
    <div className="modal-backdrop" onMouseDown={(event) => event.target === event.currentTarget && onClose()}>
      <div
        aria-labelledby={titleId}
        aria-modal="true"
        className="modal"
        ref={panelRef}
        role="dialog"
        tabIndex={-1}
      >
        <div className="modal-heading">
          <div>
            {eyebrow ? <small>{eyebrow}</small> : null}
            <h2 id={titleId}>{title}</h2>
          </div>
          <button aria-label="关闭弹窗" onClick={onClose} type="button">
            <X size={19} />
          </button>
        </div>
        {children}
        {footer ? <div className="modal-actions">{footer}</div> : null}
      </div>
    </div>
  );
}

type ConfirmDialogProps = {
  title: string;
  description: string;
  impacts: string[];
  warnings?: string[];
  confirmLabel: string;
  busy?: boolean;
  onCancel: () => void;
  onConfirm: () => void;
};

export function ConfirmDialog({
  title,
  description,
  impacts,
  warnings = [],
  confirmLabel,
  busy = false,
  onCancel,
  onConfirm,
}: ConfirmDialogProps) {
  const [acknowledged, setAcknowledged] = useState(false);
  return (
    <Dialog
      eyebrow="高风险操作确认"
      onClose={() => !busy && onCancel()}
      title={title}
      footer={(
        <>
          <button className="button secondary" disabled={busy} onClick={onCancel} type="button">取消</button>
          <button className="button danger" disabled={!acknowledged || busy} onClick={onConfirm} type="button">
            {busy ? "正在执行…" : confirmLabel}
          </button>
        </>
      )}
    >
      <div className="confirm-body">
        <p>{description}</p>
        <section aria-labelledby="impact-title" className="impact-list">
          <h3 id="impact-title">影响范围</h3>
          <ul>{impacts.map((impact) => <li key={impact}>{impact}</li>)}</ul>
        </section>
        {warnings.length ? (
          <div className="warning-note" role="alert">
            <AlertTriangle aria-hidden="true" size={17} />
            <span>{warnings.join("；")}</span>
          </div>
        ) : null}
        <label className="confirm-check">
          <input
            checked={acknowledged}
            data-autofocus
            onChange={(event) => setAcknowledged(event.target.checked)}
            type="checkbox"
          />
          <span>我已核对影响范围和回滚条件</span>
        </label>
      </div>
    </Dialog>
  );
}
