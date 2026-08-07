import { Link } from "react-router-dom";
import { Card } from "../../design/Card";

export function NotFound() {
  return (
    <Card>
      <h1 style={{ fontSize: 20, marginBottom: 8 }}>Not found</h1>
      <p style={{ fontSize: 13, color: "var(--color-text-muted)", margin: 0 }}>
        Nothing here. <Link to="/console/overview">Back to overview</Link>.
      </p>
    </Card>
  );
}
