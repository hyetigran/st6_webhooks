package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"webhooks-go/internal/api"
	"webhooks-go/internal/config"
	"webhooks-go/internal/db"
)

func main() {
	cfg := config.Load()
	key := config.SecretEncryptionKey()
	ctx := context.Background()

	pool, err := db.NewPool(ctx, cfg.DB.ConnectionString)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	srv := api.NewServer(pool, key, cfg.SecretRotation)

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	log.Printf("API listening on %s", addr)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
