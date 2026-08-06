# PRD.md §8's acceptance criteria. Run from node/ (this directory) — see
# README.md for the ~15-minute clone-to-run guide. Postgres must already be
# up (`docker compose up -d postgres`); every target below manages its own
# dedicated database (webhooks_node_test / _chaos / _load), separate from
# local dev data and from each other.

.PHONY: typecheck build test properties chaos load verify

typecheck:
	npm run typecheck

build:
	npm run build

# R-1, R-3, R-4, R-13, R-14, R-2, R-16 rows of PRD §8 — deterministic
# behavioral tests, plus everything test/properties (make properties is a
# more targeted lens over the same suite, not a disjoint one).
test:
	npm test

# Seeded, randomized invariants — seq order under concurrent expansion
# (R-8/R-11), repeated-key idempotency (R-6/R-21), replay crash-safety
# (R-19/R-21/R-22). Each run logs its seed; rerun with
# PROPERTY_TEST_SEED=<seed> to reproduce a specific failure exactly.
properties:
	npx vitest run test/properties

# Real process kill/SIGSTOP/SIGCONT against real spawned processes.
# R-7/R-17 (kill mid-delivery), R-11/R-15/R-17 (F-2's stall-fencing),
# R-11/R-12 (partition head blocks then drains), R-15 (crash after a
# successful send, before write-back), R-8/R-11 (F-1's other half: a real
# process killed mid-expansion, while holding the tenant's advisory lock,
# doesn't corrupt subsequent publish order). Each scenario is its own
# process; `make` stops at the first failure. ~10s total.
chaos:
	npx tsx chaos/worker-kill-mid-delivery.ts
	npx tsx chaos/worker-stall-fencing.ts
	npx tsx chaos/partition-head-blocked-then-drains.ts
	npx tsx chaos/crash-after-successful-send.ts
	npx tsx chaos/expansion-crash-order.ts

# Real HTTP latency against a real spawned api/worker pool. R-8 (publish
# and replay latency flat, independent of subscriber count / window size),
# R-18 (noisy-neighbor volume fairness, F-5's tarpit-tenant bound: a quiet
# tenant's added latency stays within roughly one outbound-timeout cycle).
# ~6s total.
load:
	npx tsx load/publish-latency-flat.ts
	npx tsx load/replay-latency-flat.ts
	npx tsx load/noisy-neighbor.ts
	npx tsx load/tarpit-fairness.ts

# Runs every PRD §8 acceptance command. Evidence (seeds, measured
# latencies, pass/fail, per scenario) lands in ../evidence/{chaos,load}/ —
# regenerated fresh on every run, safe to commit as a snapshot of the last
# real pass.
verify: typecheck test properties chaos load
	@echo ""
	@echo "make verify: all PRD §8 acceptance criteria passed."
	@echo "Evidence: $$(find ../evidence -name '*.json' | wc -l | tr -d ' ') artifact(s) in ../evidence/"
