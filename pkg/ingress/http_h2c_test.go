package ingress

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// TestLeafTransportDoesNotDeadlockUnderAdmissionQueue guards the ingress-to-leaf
// hop against a single-connection HTTP/2 flow-control deadlock. The leaf admits
// a bounded number of requests, so queued streams hold unread request bodies;
// if the transport pins all streams to one connection those unread bodies can
// exhaust the connection window and stall every stream. See
// newLeafHTTP2Transport.
func TestLeafTransportDoesNotDeadlockUnderAdmissionQueue(t *testing.T) {
	const (
		concurrent = 4000
		admitted   = 50
		bodyBytes  = 4096
	)

	sem := make(chan struct{}, admitted)
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sem <- struct{}{}
		defer func() { <-sem }()
		_, _ = io.Copy(io.Discard, r.Body)
		time.Sleep(time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// Mirror the leaf's HTTP/2 server window (see pkg/leaf RunHTTPInvocationServer):
	// the admission queue leaves request bodies unread, so the connection upload
	// window must hold them or the hop deadlocks.
	server := &http.Server{Handler: h2c.NewHandler(backend, &http2.Server{
		MaxConcurrentStreams:         4096,
		MaxUploadBufferPerConnection: 32 << 20,
		MaxUploadBufferPerStream:     4 << 20,
	})}
	go server.Serve(ln)
	t.Cleanup(func() { _ = server.Close() })

	proxy := &httputil.ReverseProxy{
		Transport:     newLeafHTTP2Transport(),
		FlushInterval: -1,
		Director: func(r *http.Request) {
			r.URL.Scheme = "http"
			r.URL.Host = ln.Addr().String()
			r.Host = ln.Addr().String()
		},
	}
	var wg sync.WaitGroup
	var ok, fail atomic.Int64
	for range concurrent {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			req := httptest.NewRequest(http.MethodPost, "http://leaf/invoke", bytes.NewReader(make([]byte, bodyBytes))).WithContext(ctx)
			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, req)
			if rec.Code == http.StatusOK {
				ok.Add(1)
				return
			}
			fail.Add(1)
		}()
	}
	wg.Wait()

	if fail.Load() != 0 {
		t.Fatalf("leaf transport dropped %d/%d requests", fail.Load(), concurrent)
	}
}
