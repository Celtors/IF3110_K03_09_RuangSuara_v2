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

	accesscontrol "backend/access_control"
	databasemanagement "backend/database_management"
	"backend/internal/app"
	"backend/migration"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	var repository *migration.Repository
	if dsn := accesscontrol.PlatformDSN(); dsn != "" {
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return errors.New("invalid platform database configuration")
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
		log.Print("PLATFORM_DATABASE_URL is unset; Migration storage is unavailable")
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
	log.Printf("Shared backend listening at %s", addr)
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
	router := gin.Default()
	router.Use(cors.Default())
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
