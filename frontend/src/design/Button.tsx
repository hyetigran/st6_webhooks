import type { ButtonHTMLAttributes } from "react";

type Variant = "primary" | "secondary";

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
}

const base: React.CSSProperties = {
  fontFamily: "var(--font-body)",
  fontSize: 14,
  fontWeight: 600,
  padding: "6.8px 12.24px",
  borderRadius: "var(--radius)",
  borderWidth: 1,
  borderStyle: "solid",
  cursor: "pointer",
};

const variants: Record<Variant, React.CSSProperties> = {
  primary: {
    background: "var(--color-accent)",
    borderColor: "var(--color-accent)",
    color: "var(--color-bg)",
  },
  secondary: {
    background: "transparent",
    borderColor: "var(--color-divider)",
    color: "var(--color-text)",
  },
};

export function Button({ variant = "secondary", style, disabled, ...props }: ButtonProps) {
  return (
    <button
      {...props}
      disabled={disabled}
      style={{
        ...base,
        ...variants[variant],
        ...(disabled ? { opacity: 0.5, cursor: "not-allowed" } : null),
        ...style,
      }}
    />
  );
}
