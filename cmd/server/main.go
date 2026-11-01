package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ahsyarif/promptify/internal/db"
	"github.com/ahsyarif/promptify/internal/payment/midtrans"
	"github.com/ahsyarif/promptify/internal/server"
	"github.com/ahsyarif/promptify/internal/store"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	addr := envOr("ADDR", ":8080")
	cfg, err := loadConfig(logger)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, dbURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	srv, err := server.New(store.New(pool), cfg, logger)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", addr)
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}

func loadConfig(logger *slog.Logger) (server.Config, error) {
	cfg := server.Config{
		UploadDir: envOr("UPLOAD_DIR", "uploads"),
		PublicURL: os.Getenv("PUBLIC_URL"),
		Google: server.GoogleConfig{
			ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
			ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		},
	}

	for e := range strings.SplitSeq(os.Getenv("ADMIN_EMAILS"), ",") {
		if e = strings.TrimSpace(e); e != "" {
			cfg.AdminEmails = append(cfg.AdminEmails, e)
		}
	}
	if len(cfg.AdminEmails) == 0 {
		logger.Warn("ADMIN_EMAILS is empty: nobody can add or edit prompts")
	}

	price, err := strconv.ParseInt(envOr("MEMBERSHIP_PRICE", "49000"), 10, 64)
	if err != nil || price <= 0 {
		return cfg, fmt.Errorf("MEMBERSHIP_PRICE must be a positive whole number of rupiah, got %q", os.Getenv("MEMBERSHIP_PRICE"))
	}
	cfg.MembershipPrice = price

	if key := os.Getenv("MIDTRANS_SERVER_KEY"); key != "" {
		production := os.Getenv("MIDTRANS_PRODUCTION") == "true"
		cfg.Midtrans = midtrans.New(key, production)
		logger.Info("payments enabled", "provider", "midtrans", "production", production)
	} else {
		logger.Warn("MIDTRANS_SERVER_KEY is not set: payments are disabled")
	}

	if (cfg.Google.ClientID == "") != (cfg.Google.ClientSecret == "") {
		return cfg, errors.New("set both GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET, or neither")
	}
	if cfg.Google.Enabled() && cfg.PublicURL == "" {
		return cfg, errors.New("PUBLIC_URL is required for Google login")
	}
	if !cfg.Google.Enabled() {
		logger.Warn("GOOGLE_CLIENT_ID is not set: Google login is disabled")
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
