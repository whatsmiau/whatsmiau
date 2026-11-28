package whatsmiau

import (
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/verbeux-ai/whatsmiau/models"
)

type recoveryRoundTripFunc func(*http.Request) (*http.Response, error)

func (f recoveryRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type websocketFailure struct {
	err error
}

func (s websocketFailure) Read([]byte) (int, error)  { return 0, s.err }
func (websocketFailure) Write(p []byte) (int, error) { return len(p), nil }
func (websocketFailure) Close() error                { return nil }

func TestManagedWebsocketRequestsFallbackAfterRemoteTLSAlert(t *testing.T) {
	alert := &net.OpError{Op: "remote error", Err: errors.New("error decoding message")}
	var reconnects []bool
	var socketErr error = alert
	recovery := &managedProxyRecovery{transport: recoveryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		reconnects = append(reconnects, req.Context().Value(recoveryRouteContextKey{}) == true)
		return &http.Response{StatusCode: http.StatusSwitchingProtocols, Body: websocketFailure{err: socketErr}}, nil
	})}
	req := httptest.NewRequest(http.MethodGet, "https://web.whatsapp.com/ws/chat", nil)
	req.Header.Set("Upgrade", "websocket")

	initial, err := recovery.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initial.Body.Read(make([]byte, 8)); !errors.Is(err, alert) {
		t.Fatalf("the original transport error must reach the client: %v", err)
	}
	fallback, err := recovery.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	// Failure on the recovery request must not indefinitely extend the window.
	recovery.recoverUntil = time.Time{}
	_, _ = fallback.Body.Read(make([]byte, 8))
	socketErr = io.EOF
	normal, err := recovery.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = normal.Body.Read(make([]byte, 8))
	_, _ = recovery.RoundTrip(req)
	if want := []bool{false, true, false, false}; !reflect.DeepEqual(reconnects, want) {
		t.Fatalf("fallback requested %v, want %v", reconnects, want)
	}
}

func TestManagedWebsocketSendsSignedRecoveryOnlyOnEligibleCONNECT(t *testing.T) {
	type requestHeaders struct{ selector, key, signature string }
	seen := make(chan requestHeaders, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- requestHeaders{r.Header.Get("Proxy-Authorization"), r.Header.Get(proxyInstanceKeyHeader), r.Header.Get(proxyRecoveryRouteHeader)}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	host, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	cfg := models.InstanceProxy{ProxyHost: host, ProxyPort: port, ProxyProtocol: "http", ProxyUsername: "fixture-selector", ProxyPassword: "existing-test-key"}
	transport := managedProxyTransport("fixture-instance", cfg, server.Listener.Addr().String(), "fixture-scope")
	const secret = "fixture-only-recovery-secret"
	recovery := newManagedProxyRecovery(transport, cfg.ProxyUsername, secret)
	recovery.recoverUntil = time.Now().Add(time.Minute)
	t.Cleanup(transport.CloseIdleConnections)
	t.Cleanup(recovery.transport.(*http.Transport).CloseIdleConnections)
	for _, tc := range []struct {
		target  string
		upgrade bool
		signed  bool
	}{
		{"https://web.whatsapp.com/ws/chat", true, true},
		{"https://web.whatsapp.com/path", false, false},
		{"https://web.whatsapp.com.example.net/ws", true, false},
		{"https://web.whatsapp.com:444/ws", true, false},
	} {
		req, err := http.NewRequest(http.MethodGet, tc.target, nil)
		if err != nil {
			t.Fatal(err)
		}
		if tc.upgrade {
			req.Header.Set("Upgrade", "websocket")
		}
		client := &http.Client{Transport: recovery, Timeout: time.Second}
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
			t.Fatal("fixture should reject CONNECT")
		}
		var h requestHeaders
		select {
		case h = <-seen:
		case <-time.After(time.Second):
			t.Fatalf("CONNECT did not reach the proxy for %s", tc.target)
		}
		if h.selector != "Basic "+base64.StdEncoding.EncodeToString([]byte("fixture-selector:existing-test-key")) || h.key != transport.ProxyConnectHeader.Get(proxyInstanceKeyHeader) {
			t.Fatal("existing authentication and instance identity must survive")
		}
		if (h.signature != "") != tc.signed {
			t.Fatalf("unexpected recovery signature for %s", tc.target)
		}
	}

	// Fixture shared with the receiver's signature verification test.
	key := "v1." + strings.Repeat("a", 64)
	token := signProxyRecovery(secret, key, cfg.ProxyUsername, "web.whatsapp.com:443", time.Unix(1795818100, 0))
	if token != "1795818100.f61650ec94be8fb22820629538746204aa6be34fa69c1928f11633c93d0aa51f" {
		t.Fatal("the wire signature changed")
	}
}

func TestManagedProxyRecoveryNeedsOptInForAnInstance(t *testing.T) {
	instance := "example-session"
	for _, tc := range []struct {
		secret    string
		instances []string
		want      bool
	}{
		{"secret", nil, false},
		{"", []string{instance}, false},
		{"secret", []string{"different-session"}, false},
		{"secret", []string{instance}, true},
		{"secret", []string{"*"}, true},
	} {
		if got := enableManagedProxyRecovery(instance, tc.secret, tc.instances); got != tc.want {
			t.Fatalf("opt-in %v produced %t, want %t", tc.instances, got, tc.want)
		}
	}
}
