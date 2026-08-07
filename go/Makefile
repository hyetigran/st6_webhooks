# PRD.md §8's acceptance criteria. Run from go/ (this directory) — see
# README.md for the ~15-minute clone-to-run guide. Postgres must already be
# up (`docker compose up -d postgres`); every target below manages its own
# dedicated database (webhooks_go_test / _chaos / _load), separate from
# local dev data and from each other.

.PHONY: typecheck build test properties build-scenarios chaos load verify

typecheck:
	go vet ./...

build:
	go build ./...

# -p 1: test packages share one Postgres database (webhooks_go_test) via
# TRUNCATE-based fixtures, the same reason node/vitest.config.ts sets
# fileParallelism: false — running package test binaries in parallel (go
# test's default) lets one package's TRUNCATE wipe rows another package's
# test just inserted.
test:
	go test -p 1 ./...

# Seeded, randomized invariants — seq order under concurrent expansion
# (R-8/R-11), repeated-key idempotency (R-6/R-21), replay crash-safety
# (R-19/R-21/R-22). Every property test's name contains "Property". Each run
# logs its seed; rerun with PROPERTY_TEST_SEED=<seed> to reproduce a
# specific failure exactly.
properties:
	go test -p 1 ./... -run Property -v

# chaos/load scenarios exec pre-built binaries directly (never `go run` —
# see internal/scenariosupport's ManagedProcess doc comment for why: `go
# run` execs its compiled binary as a child process, so SIGKILL/SIGSTOP
# sent to the `go run` wrapper never reaches the real process). bin/ is
# gitignored, so every chaos/load run rebuilds fresh.
build-scenarios:
	go build -o bin/api ./cmd/api
	go build -o bin/chaosworker ./cmd/chaosworker
	go build -o bin/expansionholder ./cmd/expansionholder

# Real process kill/SIGSTOP/SIGCONT against real spawned processes.
# R-7/R-17 (kill mid-delivery), R-11/R-15/R-17 (F-2's stall-fencing),
# R-11/R-12 (partition head blocks then drains), R-15 (crash after a
# successful send, before write-back), R-8/R-11 (F-1's other half: a real
# process killed mid-expansion, while holding the tenant's advisory lock,
# doesn't corrupt subsequent publish order). Each scenario is its own
# process; `make` stops at the first failure.
chaos: build-scenarios
	go run ./chaos/workerkillmiddelivery
	go run ./chaos/workerstallfencing
	go run ./chaos/partitionheadblockedthendrains
	go run ./chaos/crashaftersuccessfulsend
	go run ./chaos/expansioncrashorder

# Real HTTP latency against a real spawned api/worker pool. R-8 (publish
# and replay latency flat, independent of subscriber count / window size),
# R-18 (noisy-neighbor volume fairness, F-5's tarpit-tenant bound: a quiet
# tenant's added latency stays within roughly one outbound-timeout cycle).
load: build-scenarios
	go run ./load/publishlatencyflat
	go run ./load/replaylatencyflat
	go run ./load/noisyneighbor
	go run ./load/tarpitfairness

# Runs every PRD §8 acceptance command. Evidence (seeds, measured
# latencies, pass/fail, per scenario) lands in ../evidence/go/{chaos,load}/
# — namespaced separately from the Node stack's ../evidence/{chaos,load}/,
# since both stacks name scenarios identically and a shared directory would
# let each stack's verify run clobber the other's committed evidence.
# Regenerated fresh on every run, safe to commit as a snapshot of the last
# real pass.
verify: typecheck test properties chaos load
	@echo ""
	@echo "make verify: all PRD §8 acceptance criteria passed."
	@echo "Evidence: $$(find ../evidence/go -name '*.json' | wc -l | tr -d ' ') artifact(s) in ../evidence/go/"
