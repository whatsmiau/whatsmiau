package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

const (
	shortStepTimeout            = 50 * time.Millisecond
	handlerReleaseDelay         = 100 * time.Millisecond
	inFlightShutdownTimeout     = 2 * time.Second
	stuckRequestShutdownTimeout = 200 * time.Millisecond
	hangGuardTimeout            = 5 * time.Second
)

func requireShutdownStepsReturn(t *testing.T, steps []shutdownStep) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		runShutdownSteps(steps)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(hangGuardTimeout):
		t.Fatal("shutdown steps did not return")
	}
}

func TestRunShutdownStepsRunsStepsInOrder(t *testing.T) {
	var executed []string
	recordStep := func(name string) shutdownStep {
		return shutdownStep{name: name, timeout: shortStepTimeout, run: func(context.Context) error {
			executed = append(executed, name)
			return nil
		}}
	}

	runShutdownSteps([]shutdownStep{recordStep("server"), recordStep("traces")})

	if !slices.Equal(executed, []string{"server", "traces"}) {
		t.Fatalf("executed steps = %v, want [server traces]", executed)
	}
}

func TestRunShutdownStepsFlushesTracesAfterServerShutdownFails(t *testing.T) {
	tracesFlushed := false

	runShutdownSteps([]shutdownStep{
		{name: "server", timeout: shortStepTimeout, run: func(context.Context) error {
			return errors.New("server shutdown failed")
		}},
		{name: "traces", timeout: shortStepTimeout, run: func(context.Context) error {
			tracesFlushed = true
			return nil
		}},
	})

	if !tracesFlushed {
		t.Fatal("traces were not flushed after the server shutdown failed")
	}
}

func TestRunShutdownStepsMovesOnWhenStepBlocksUntilDeadline(t *testing.T) {
	tracesFlushed := false

	requireShutdownStepsReturn(t, []shutdownStep{
		{name: "server", timeout: shortStepTimeout, run: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}},
		{name: "traces", timeout: shortStepTimeout, run: func(context.Context) error {
			tracesFlushed = true
			return nil
		}},
	})

	if !tracesFlushed {
		t.Fatal("traces were not flushed after the server step hit its deadline")
	}
}

type blockingHandler struct {
	entered     chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
	proto       string
	finished    atomic.Bool
}

func (h *blockingHandler) serve(c echo.Context) error {
	h.proto = c.Request().Proto
	close(h.entered)
	<-h.release
	defer h.finished.Store(true)
	return c.NoContent(http.StatusNoContent)
}

func (h *blockingHandler) unblock() {
	h.releaseOnce.Do(func() { close(h.release) })
}

func startServerWithBlockedRequest(t *testing.T, client *http.Client) (*echo.Echo, *blockingHandler) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	app := echo.New()
	app.HideBanner = true
	app.HidePort = true
	app.Listener = listener
	handler := &blockingHandler{entered: make(chan struct{}), release: make(chan struct{})}
	app.GET("/blocked", handler.serve)
	enableUnencryptedHTTP2(app.Server)

	serveDone := make(chan struct{})
	go func() {
		_ = app.Start(listener.Addr().String())
		close(serveDone)
	}()

	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, err := client.Get("http://" + listener.Addr().String() + "/blocked")
		if err == nil {
			_ = response.Body.Close()
		}
	}()

	t.Cleanup(func() {
		handler.unblock()
		_ = app.Close()
		<-serveDone
		<-requestDone
		client.CloseIdleConnections()
	})

	select {
	case <-handler.entered:
	case <-time.After(hangGuardTimeout):
		t.Fatal("request never reached the handler")
	}
	return app, handler
}

func newHTTP2PriorKnowledgeClient() *http.Client {
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	return &http.Client{Timeout: hangGuardTimeout, Transport: &http.Transport{Protocols: protocols}}
}

func newHTTP1Client() *http.Client {
	return &http.Client{Timeout: hangGuardTimeout, Transport: &http.Transport{}}
}

func shutdownDuringBlockedRequest(t *testing.T, client *http.Client) (proto string, handlerFinishedAtFlush bool) {
	t.Helper()
	app, handler := startServerWithBlockedRequest(t, client)
	app.Server.RegisterOnShutdown(func() { time.AfterFunc(handlerReleaseDelay, handler.unblock) })

	requireShutdownStepsReturn(t, []shutdownStep{
		{name: "server", timeout: inFlightShutdownTimeout, run: app.Shutdown},
		{name: "traces", timeout: shortStepTimeout, run: func(context.Context) error {
			handlerFinishedAtFlush = handler.finished.Load()
			return nil
		}},
	})
	return handler.proto, handlerFinishedAtFlush
}

func TestShutdownWaitsForInFlightHTTP2RequestBeforeFlushingTraces(t *testing.T) {
	proto, handlerFinishedAtFlush := shutdownDuringBlockedRequest(t, newHTTP2PriorKnowledgeClient())

	if proto != "HTTP/2.0" {
		t.Fatalf("request proto = %q, want HTTP/2.0", proto)
	}
	if !handlerFinishedAtFlush {
		t.Fatal("traces were flushed before the in-flight HTTP/2 request finished")
	}
}

func TestShutdownWaitsForInFlightHTTP1RequestBeforeFlushingTraces(t *testing.T) {
	proto, handlerFinishedAtFlush := shutdownDuringBlockedRequest(t, newHTTP1Client())

	if proto != "HTTP/1.1" {
		t.Fatalf("request proto = %q, want HTTP/1.1", proto)
	}
	if !handlerFinishedAtFlush {
		t.Fatal("traces were flushed before the in-flight HTTP/1.1 request finished")
	}
}

func TestShutdownFlushesTracesWhenInFlightRequestOutlivesDeadline(t *testing.T) {
	app, _ := startServerWithBlockedRequest(t, newHTTP2PriorKnowledgeClient())
	tracesFlushed := false

	requireShutdownStepsReturn(t, []shutdownStep{
		{name: "server", timeout: stuckRequestShutdownTimeout, run: app.Shutdown},
		{name: "traces", timeout: shortStepTimeout, run: func(context.Context) error {
			tracesFlushed = true
			return nil
		}},
	})

	if !tracesFlushed {
		t.Fatal("traces were not flushed after the stuck request outlived the shutdown deadline")
	}
}
