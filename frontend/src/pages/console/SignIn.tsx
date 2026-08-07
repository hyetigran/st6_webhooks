import { useState, type FormEvent } from "react";
import { Button } from "../../design/Button";
import { Card } from "../../design/Card";

export function SignIn({ onSignIn }: { onSignIn: (apiKey: string) => void }) {
  const [value, setValue] = useState("");

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    const trimmed = value.trim();
    if (trimmed) onSignIn(trimmed);
  }

  return (
    <div style={{ display: "flex", justifyContent: "center", paddingTop: 80 }}>
      <Card style={{ width: 420 }}>
        <h2 style={{ fontSize: 20, marginBottom: 12 }}>Sign in</h2>
        <p style={{ fontSize: 14, color: "var(--color-text-muted)", marginBottom: 20 }}>
          Paste your tenant API key. Keys are seeded out-of-band — run{" "}
          <code style={{ fontFamily: "var(--font-mono)" }}>npm run seed</code> (Node) or{" "}
          <code style={{ fontFamily: "var(--font-mono)" }}>go run ./cmd/seed</code> (Go) against the backend you want to
          use, then paste the printed key here.
        </p>
        <form onSubmit={handleSubmit} style={{ display: "flex", flexDirection: "column", gap: 12 }}>
          <input
            type="password"
            autoFocus
            placeholder="tenant_..."
            value={value}
            onChange={(e) => setValue(e.target.value)}
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 13,
              padding: "8px 10px",
              border: "1px solid var(--color-divider)",
              borderRadius: "var(--radius)",
            }}
          />
          <Button type="submit" variant="primary" disabled={!value.trim()}>
            Continue
          </Button>
        </form>
      </Card>
    </div>
  );
}
