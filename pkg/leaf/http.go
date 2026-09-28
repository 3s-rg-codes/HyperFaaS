package leaf

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"hyperfaas-ideal-arch/pkg/leaf/dataplane"
)

const (
	HTTPInvokePath = "/invoke"
	HTTPHealthPath = "/healthz"

	httpHeaderUserID     = "X-HyperFaaS-User-ID"
	httpHeaderFunctionID = "X-HyperFaaS-Function-ID"
)

// FunctionRegistry is the one request-time dependency of the leaf HTTP path.
type FunctionRegistry interface {
	GetFunction(functionID uint64) *dataplane.Function
}

type sandboxTargetKey struct{}

type httpInvocationHandler struct {
	registry FunctionRegistry
	proxy    *httputil.ReverseProxy
}

func newHTTPInvocationHandler(logger *slog.Logger, registry FunctionRegistry, cfg DataplaneConfig) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	maxConns := cfg.HTTPMaxConnsPerHost
	if maxConns <= 0 {
		maxConns = dataplane.DefaultSandboxConcurrency
	}
	maxIdle := cfg.HTTPMaxIdleConns
	if maxIdle <= 0 {
		maxIdle = 4096
	}
	maxIdlePerHost := cfg.HTTPMaxIdleConnsPerHost
	if maxIdlePerHost <= 0 || maxIdlePerHost > maxConns {
		maxIdlePerHost = maxConns
	}
	dialTimeout := cfg.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = 2 * time.Second
	}
	idleTimeout := cfg.HTTPIdleConnTimeout
	if idleTimeout <= 0 {
		idleTimeout = 90 * time.Second
	}

	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        maxIdle,
		MaxIdleConnsPerHost: maxIdlePerHost,
		MaxConnsPerHost:     maxConns,
		IdleConnTimeout:     idleTimeout,
		ForceAttemptHTTP2:   false,
	}
	proxy := &httputil.ReverseProxy{
		Director: func(request *http.Request) {
			target, _ := request.Context().Value(sandboxTargetKey{}).(string)
			request.URL.Scheme = "http"
			request.URL.Host = target
			request.URL.Path = "/"
			request.Host = target
		},
		Transport:  transport,
		BufferPool: newProxyBufferPool(),
		ErrorHandler: func(w http.ResponseWriter, request *http.Request, err error) {
			if !errors.Is(err, context.Canceled) {
				logger.Warn("sandbox proxy error", "error", err)
			}
			writeSandboxProxyError(w, err)
		},
	}
	return &httpInvocationHandler{registry: registry, proxy: proxy}
}

func (h *httpInvocationHandler) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if request.URL.Path == HTTPHealthPath {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
		return
	}
	if request.URL.Path != HTTPInvokePath {
		http.NotFound(w, request)
		return
	}
	if request.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	functionID, err := parseInvocationHeader(request, httpHeaderFunctionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := parseInvocationHeader(request, httpHeaderUserID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	function := h.registry.GetFunction(functionID)
	if function == nil {
		http.Error(w, "function not found", http.StatusNotFound)
		return
	}
	if function.Protocol != "http" {
		http.Error(w, "function protocol is not HTTP", http.StatusNotImplemented)
		return
	}

	ctx := request.Context()
	if function.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, function.RequestTimeout)
		defer cancel()
	}
	lease, err := function.Pool.Acquire(ctx)
	if err != nil {
		writePoolError(w, err)
		return
	}
	defer lease.Release()

	target := lease.Sandbox()
	h.proxy.ServeHTTP(w, request.WithContext(context.WithValue(ctx, sandboxTargetKey{}, target.Address)))
}

func parseInvocationHeader(request *http.Request, name string) (uint64, error) {
	value := strings.TrimSpace(request.Header.Get(name))
	if value == "" {
		return 0, errors.New(name + " header is required")
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 {
		return 0, errors.New(name + " must be a positive integer")
	}
	return parsed, nil
}

func writePoolError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		return
	case errors.Is(err, context.DeadlineExceeded):
		http.Error(w, "invocation timed out", http.StatusGatewayTimeout)
	case errors.Is(err, dataplane.ErrQueueFull):
		http.Error(w, err.Error(), http.StatusTooManyRequests)
	case errors.Is(err, dataplane.ErrPoolClosed):
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	default:
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	}
}

func writeSandboxProxyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		return
	case errors.Is(err, context.DeadlineExceeded):
		http.Error(w, "sandbox invocation timed out", http.StatusGatewayTimeout)
		return
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		http.Error(w, "sandbox invocation timed out", http.StatusGatewayTimeout)
		return
	}
	http.Error(w, "sandbox unavailable", http.StatusBadGateway)
}

// proxyBufferPool recycles the 32 KiB copy buffers used by ReverseProxy. Without
// it every proxied request allocates a fresh buffer, which dominates allocation
// and GC cost on the leaf hot path.
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

func RunHTTPInvocationServer(ctx context.Context, logger *slog.Logger, address string, handler http.Handler) error {
	server := &http.Server{
		Addr: address,
		Handler: h2c.NewHandler(handler, &http2.Server{
			MaxConcurrentStreams: 4096,
			// The leaf admits only a bounded number of requests per sandbox, so
			// during a burst many multiplexed streams wait with their request
			// bodies not yet read. Go's default connection upload window is too
			// small to hold those bodies; once it is exhausted, no stream on the
			// connection can make progress and the whole hop deadlocks. Advertise
			// a window large enough to cover the in-flight streams for typical
			// function payloads so admission backpressure, not HTTP/2 flow
			// control, is the limit. The window is a ceiling, not an allocation.
			MaxUploadBufferPerConnection: 32 << 20,
			MaxUploadBufferPerStream:     4 << 20,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) {
			return ctx.Err()
		}
		return err
	case err := <-errCh:
		return err
	}
}
