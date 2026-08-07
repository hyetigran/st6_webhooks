export function Field({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <div style={{ fontSize: 11, letterSpacing: 0.4, textTransform: "uppercase", color: "var(--color-text-muted)" }}>
        {label}
      </div>
      <div className={mono ? "app-mono" : undefined} style={{ fontSize: 14 }}>
        {value}
      </div>
    </div>
  );
}
