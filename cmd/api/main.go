package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lumen-dev02/team-task-manager/internal/auth"
	"github.com/lumen-dev02/team-task-manager/internal/config"
	"github.com/lumen-dev02/team-task-manager/internal/httpapi"
	"github.com/lumen-dev02/team-task-manager/internal/store"
	"github.com/lumen-dev02/team-task-manager/migrations"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("application stopped", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	migrate := flag.Bool("migrate", false, "apply database migrations and exit")
	flag.Parse()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("parse database configuration: %w", err)
	}
	poolCfg.MaxConns = 10
	poolCfg.ConnConfig.ConnectTimeout = 5 * time.Second
	db, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err = db.Ping(startup); err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	if *migrate {
		if err = migrations.Up(startup, db); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		log.Info("migrations applied")
		return nil
	}
	srv := &http.Server{Addr: cfg.Addr, Handler: httpapi.New(store.New(db), auth.New(cfg.JWTSecret), log),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	result := make(chan error, 1)
	go func() { result <- srv.ListenAndServe() }()
	log.Info("http server starting", "addr", cfg.Addr)
	select {
	case err = <-result:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		stop()
	}
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelShutdown()
	if err = srv.Shutdown(shutdown); err != nil {
		_ = srv.Close()
		return fmt.Errorf("shutdown: %w", err)
	}
	log.Info("http server stopped")
	return nil
}
