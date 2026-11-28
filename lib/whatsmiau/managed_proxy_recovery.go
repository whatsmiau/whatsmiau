package whatsmiau

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	proxyRecoveryRouteHeader = "X-Whatsmiau-Recovery-Route"
	messageRetryTimeout      = 15 * time.Second
	proxyRecoveryWindow      = 30 * time.Second
)

func enableManagedProxyRecovery(instanceID, key string, instances []string) bool {
	return key != "" && instanceID != "" && (slices.Contains(instances, instanceID) || slices.Contains(instances, "*"))
}

type recoveryRouteContextKey struct{}

// Recovery state belongs to one authenticated WebSocket transport. It is
// independent of the media and pairing transports.
type managedProxyRecovery struct {
	transport http.RoundTripper

	mu           sync.Mutex
	socket       uint64
	recoverUntil time.Time
}

func newManagedProxyRecovery(primary *http.Transport, selector, secret string) *managedProxyRecovery {
	transport := primary.Clone()
	headers := primary.ProxyConnectHeader.Clone()
	transport.GetProxyConnectHeader = func(ctx context.Context, _ *url.URL, target string) (http.Header, error) {
		result := headers.Clone()
		if ctx.Value(recoveryRouteContextKey{}) == true {
			if host, port, err := net.SplitHostPort(target); err == nil && port == "443" && whatsappProxyHost(host) {
				result.Set(proxyRecoveryRouteHeader, signProxyRecovery(secret, result.Get(proxyInstanceKeyHeader), selector, target, time.Now()))
			}
		}
		return result, nil
	}
	return &managedProxyRecovery{transport: transport}
}

func whatsappProxyHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return strings.HasSuffix(host, ".whatsapp.com") || strings.HasSuffix(host, ".whatsapp.net")
}

func signProxyRecovery(secret, instanceKey, selector, target string, now time.Time) string {
	stamp := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stamp + "\n" + instanceKey + "\n" + selector + "\n" + target))
	return stamp + "." + hex.EncodeToString(mac.Sum(nil))
}

func (r *managedProxyRecovery) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || !whatsappProxyHost(req.URL.Hostname()) || !strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
		return r.transport.RoundTrip(req)
	}

	r.mu.Lock()
	recovering := time.Now().Before(r.recoverUntil)
	r.mu.Unlock()
	if recovering {
		req = req.WithContext(context.WithValue(req.Context(), recoveryRouteContextKey{}, true))
	}
	resp, err := r.transport.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		return resp, err
	}
	socket, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		return resp, nil
	}

	r.mu.Lock()
	r.socket++
	generation := r.socket
	r.mu.Unlock()
	resp.Body = &recoveryWebsocket{ReadWriteCloser: socket, onReadError: func(err error) {
		if recovering || !isRemoteTLSDecodeError(err) {
			return
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if generation == r.socket {
			r.recoverUntil = time.Now().Add(proxyRecoveryWindow)
		}
	}}
	return resp, nil
}

type recoveryWebsocket struct {
	io.ReadWriteCloser
	onReadError func(error)
}

func (s *recoveryWebsocket) Read(p []byte) (int, error) {
	n, err := s.ReadWriteCloser.Read(p)
	if err != nil {
		s.onReadError(err)
	}
	return n, err
}

func isRemoteTLSDecodeError(err error) bool {
	// Go's TCP TLS implementation wraps the private alert value in a
	// net.OpError. Restrict recovery to this remote alert, not a generic EOF.
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "remote error" && op.Err != nil && op.Err.Error() == "error decoding message"
}
