package ingress

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"hyperfaas-ideal-arch/pkg/ingress/routing"
)

// newLeafLikeServer starts an h2c server configured with the same HTTP/2
// windows as the real leaf (see pkg/leaf RunHTTPInvocationServer) and returns
// its host:port. The leaf serves HTTP/1.1 and h2c on the same port, so this one
// backend exercises every transport arm.
func newLeafLikeServer(tb testing.TB, handler http.Handler) string {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	server := &http.Server{Handler: h2c.NewHandler(handler, &http2.Server{
		MaxConcurrentStreams:         4096,
		MaxUploadBufferPerConnection: 32 << 20,
		MaxUploadBufferPerStream:     4 << 20,
	})}
	go func() { _ = server.Serve(ln) }()
	tb.Cleanup(func() { _ = server.Close() })
	return ln.Addr().String()
}

// newTransportHandler builds the public invoke handler pointed at addr using the
// given ingress-to-leaf transport arm.
func newTransportHandler(tb testing.TB, addr, kind string) http.Handler {
	tb.Helper()
	return newHTTPInvokeHandler(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		fixedSelector{target: routing.LeafTarget{LeafID: 1, HTTPAddress: addr}},
		kind,
	)
}

// fixedSelector routes every request to one leaf. It stands in for the routing
// engine in tests that only exercise the proxy transport.
type fixedSelector struct {
	target routing.LeafTarget
}

func (f fixedSelector) Pick(routing.RouteRequest) (routing.LeafTarget, error) {
	return f.target, nil
}

func transportArms() []struct{ name, kind string } {
	return []struct{ name, kind string }{
		{"h2c", LeafTransportH2C},
		{"http1", LeafTransportHTTP1},
		{"http1-nokeepalive", LeafTransportHTTP1NoKeepAlive},
	}
}

// TestLeafTransportArmsUnderAdmissionQueue extends the h2c deadlock guard to
// every arm: a bounded-admission leaf must not stall the hop, and every request
// must complete for h2c and for both HTTP/1.1 connection models.
func TestLeafTransportArmsUnderAdmissionQueue(t *testing.T) {
	const (
		concurrent = 256
		admitted   = 20
		bodyBytes  = 4096
	)
	sem := make(chan struct{}, admitted)
	addr := newLeafLikeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sem <- struct{}{}
		defer func() { <-sem }()
		_, _ = io.Copy(io.Discard, r.Body)
		time.Sleep(time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	}))

	for _, arm := range transportArms() {
		t.Run(arm.name, func(t *testing.T) {
			handler := newTransportHandler(t, addr, arm.kind)
			var wg sync.WaitGroup
			var fail atomic.Int64
			for i := 0; i < concurrent; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					req := httptest.NewRequest(http.MethodPost, "http://leaf/invoke", bytes.NewReader(make([]byte, bodyBytes))).WithContext(ctx)
					req.Header.Set(headerUserID, "1")
					req.Header.Set(headerFunctionID, "7")
					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, req)
					if rec.Code != http.StatusOK {
						fail.Add(1)
					}
				}()
			}
			wg.Wait()
			if fail.Load() != 0 {
				t.Fatalf("%s dropped %d/%d requests", arm.name, fail.Load(), concurrent)
			}
		})
	}
}

// BenchmarkLeafTransport measures the ingress-to-leaf hop in isolation for each
// arm under a fixed number of parallel callers. It includes the ingress ReverseProxy
// and the selected transport but excludes the generator and the leaf's sandbox
// hop, so it isolates transport protocol/connection cost.
func BenchmarkLeafTransport(b *testing.B) {
	addr := newLeafLikeServer(b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte("ok"))
	}))
	for _, arm := range transportArms() {
		b.Run(arm.name, func(b *testing.B) {
			handler := newTransportHandler(b, addr, arm.kind)
			payload := make([]byte, 1<<10)
			b.SetParallelism(16)
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					req := httptest.NewRequest(http.MethodPost, "http://leaf/invoke", bytes.NewReader(payload))
					req.Header.Set(headerUserID, "1")
					req.Header.Set(headerFunctionID, "7")
					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, req)
					if rec.Code != http.StatusOK {
						b.Errorf("status %d", rec.Code)
						return
					}
				}
			})
		})
	}
}
