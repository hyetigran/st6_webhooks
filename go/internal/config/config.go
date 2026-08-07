// Package config loads every tunable from the environment, mirroring
// node/src/config.ts field-for-field so both stacks share one set of
// env-var names and defaults. Every retry/timeout-driven value is
// env-configurable so tests can use small, fast values instead of
// production defaults (PRD.md §8's "time is injected").
package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

func init() {
	// Best-effort: missing .env is fine in prod, where real env vars are set
	// directly (docker-compose, etc).
	_ = godotenv.Load()
}

func envInt(name string, fallback int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		panic(fmt.Sprintf("env var %s must be a number, got %q", name, raw))
	}
	return parsed
}

func envString(name string, fallback string, required bool) string {
	raw := os.Getenv(name)
	if raw == "" {
		if required {
			panic(fmt.Sprintf("env var %s is required", name))
		}
		return fallback
	}
	return raw
}

// Backoff is full-jitter exponential retry config (docs/adr: Attempt ceiling
// & backoff schedule): delay = random(0, min(baseDelayMs * multiplier **
// attempt, maxDelayMs)). Global config only for v0.2.0 — per-endpoint tuning
// is explicitly out of scope.
type BackoffConfig struct {
	BaseDelayMs int
	Multiplier  int
	MaxDelayMs  int
	MaxAttempts int
}

// OutboundHTTPConfig bounds the actual outbound webhook call (R-16). No
// max-redirects setting — docs/adr/0006 replaced "bounded redirect count"
// with "never follow redirects" (a hop-count limit bounds count, not
// destination), so there's nothing to bound here.
type OutboundHTTPConfig struct {
	ConnectTimeoutMs      int
	TotalTimeoutMs        int
	MaxResponseBodyBytes  int
	MaxConnectionsPerHost int
}

// SigningConfig is the receiver-contract signature tolerance.
type SigningConfig struct {
	TimestampToleranceSec int
}

// SecretRotationConfig is the sender-side multi-sign overlap window
// (docs/adr/0003): how long a rotated-out secret keeps being signed with,
// alongside the current one.
type SecretRotationConfig struct {
	OverlapHours int
}

// ServerConfig is the API's own listen port.
type ServerConfig struct {
	Port int
}

// WorkerConfig tunes the poll loop's idle wait.
type WorkerConfig struct {
	// How long to wait before polling again after a cycle found nothing to
	// do. 0 when a cycle did find work, so the worker drains a backlog
	// immediately rather than waiting out a full idle interval per item.
	IdlePollIntervalMs int
}

// DBConfig is the Postgres connection string.
type DBConfig struct {
	ConnectionString string
}

// Config is every env-configurable tunable, loaded once at process start.
type Config struct {
	Backoff        BackoffConfig
	OutboundHTTP   OutboundHTTPConfig
	Signing        SigningConfig
	SecretRotation SecretRotationConfig
	Server         ServerConfig
	Worker         WorkerConfig
	DB             DBConfig

	// leaseMinDurationMs backs LeaseDurationMs() below.
	leaseMinDurationMs int
}

// Load reads every tunable from the environment. Panics on a malformed
// value or a missing required var (SECRET_ENCRYPTION_KEY is validated
// separately via SecretEncryptionKey(), not here, since only the byte
// length check needs the raw value).
func Load() Config {
	return Config{
		Backoff: BackoffConfig{
			BaseDelayMs: envInt("BACKOFF_BASE_DELAY_MS", 1_000),
			Multiplier:  envInt("BACKOFF_MULTIPLIER", 2),
			MaxDelayMs:  envInt("BACKOFF_MAX_DELAY_MS", 30_000),
			MaxAttempts: envInt("BACKOFF_MAX_ATTEMPTS", 6),
		},
		OutboundHTTP: OutboundHTTPConfig{
			ConnectTimeoutMs:      envInt("OUTBOUND_CONNECT_TIMEOUT_MS", 5_000),
			TotalTimeoutMs:        envInt("OUTBOUND_TOTAL_TIMEOUT_MS", 10_000),
			MaxResponseBodyBytes:  envInt("OUTBOUND_MAX_RESPONSE_BODY_BYTES", 64*1024),
			MaxConnectionsPerHost: envInt("OUTBOUND_MAX_CONNECTIONS_PER_HOST", 10),
		},
		Signing: SigningConfig{
			TimestampToleranceSec: envInt("SIGNATURE_TIMESTAMP_TOLERANCE_SEC", 5*60),
		},
		SecretRotation: SecretRotationConfig{
			OverlapHours: envInt("SECRET_ROTATION_OVERLAP_HOURS", 24),
		},
		Server: ServerConfig{
			Port: envInt("PORT", 8090),
		},
		Worker: WorkerConfig{
			IdlePollIntervalMs: envInt("WORKER_IDLE_POLL_INTERVAL_MS", 200),
		},
		DB: DBConfig{
			ConnectionString: envString("DATABASE_URL", "postgres://webhooks:webhooks@localhost:5533/webhooks_go", false),
		},
		leaseMinDurationMs: envInt("LEASE_MIN_DURATION_MS", 30_000),
	}
}

// LeaseDurationMs is derived from the outbound HTTP timeout, not an
// independent constant, so the two can't drift apart (docs/adr/0002). The
// floor is itself env-configurable so chaos tests can shrink lease expiry to
// a few seconds instead of always waiting out the production-safe default.
func (c Config) LeaseDurationMs() int {
	timeout := c.OutboundHTTP.TotalTimeoutMs
	floor := c.leaseMinDurationMs
	if timeout > floor {
		floor = timeout
	}
	return timeout + floor
}

// SecretEncryptionKey decodes and validates SECRET_ENCRYPTION_KEY (32-byte,
// base64). Required, no hardcoded fallback even for local dev — generate one
// with `openssl rand -base64 32` and put it in .env.
func SecretEncryptionKey() []byte {
	raw := envString("SECRET_ENCRYPTION_KEY", "", true)
	buf, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		panic(fmt.Sprintf("SECRET_ENCRYPTION_KEY must be valid base64: %v", err))
	}
	if len(buf) != 32 {
		panic("SECRET_ENCRYPTION_KEY must decode to exactly 32 bytes (base64-encoded)")
	}
	return buf
}
