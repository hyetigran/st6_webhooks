// Tenants are seeded out-of-band, not self-service (Shared REST API
// contract — no signup flow is in scope). This command creates a demo
// tenant with a printed API key for local development and the test suite.
// Mirrors node/src/db/seed.ts.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"webhooks-go/internal/config"
	"webhooks-go/internal/crypto"
	"webhooks-go/internal/db"
	"webhooks-go/internal/secrets"
)

func main() {
	name := "demo"
	if len(os.Args) > 1 {
		name = os.Args[1]
	}
	apiKey := secrets.Generate("tenant")

	cfg := config.Load()
	ctx := context.Background()

	pool, err := db.NewPool(ctx, cfg.DB.ConnectionString)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	var id string
	// Only the hash is stored (REVIEW.md F-16) — this is the one and only
	// place the plaintext key is ever printed.
	err = pool.QueryRow(ctx,
		`INSERT INTO tenants (name, api_key_hash) VALUES ($1, $2) RETURNING id`,
		name, crypto.HashAPIKey(apiKey),
	).Scan(&id)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Created tenant %q (%s)\n", name, id)
	fmt.Printf("API key: %s\n", apiKey)
}
