package whatsmiau

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/verbeux-ai/whatsmiau/models"
)

const proxyInstanceKeyHeader = "X-Whatsmiau-Instance-Key"

func managedProxyTransport(id string, proxy models.InstanceProxy, managedEndpoint, scope string) *http.Transport {
	endpoint := net.JoinHostPort(proxy.ProxyHost, proxy.ProxyPort)
	if id == "" || scope == "" || !strings.EqualFold(managedEndpoint, endpoint) || proxy.ProxyUsername == "" || proxy.ProxyPassword == "" {
		return nil
	}
	if proxy.ProxyProtocol != "http" && proxy.ProxyProtocol != "https" {
		return nil
	}

	key := sha256.Sum256([]byte(scope + "\x00" + id))
	proxyURL := &url.URL{
		Scheme: proxy.ProxyProtocol,
		Host:   endpoint,
		User:   url.UserPassword(proxy.ProxyUsername, proxy.ProxyPassword),
	}
	transport := (http.DefaultTransport.(*http.Transport)).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	transport.ProxyConnectHeader = http.Header{proxyInstanceKeyHeader: {"v1." + hex.EncodeToString(key[:])}}
	return transport
}
