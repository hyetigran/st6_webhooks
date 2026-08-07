import type { ReactNode } from "react";

interface BadgeProps {
  children: ReactNode;
  tone?: "neutral" | "accent" | "danger";
}

const toneColor: Record<NonNullable<BadgeProps["tone"]>, string> = {
  neutral: "var(--color-text-muted)",
  accent: "var(--color-accent-700)",
  danger: "var(--color-danger-700)",
};

/** Small status pill — matches the reference mockup's flat, borderless,
 * neutral-background badge (no color-fill for severity; the copy carries
 * the weight, the badge just labels the state). */
export function Badge({ children, tone = "neutral" }: BadgeProps) {
  return (
    <span
      style={{
        display: "inline-block",
        fontFamily: "var(--font-body)",
        fontSize: 11,
        letterSpacing: 0.2,
        padding: "3px 10px",
        background: "var(--color-badge-bg)",
        color: toneColor[tone],
        borderRadius: "var(--radius)",
      }}
    >
      {children}
    </span>
  );
}
