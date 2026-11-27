package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	accesscontrol "backend/access_control"
	databasemanagement "backend/database_management"
	"backend/internal/app"
	"backend/internal/telemetry"
	"backend/migration"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	slog.SetDefault(slog.New(telemetry.NewHandler(os.Stdout)))
	gin.SetMode(gin.ReleaseMode)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		slog.ErrorContext(ctx, "application.stopped", "status", "failed", "error", err.Error())
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	var repository *migration.Repository
	if dsn := accesscontrol.PlatformDSN(); dsn != "" {
		config, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			return errors.New("invalid platform database configuration")
		}
		config.ConnConfig.Tracer = telemetry.PostgresTracer{Scope: "platform"}
		pool, err := pgxpool.NewWithConfig(ctx, config)
		if err != nil {
			return errors.New("could not open platform database pool")
		}
		defer pool.Close()
		repository = &migration.Repository{Pool: pool}
		initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = repository.Initialize(initCtx)
		cancel()
		if err != nil {
			return errors.New("could not initialize Migration platform storage")
		}
	} else {
		slog.WarnContext(ctx, "migration.storage", "status", "unavailable")
	}

	service, closeDependencies, err := migration.MigrationService(ctx, repository)
	if err != nil {
		return err
	}
	defer closeDependencies()
	assets := os.Getenv("FRONTEND_DIST")
	if assets == "" {
		assets = "../../frontend/dist" // Run Go commands from backend/src.
	}
	router, closePlatform, err := newRouter(service, assets)
	if err != nil {
		return err
	}
	defer closePlatform()
	migration.RegisterMigrationSession(router, service)

	// Approval workflow: routes, then the queue worker. On shutdown the worker is stopped and
	// drained first, before the deferred pool closures above run.
	review := migration.NewReview(service)
	migration.RegisterReviewRoutes(router, review)
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := review.StartWorker(workerCtx, migration.DefaultWorkerConfig())
	defer func() {
		stopWorker()
		<-workerDone
	}()
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080" // Preserve the shared backend's default listener.
	}
	if err = migration.ValidateListenAddress(addr); err != nil {
		return err
	}
	server := &http.Server{
		Addr: addr, Handler: router,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 50 * time.Second, IdleTimeout: 60 * time.Second,
	}
	shutdownDone := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		select {
		case <-ctx.Done():
			clean, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := server.Shutdown(clean); err != nil {
				_ = server.Close()
			}
		case <-serverDone:
		}
	}()
	slog.InfoContext(ctx, "application.starting", "status", "started")
	err = server.ListenAndServe()
	close(serverDone)
	// Shutdown drains active requests before the deferred pool closures run.
	<-shutdownDone
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errors.New("shared HTTP server stopped unexpectedly")
	}
	return nil
}

func newRouter(service *migration.Service, assets string) (*gin.Engine, func(), error) {
	router := gin.New()
	router.Use(telemetry.HTTP, cors.Default())
	platform, err := accesscontrol.SetupAccessControl(router)
	if err != nil {
		return nil, nil, err
	}
	provider := databasemanagement.NewProvider(platform)
	if service.Access == nil {
		service.Access = migration.ApplicationAccess{Platform: platform}
	}
	if service.Connections == nil {
		service.Connections = provider
	}
	app.RegisterMigration(router, service, assets)
	return router, func() {
		provider.Close()
		if pool, err := platform.DB(); err == nil {
			_ = pool.Close()
		}
	}, nil
}
