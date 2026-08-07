import type { ReactNode } from "react";
import { Card } from "./Card";

interface StatCardProps {
  label: string;
  value: ReactNode;
  caption?: ReactNode;
  /** Colors the value text so a stat that needs a look reads with some
   * urgency instead of the same flat weight as every "this is fine"
   * number on the page. Off by default — most stats are neutral. */
  tone?: "neutral" | "danger";
}

const toneColor: Record<NonNullable<StatCardProps["tone"]>, string> = {
  neutral: "var(--color-text)",
  danger: "var(--color-danger-700)",
};

export function StatCard({ label, value, caption, tone = "neutral" }: StatCardProps) {
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
      <span style={{ fontFamily: "var(--font-heading)", fontSize: 34, fontWeight: 400, color: toneColor[tone] }}>
        {value}
      </span>
      {caption && (
        <span style={{ fontFamily: "var(--font-body)", fontSize: 12, color: "var(--color-text-muted)" }}>
          {caption}
        </span>
      )}
    </Card>
  );
}
