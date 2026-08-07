import { NavLink, Outlet } from "react-router-dom";
import { useAuth } from "../../lib/auth";
import { useBackend } from "../../lib/backend";
import { SignIn } from "./SignIn";

const navLinkStyle = ({ isActive }: { isActive: boolean }): React.CSSProperties => ({
  display: "inline-block",
  padding: "10px 20px",
  fontFamily: "var(--font-body)",
  fontSize: 15,
  color: "var(--color-text)",
  background: isActive ? "var(--color-accent)" : "transparent",
  ...(isActive ? { color: "var(--color-bg)" } : null),
});

export function ConsoleShell() {
  const { backend, backends, setBackendId } = useBackend();
  const { apiKey, signIn, signOut } = useAuth();

  return (
    <div style={{ minHeight: "100vh", background: "var(--color-bg)" }}>
      <header
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          borderBottom: "1px solid var(--color-divider)",
          padding: "0 24px",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 24 }}>
          <NavLink to="/" style={{ color: "var(--color-text)" }}>
            <span style={{ fontFamily: "var(--font-heading)", fontWeight: 700, fontSize: 20, letterSpacing: 0.8 }}>
              Gauntlet Relay
            </span>
          </NavLink>
          <span style={{ color: "var(--color-divider)" }}>|</span>
          <span style={{ fontFamily: "var(--font-body)", fontSize: 12, letterSpacing: 1, color: "var(--color-text-muted)" }}>
            CONSOLE
          </span>
          <nav style={{ display: "flex" }}>
            <NavLink to="/console/endpoints" style={navLinkStyle}>
              Endpoints
            </NavLink>
          </nav>
        </div>

        <div style={{ display: "flex", alignItems: "center", gap: 16 }}>
          <label
            style={{ fontFamily: "var(--font-body)", fontSize: 12, letterSpacing: 1, color: "var(--color-text-muted)" }}
          >
            BACKEND
          </label>
          <select
            value={backend.id}
            onChange={(e) => setBackendId(e.target.value as "node" | "go")}
            style={{
              border: "1px solid var(--color-divider)",
              borderRadius: "var(--radius)",
              padding: "6px 8px",
              background: "var(--color-surface)",
            }}
          >
            {backends.map((b) => (
              <option key={b.id} value={b.id}>
                {b.label}
              </option>
            ))}
          </select>
          {apiKey && (
            <button
              onClick={signOut}
              style={{ background: "none", border: "none", cursor: "pointer", color: "var(--color-accent-700)", fontSize: 13 }}
            >
              Sign out
            </button>
          )}
        </div>
      </header>

      <main style={{ padding: "24px 32px" }}>{apiKey ? <Outlet /> : <SignIn onSignIn={signIn} />}</main>
    </div>
  );
}
