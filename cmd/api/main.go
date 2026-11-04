package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Naveenkumar75/omnidesk/internal/auth"
	"github.com/Naveenkumar75/omnidesk/internal/config"
	"github.com/Naveenkumar75/omnidesk/internal/httpapi"
	"github.com/Naveenkumar75/omnidesk/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	api := &httpapi.API{Store: st, Issuer: auth.NewIssuer(cfg.JWTSecret, 24*time.Hour)}
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: api.Router(), ReadHeaderTimeout: 5 * time.Second}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("omnidesk listening on %s", cfg.HTTPAddr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server: %v", err)
	}
}
