import { Card } from "./Card";

export function ErrorState({ message }: { message: string }) {
  return (
    <Card style={{ borderColor: "var(--color-danger)" }}>
      <p style={{ margin: 0, fontSize: 13, color: "var(--color-danger)" }}>{message}</p>
    </Card>
  );
}
