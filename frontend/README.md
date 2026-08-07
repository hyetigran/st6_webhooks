# Webhook delivery service — frontend

One shared React SPA against either backend's identical REST API (ADR-008/DECISIONS.md) — a
runtime toggle switches between the Node/TS and Go implementations without a rebuild. Endpoint
management, event/delivery detail, endpoint queue view, and replay all live here.

## Prerequisites

- Node.js 20+
- A running backend — see `../node/README.md` or `../go/README.md`. Both must have CORS enabled
  for a browser to call them cross-origin (see `CORS_ORIGIN` in each stack's `.env.example`).

## Quickstart

```sh
cd frontend
npm install
npm run dev              # http://localhost:5173
```

Pick a backend from the "BACKEND" selector in the console nav (Node `:3000` / Go `:8090` by
default — override via `VITE_NODE_API_URL`/`VITE_GO_API_URL`), then sign in with a tenant API
key from that backend's own seed step (`npm run seed` / `go run ./cmd/seed`).

## Design reference

The visual design (fonts, colors, zero-border-radius, component patterns) is a faithful port of
the mockup pasted at the repo root — see `src/design/tokens.css` for the extracted tokens.

## Running tests

```sh
npm run typecheck
npm test             # the typed API client — request shapes, cursor pagination, error handling
```

Component-level views aren't unit-tested — they're display-heavy and low-signal to test in
isolation; verify them by running `npm run dev` against a real backend.

## Project structure

```
src/
  api/            typed client against the Shared REST API contract (TDD'd), + useApiClient hook
  design/         design tokens + base components (Button, Card, Badge, Modal, StatCard, Table)
  lib/            backend switcher + auth (tenant API key) context, formatting helpers
  pages/
    Landing.tsx           marketing/pitch page
    console/               the actual dashboard app
      ConsoleShell.tsx      nav shell: backend switcher, sign-in gate
      Endpoints.tsx          PRD §7 surface 1: list, register, pause/resume, rotate secret
```
