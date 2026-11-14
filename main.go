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

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/verbeux-ai/whatsmiau/env"
	log_connect "github.com/verbeux-ai/whatsmiau/lib/log-connect"
	"github.com/verbeux-ai/whatsmiau/lib/whatsmiau"
	servermiddleware "github.com/verbeux-ai/whatsmiau/server/middleware"
	"github.com/verbeux-ai/whatsmiau/server/routes"
	"github.com/verbeux-ai/whatsmiau/services"
	"go.uber.org/zap"
)

const (
	serverShutdownTimeout = 7 * time.Second
	traceFlushTimeout     = 2 * time.Second
)

//go:generate go run github.com/swaggo/swag/cmd/swag init -g main.go -o docs
//go:generate go run ./cmd/swagger-contract

// @title           WhatsMiau API
// @version         0.3.2
// @description     Authenticated WhatsApp API compatible with Evolution API. The specification covers the complete application route surface, including canonical routes, compatibility aliases, calls and documentation delivery.

// @contact.name   Verbeux AI
// @contact.url    https://github.com/verbeux-ai/whatsmiau

// @license.name  MIT

// @host      localhost:8080
// @BasePath  /
// @schemes   http https

// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name apikey
func main() {
	if err := env.Load(); err != nil {
		panic(err)
	}

	if err := log_connect.StartLogger(); err != nil {
		log.Fatalln(err)
	}

	shutdownTracing, err := services.StartTracing(context.Background())
	if err != nil {
		zap.L().Fatal("failed to start tracing", zap.Error(err))
	}

	ctx, c := context.WithTimeout(context.Background(), 10*time.Second)
	defer c()
	whatsmiau.LoadMiau(ctx, services.SQLStore())

	app := echo.New()
	app.Pre(middleware.Recover())
	app.Pre(middleware.RemoveTrailingSlash())
	app.Pre(middleware.CORSWithConfig(middleware.CORSConfig{
		ExposeHeaders: []string{servermiddleware.TraceResponseHeader},
	}))

	routes.Load(app)

	enableUnencryptedHTTP2(app.Server)

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	go func() {
		port := ":" + env.Env.Port
		zap.L().Info("starting server...", zap.String("port", port))

		if err := app.Start(port); err != nil && !errors.Is(err, http.ErrServerClosed) {
			zap.L().Fatal("failed to start server", zap.Error(err))
		}
	}()

	<-signalCtx.Done()
	stopSignals()
	zap.L().Info("shutting down server...")

	runShutdownSteps([]shutdownStep{
		{name: "server", timeout: serverShutdownTimeout, run: app.Shutdown},
		{name: "traces", timeout: traceFlushTimeout, run: shutdownTracing},
	})
}

func enableUnencryptedHTTP2(server *http.Server) {
	server.Protocols = new(http.Protocols)
	server.Protocols.SetHTTP1(true)
	server.Protocols.SetUnencryptedHTTP2(true)
}

type shutdownStep struct {
	name    string
	timeout time.Duration
	run     func(context.Context) error
}

func runShutdownSteps(steps []shutdownStep) {
	for _, step := range steps {
		ctx, cancel := context.WithTimeout(context.Background(), step.timeout)
		if err := step.run(ctx); err != nil {
			zap.L().Error("shutdown step failed", zap.String("step", step.name), zap.Error(err))
		}
		cancel()
	}
}
