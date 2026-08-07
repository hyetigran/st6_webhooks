package main

import (
	"context"
	"log"

	"webhooks-go/internal/config"
	"webhooks-go/internal/db"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()

	pool, err := db.NewPool(ctx, cfg.DB.ConnectionString)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		log.Fatal(err)
	}
}
