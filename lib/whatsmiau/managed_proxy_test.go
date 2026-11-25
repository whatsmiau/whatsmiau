package whatsmiau

import (
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/verbeux-ai/whatsmiau/models"
)

func TestManagedProxySendsStableInstanceKeyWithExistingCredentials(t *testing.T) {
	type attempt struct {
		method, key, auth string
	}
	requests := make(chan attempt, 4)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- attempt{r.Method, r.Header.Get(proxyInstanceKeyHeader), r.Header.Get("Proxy-Authorization")}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxyServer.Close()
	host, port, err := net.SplitHostPort(proxyServer.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	proxy := models.InstanceProxy{ProxyHost: host, ProxyPort: port, ProxyProtocol: "http", ProxyUsername: "shared-route", ProxyPassword: "test-key"}
	endpoint := net.JoinHostPort(host, port)
	var keys []string
	for _, value := range []struct{ id, scope string }{{"existing", "core-0"}, {"existing", "core-0"}, {"other", "core-0"}, {"existing", "core-1"}} {
		transport := managedProxyTransport(value.id, proxy, endpoint, value.scope)
		if transport == nil {
			t.Fatal("managed transport missing")
		}
		client := &http.Client{Transport: transport, Timeout: time.Second}
		if resp, err := client.Get("https://origin.invalid/"); err == nil {
			resp.Body.Close()
			t.Fatal("fixture must stop after CONNECT")
		}
		transport.CloseIdleConnections()
		select {
		case got := <-requests:
			if got.method != http.MethodConnect || !strings.HasPrefix(got.key, "v1.") || len(got.key) != 67 {
				t.Fatalf("invalid CONNECT identity: %+v", got)
			}
			if got.auth != "Basic "+base64.StdEncoding.EncodeToString([]byte("shared-route:test-key")) {
				t.Fatal("existing proxy authentication changed")
			}
			keys = append(keys, got.key)
		default:
			t.Fatal("CONNECT did not reach the proxy")
		}
	}
	if keys[0] != keys[1] || keys[0] == keys[2] || keys[0] == keys[3] {
		t.Fatal("identity must be stable per instance and distinct across IDs and cores")
	}
	if managedProxyTransport("existing", proxy, "unmanaged.invalid:1234", "core-0") != nil {
		t.Fatal("identity propagated to a different endpoint")
	}
	if managedProxyTransport("existing", proxy, endpoint, "") != nil {
		t.Fatal("identity enabled without configuration")
	}
}
