import type { InputHTMLAttributes } from "react";

const style: React.CSSProperties = {
  fontFamily: "var(--font-body)",
  fontSize: 14,
  padding: "8px 10px",
  border: "1px solid var(--color-divider)",
  borderRadius: "var(--radius)",
};

/** Every plain text/url/datetime-local input in the app shares this look —
 * previously copy-pasted as an inline style object in three separate
 * files (RegisterForm, ReplayForm, the Events filter box). */
export function TextInput(props: InputHTMLAttributes<HTMLInputElement>) {
  return <input {...props} style={{ ...style, ...props.style }} />;
}
