// Package httpx provides outbound HTTP clients sharing one Transport so
// connections pool across fetchers, monitors and LLM providers.
package httpx

import (
	"net"
	"net/http"
	"time"
)

var transport = &http.Transport{
	Proxy: http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          100,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
}

// Client returns an *http.Client with the given overall timeout on the
// shared Transport. The same Transport must back every client or idle
// connections are duplicated per client.
func Client(timeout time.Duration) *http.Client {
	return &http.Client{Transport: transport, Timeout: timeout}
}
