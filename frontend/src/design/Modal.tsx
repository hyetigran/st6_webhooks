import type { ReactNode } from "react";
import { Card } from "./Card";

export function Modal({ children, onClose }: { children: ReactNode; onClose: () => void }) {
  return (
    <div
      role="presentation"
      onClick={onClose}
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(29, 31, 32, 0.4)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        zIndex: 100,
      }}
    >
      <div role="dialog" aria-modal="true" onClick={(e) => e.stopPropagation()}>
        <Card style={{ width: 460, background: "var(--color-surface)" }}>{children}</Card>
      </div>
    </div>
  );
}
