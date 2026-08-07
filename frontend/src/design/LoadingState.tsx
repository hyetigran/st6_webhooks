export function LoadingState({ label = "Loading…" }: { label?: string }) {
  return <p style={{ fontSize: 13, color: "var(--color-text-muted)" }}>{label}</p>;
}
