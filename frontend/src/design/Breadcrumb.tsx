import type { ReactNode } from "react";

export function Breadcrumb({ children }: { children: ReactNode }) {
  return <p style={{ fontSize: 13, color: "var(--color-text-muted)", marginBottom: 8 }}>{children}</p>;
}
