import type { ReactNode } from "react";
import { Card } from "./Card";

interface StatCardProps {
  label: string;
  value: ReactNode;
  caption?: ReactNode;
}

export function StatCard({ label, value, caption }: StatCardProps) {
  return (
    <Card style={{ display: "flex", flexDirection: "column", gap: 6 }}>
      <span
        style={{
          fontFamily: "var(--font-body)",
          fontSize: 11,
          letterSpacing: 0.6,
          textTransform: "uppercase",
          color: "var(--color-text-muted)",
        }}
      >
        {label}
      </span>
      <span style={{ fontFamily: "var(--font-heading)", fontSize: 34, fontWeight: 400 }}>{value}</span>
      {caption && (
        <span style={{ fontFamily: "var(--font-body)", fontSize: 12, color: "var(--color-text-muted)" }}>
          {caption}
        </span>
      )}
    </Card>
  );
}
