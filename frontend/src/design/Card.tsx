import type { CSSProperties, ReactNode } from "react";

interface CardProps {
  children: ReactNode;
  style?: CSSProperties;
  /** The reference mockup marks certain section wrappers (list panels,
   * table headers) with small "+" glyphs at each corner — a blueprint/
   * schematic accent, not used on every bordered box (inner item cards
   * inside a marked section stay plain). Off by default. */
  cornerMarks?: boolean;
}

const cornerStyle: CSSProperties = {
  position: "absolute",
  fontFamily: "var(--font-body)",
  fontSize: 12,
  lineHeight: 1,
  color: "var(--color-divider)",
  userSelect: "none",
};

export function Card({ children, style, cornerMarks = false }: CardProps) {
  return (
    <div
      style={{
        position: "relative",
        border: "1px solid var(--color-divider)",
        borderRadius: "var(--radius)",
        padding: "16px 18px",
        background: "var(--color-surface)",
        ...style,
      }}
    >
      {cornerMarks && (
        <>
          <span aria-hidden style={{ ...cornerStyle, top: -7, left: -7 }}>+</span>
          <span aria-hidden style={{ ...cornerStyle, top: -7, right: -7 }}>+</span>
          <span aria-hidden style={{ ...cornerStyle, bottom: -7, left: -7 }}>+</span>
          <span aria-hidden style={{ ...cornerStyle, bottom: -7, right: -7 }}>+</span>
        </>
      )}
      {children}
    </div>
  );
}
