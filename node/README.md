# Webhook delivery service — Node.js/TypeScript

Partial build in progress; see the map issue and `../DECISIONS.md` for the architecture. This
README covers what's implemented so far (schema + endpoint management API). It'll be expanded
into a full clone-to-run guide by the "Test suite & deployment" ticket.

## Run locally

```sh
docker compose up -d postgres
cp .env.example .env
npm install
npm run migrate
npm run seed        # prints a demo tenant's API key
npm run dev
```

`GET /healthz` is unauthenticated. Every other route needs `Authorization: Bearer <api_key>`
from the seed step above.
