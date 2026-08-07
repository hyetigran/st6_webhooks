.PHONY: typecheck build test

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
