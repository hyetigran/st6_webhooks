import { Link } from "react-router-dom";
import { Button } from "../design/Button";
import { Card } from "../design/Card";

const pipelineStages = [
  { title: "Publisher", body: "Internal service calls POST /events with an Idempotency-Key." },
  { title: "Events", body: "One row, status pending_expansion. 202 returned immediately — O(1) regardless of subscriber count." },
  { title: "Expansion loop", body: "Per-tenant advisory lock. One delivery row per subscribed endpoint, atomically." },
  { title: "Endpoint queues", body: "Strict FIFO per endpoint. A worker claims the oldest pending delivery, signs, sends, retries." },
];

const guarantees = [
  { title: "Ordered", body: "One delivery in flight per endpoint at a time. A blocked delivery reports it, and why." },
  { title: "Retried", body: "Full-jitter exponential backoff, six attempts, then the endpoint halts — not silently dropped." },
  { title: "Explainable", body: "Every delivery's exact request and response history, on one screen, no engineer required." },
  { title: "Replayable", body: "Re-deliver any past window without disturbing other endpoints' live traffic." },
];

export function Landing() {
  return (
    <div style={{ minHeight: "100vh", background: "var(--color-bg)" }}>
      <header
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          borderBottom: "1px solid var(--color-divider)",
          padding: "0 24px",
          height: 64,
        }}
      >
        <span style={{ fontFamily: "var(--font-heading)", fontWeight: 700, fontSize: 20, letterSpacing: 0.8 }}>
          Gauntlet Relay
        </span>
        <nav style={{ display: "flex", alignItems: "center", gap: 24 }}>
          <a href="#pipeline" style={{ color: "var(--color-text)", fontSize: 14 }}>
            The pipeline
          </a>
          <a href="#guarantees" style={{ color: "var(--color-text)", fontSize: 14 }}>
            Guarantees
          </a>
          <Link to="/console/overview">
            <Button variant="primary">Open the dashboard</Button>
          </Link>
        </nav>
      </header>

      <section style={{ display: "flex", gap: 48, padding: "64px 24px", maxWidth: 1100, margin: "0 auto" }}>
        <div style={{ flex: 1 }}>
          <h1 style={{ fontSize: 44, lineHeight: 1.05, marginBottom: 20 }}>
            Every webhook accounted for.
            <br />
            <span style={{ color: "var(--color-accent)" }}>Out loud.</span>
          </h1>
          <p style={{ maxWidth: 420, marginBottom: 24, color: "var(--color-text-muted)" }}>
            Gauntlet Relay delivers your events in the order you published them, keeps retrying the ones that fail, and
            shows your team exactly what happened to any single event — on one screen, no engineer required.
          </p>
          <div style={{ display: "flex", gap: 12 }}>
            <Link to="/console/overview">
              <Button variant="primary">Open the dashboard</Button>
            </Link>
            <a href="https://gitlab.com" target="_blank" rel="noreferrer">
              <Button variant="secondary">Source code</Button>
            </a>
          </div>
        </div>
      </section>

      <section id="pipeline" style={{ padding: "48px 24px", maxWidth: 1100, margin: "0 auto" }}>
        <h2 style={{ fontSize: 22, marginBottom: 24 }}>The pipeline</h2>
        <div style={{ display: "grid", gridTemplateColumns: "repeat(4, 1fr)", gap: 16 }}>
          {pipelineStages.map((stage, i) => (
            <Card key={stage.title} cornerMarks style={{ display: "flex", flexDirection: "column", gap: 8 }}>
              <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--color-text-muted)" }}>
                {String(i + 1).padStart(2, "0")}
              </span>
              <h3 style={{ fontSize: 15 }}>{stage.title}</h3>
              <p style={{ fontSize: 13, color: "var(--color-text-muted)", margin: 0 }}>{stage.body}</p>
            </Card>
          ))}
        </div>
      </section>

      <section id="guarantees" style={{ padding: "48px 24px 96px", maxWidth: 1100, margin: "0 auto" }}>
        <h2 style={{ fontSize: 22, marginBottom: 24 }}>Guarantees</h2>
        <div style={{ display: "grid", gridTemplateColumns: "repeat(2, 1fr)", gap: 16 }}>
          {guarantees.map((g) => (
            <Card key={g.title}>
              <h3 style={{ fontSize: 15, marginBottom: 6 }}>{g.title}</h3>
              <p style={{ fontSize: 13, color: "var(--color-text-muted)", margin: 0 }}>{g.body}</p>
            </Card>
          ))}
        </div>
      </section>
    </div>
  );
}
