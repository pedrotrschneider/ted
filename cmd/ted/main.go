package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/config"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/httpapi"
	"entertainmentdistrict/internal/metadata"
	"entertainmentdistrict/internal/realdebrid"
	"entertainmentdistrict/internal/settings"
	"entertainmentdistrict/internal/streaming"
	"entertainmentdistrict/internal/webui"

	"github.com/jackc/pgx/v5/pgconn"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		// Never print arbitrary driver/configuration errors: they may include credentials or input.
		code := ""
		var pe *pgconn.PgError
		if errors.As(err, &pe) {
			code = pe.Code
		}
		logger.Error("TED stopped", "error_type", fmt.Sprintf("%T", err), "sqlstate", code)
		os.Exit(1)
	}
}
func run(logger *slog.Logger) error {
	args := os.Args[1:]
	command := "serve"
	if len(args) > 0 {
		command = args[0]
	}
	if command == "healthcheck" {
		listen := os.Getenv("TED_LISTEN")
		if listen == "" {
			listen = ":8080"
		}
		_, port, err := net.SplitHostPort(listen)
		if err != nil {
			return errors.New("invalid healthcheck listen address")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "GET", "http://"+net.JoinHostPort("127.0.0.1", port)+"/health/ready", nil)
		if err != nil {
			return err
		}
		client := &http.Client{Transport: &http.Transport{Proxy: nil}}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return errors.New("not ready")
		}
		return nil
	}
	if (command != "serve" && command != "bootstrap") || (command == "serve" && len(args) > 1) || (command == "bootstrap" && len(args) != 2) {
		fmt.Fprintln(os.Stderr, "usage: ted [serve | bootstrap USERNAME | healthcheck] (bootstrap reads password from stdin)")
		return errors.New("invalid command")
	}
	cfg, err := config.Load()
	if err != nil {
		logger.Error(err.Error(), "operation", "configuration")
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err = database.Migrate(ctx, cfg.DatabaseURL); err != nil {
		logger.Error("migration failed; database retained; inspect schema and migration permissions", "operation", "migrate")
		return err
	}
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database connection failed; check configuration and availability", "operation", "connect")
		return err
	}
	defer pool.Close()
	passwords, err := accounts.NewPasswords(cfg.MinPasswordLength)
	if err != nil {
		return err
	}
	service := accounts.New(pool, passwords)
	if command == "bootstrap" {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 1026))
		if err != nil {
			return err
		}
		password := strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r")
		op, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_, err = service.Bootstrap(op, args[1], password)
		if errors.Is(err, accounts.ErrConflict) {
			logger.Error("bootstrap refused: TED already has accounts; no account was changed", "operation", "bootstrap")
		}
		if errors.Is(err, accounts.ErrInvalid) {
			logger.Error("bootstrap needs a 3–64 character username and a password meeting the configured minimum (maximum 1024 bytes)", "operation", "bootstrap", "minimum_password_length", cfg.MinPasswordLength)
		}
		if err == nil {
			logger.Info("local administrator created", "operation", "bootstrap")
		}
		return err
	}
	providers := settings.New(pool)
	if err = providers.Initialize(ctx, settings.Credentials{RDKey: cfg.RealDebridAPIKey, TMDBToken: cfg.Metadata.TMDBToken, TVDBKey: cfg.Metadata.TVDBKey, TVDBPIN: cfg.Metadata.TVDBPIN}); err != nil {
		return err
	}
	rd := realdebrid.NewStored(providers)
	api := httpapi.New(pool, service, !cfg.InsecureHTTP, logger, rd)
	api.Providers = providers
	api.NativeAuth, err = accounts.NewNativeAuth(pool, service, cfg.NativeAuth)
	if err != nil {
		logger.Error("invalid native authentication configuration")
		return err
	}
	api.CalendarLocation = cfg.CalendarLocation
	var streams *streaming.Service
	{
		op, cancel := context.WithTimeout(ctx, 60*time.Second)
		streams, err = streaming.New(op, pool, rd, logger, cfg.MappingWorkers)
		cancel()
		if err != nil {
			return err
		}
		if err = streams.ConfigureMappingBatchSize(cfg.MappingBatchSize); err != nil {
			return err
		}
		if err = streams.ConfigureStorage(cfg.MediaDir); err != nil {
			logger.Error("local media storage unavailable; check TED_MEDIA_DIR and folder permissions")
			return err
		}
		defer streams.CloseStorage()
		if err = streams.Start(ctx); err != nil {
			return err
		}
		api.Streaming = streams
		defer func() {
			stop, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = streams.Stop(stop)
		}()
	}
	{
		if inventory := streams.RDInventory(); inventory != nil {
			api.SetRDInventory(inventory)
		}
		enrichment, err := metadata.New(pool, metadata.NewStoredProviders(providers, cfg.Metadata), cfg.MetadataDir, logger)
		if err != nil {
			return err
		}
		defer enrichment.Close()
		if err = enrichment.Start(ctx); err != nil {
			return err
		}
		defer func() {
			stop, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = enrichment.Stop(stop)
		}()
		api.Metadata = enrichment
	}
	webDir := os.Getenv("TED_WEB_DIR")
	if webDir == "" {
		webDir = "web/dist"
	}
	ui, err := webui.New(os.DirFS(webDir))
	if err != nil {
		logger.Error("frontend build unavailable; build web assets and check TED_WEB_DIR", "operation", "serve")
		return err
	}
	handler := webui.WithAPI(api.Handler(), ui)
	server := &http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	cleanupCtx, cancelCleanup := context.WithCancel(ctx)
	cleanupDone := make(chan struct{})
	go func() { defer close(cleanupDone); database.Cleanup(cleanupCtx, pool, logger) }()
	defer func() { cancelCleanup(); <-cleanupDone }()
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	logger.Info("TED API starting", "operation", "serve", "address", cfg.Listen, "secure_cookies", !cfg.InsecureHTTP)
	select {
	case err = <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err = server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return err
		}
		logger.Info("TED stopped cleanly", "operation", "shutdown")
		return nil
	}
}
