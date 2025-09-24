package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dekonix/openai-proxy/internal/config"
	"github.com/dekonix/openai-proxy/internal/server"
	"github.com/dekonix/openai-proxy/internal/storage"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	store, err := storage.New(ctx, cfg.DatabaseURL)
	cancel()
	if err != nil {
		log.Fatalf("init storage: %v", err)
	}
	defer store.Close()

	srv, err := server.New(cfg, store)
	if err != nil {
		log.Fatalf("init server: %v", err)
	}

	httpServer := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: srv.Handler(),
	}

	go func() {
		log.Printf("proxy listening on %s", cfg.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}
