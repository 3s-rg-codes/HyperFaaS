package ingress

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/http2"

	"hyperfaas-ideal-arch/pkg/ingress/routing"
)

const (
	headerUserID     = "X-HyperFaaS-User-ID"
	headerFunctionID = "X-HyperFaaS-Function-ID"
)

type ingressTargetKey struct{}

// newLeafHTTP2Transport builds the cleartext-HTTP/2 transport used for the
// ingress-to-leaf hop.
//
// StrictMaxConcurrentStreams is deliberately left off. Pinning every stream to
// one connection deadlocks the hop under load: the leaf admits only a bounded
// number of requests at a time, so queued streams hold request bodies that the
// leaf has not read yet; once those unread bodies exhaust the connection-level
// flow-control window, no stream on that connection can make progress. Allowing
// the transport to open additional multiplexed connections when a connection
// reaches its stream limit keeps the hop bounded (roughly one connection per
// server-advertised MAX_CONCURRENT_STREAMS in flight) without head-of-line
// blocking. It still never opens a connection per invocation.
func newLeafHTTP2Transport() *http2.Transport {
	dialer := &net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}
	return &http2.Transport{
		AllowHTTP:       true,
		ReadIdleTimeout: 30 * time.Second,
		PingTimeout:     5 * time.Second,
		DialTLSContext: func(ctx context.Context, network, address string, _ *tls.Config) (net.Conn, error) {
			return dialer.DialContext(ctx, network, address)
		},
	}
}

// Leaf transport arms. h2c is the production default; the HTTP/1.1 arms exist
// to compare the multiplexed-stream model against the per-connection model that
// a conventional L7 proxy (for example HAProxy in HTTP mode) uses.
const (
	// LeafTransportH2C is the cleartext-HTTP/2 multiplexed hop (default).
	LeafTransportH2C = "h2c"
	// LeafTransportHTTP1 is cleartext HTTP/1.1 with a persistent connection
	// pool: one connection per in-flight request, reused while idle.
	LeafTransportHTTP1 = "http1"
	// LeafTransportHTTP1NoKeepAlive is cleartext HTTP/1.1 with keep-alive
	// disabled, i.e. a fresh connection per request. This mirrors HAProxy's
	// `option http-server-close` server-side behaviour.
	LeafTransportHTTP1NoKeepAlive = "http1-nokeepalive"
)

// newLeafHTTP1Transport builds the HTTP/1.1 pool used for the experimental
// ingress-to-leaf arms. MaxConnsPerHost is left at zero (unbounded) so the pool
// grows to one connection per in-flight request instead of queueing behind a
// fixed connection count; keepAlive=false disables reuse entirely. The leaf's
// h2c server also serves HTTP/1.1, so no leaf change is required.
func newLeafHTTP1Transport(keepAlive bool) *http.Transport {
	dialer := &net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		DialContext:         dialer.DialContext,
		DisableKeepAlives:   !keepAlive,
		MaxIdleConns:        20000,
		MaxIdleConnsPerHost: 20000,
		MaxConnsPerHost:     0,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   false,
	}
}

// newLeafTransport selects the ingress-to-leaf RoundTripper for a transport arm.
// An empty or unknown kind selects the h2c default; the value is validated at
// config load, so this fallback only matters for programmatic callers.
func newLeafTransport(kind string) http.RoundTripper {
	switch kind {
	case LeafTransportHTTP1:
		return newLeafHTTP1Transport(true)
	case LeafTransportHTTP1NoKeepAlive:
		return newLeafHTTP1Transport(false)
	default:
		return newLeafHTTP2Transport()
	}
}

type httpInvokeHandler struct {
	selector LeafSelector
	proxy    *httputil.ReverseProxy
}

func newHTTPInvokeHandler(logger *slog.Logger, selector LeafSelector, leafTransport string) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	transport := newLeafTransport(leafTransport)
	proxy := &httputil.ReverseProxy{
		Director: func(r *http.Request) {
			target, _ := r.Context().Value(ingressTargetKey{}).(string)
			r.URL.Scheme = "http"
			r.URL.Host = target
			r.Host = target
		},
		Transport:     transport,
		FlushInterval: -1,
		BufferPool:    newProxyBufferPool(),
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if !errors.Is(err, context.Canceled) {
				logger.Warn("ingress leaf proxy error", "error", err)
			}
			writeIngressProxyError(w, err)
		},
	}
	return &httpInvokeHandler{
		selector: selector,
		proxy:    proxy,
	}
}

func (h *httpInvokeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/invoke" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	h.serveStreaming(w, r)
}

// serveStreaming selects one leaf from the immutable routing snapshot and
// streams the original request to the leaf's HTTP invocation address. It never
// replays an invocation to a second leaf.
func (h *httpInvokeHandler) serveStreaming(w http.ResponseWriter, r *http.Request) {
	userID, err := parseRequiredUint64Header(r, headerUserID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	functionID, err := parseRequiredUint64Header(r, headerFunctionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.selector == nil {
		http.Error(w, "routing not configured", http.StatusServiceUnavailable)
		return
	}

	target, err := h.selector.Pick(routing.RouteRequest{
		UserID:     userID,
		FunctionID: functionID,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	if target.HTTPAddress == "" {
		http.Error(w, fmt.Sprintf("leaf %d has no http invocation address", target.LeafID), http.StatusServiceUnavailable)
		return
	}
	h.proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ingressTargetKey{}, target.HTTPAddress)))
}

// writeIngressProxyError maps a failed proxy attempt to a stable status. A dial
// failure happened before the leaf could accept the request, so it is a 503. A
// failure after the request may have been dispatched is a 502 and is never
// replayed.
func writeIngressProxyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		return
	case errors.Is(err, context.DeadlineExceeded):
		http.Error(w, "leaf invocation timed out", http.StatusGatewayTimeout)
		return
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		http.Error(w, "leaf unavailable", http.StatusServiceUnavailable)
		return
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EHOSTUNREACH) {
		http.Error(w, "leaf unavailable", http.StatusServiceUnavailable)
		return
	}
	http.Error(w, "leaf proxy error", http.StatusBadGateway)
}

// proxyBufferPool recycles the 32 KiB copy buffers used by ReverseProxy. Without
// it every proxied request allocates a fresh buffer, which dominates allocation
// and GC cost on the ingress hot path.
type proxyBufferPool struct {
	pool sync.Pool
}

func newProxyBufferPool() *proxyBufferPool {
	pool := &proxyBufferPool{}
	pool.pool.New = func() any { return make([]byte, 32*1024) }
	return pool
}

func (p *proxyBufferPool) Get() []byte { return p.pool.Get().([]byte) }

func (p *proxyBufferPool) Put(buf []byte) { p.pool.Put(buf) }

func parseRequiredUint64Header(r *http.Request, name string) (uint64, error) {
	value := strings.TrimSpace(r.Header.Get(name))
	if value == "" {
		return 0, fmt.Errorf("%s header is required", name)
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}
