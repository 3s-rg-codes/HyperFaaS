package ingress

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"hyperfaas-ideal-arch/pkg/ingress/routing"
)

const benchFunctionID = uint64(7)

func benchStreamingHandler(b *testing.B) http.Handler {
	b.Helper()
	backend := httptest.NewUnstartedServer(h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Discard the request body without buffering it: the benchmark measures
		// ingress-side allocations, and the backend runs in the same process.
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}), &http2.Server{}))
	backend.Start()
	b.Cleanup(backend.Close)

	return newHTTPInvokeHandler(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		fixedSelector{target: routing.LeafTarget{
			LeafID:      1,
			HTTPAddress: strings.TrimPrefix(backend.URL, "http://"),
		}},
		LeafTransportH2C,
	)
}

// BenchmarkHTTPInvoke measures the public HTTP invoke handler for 0-byte,
// 1 KiB and 1 MiB request bodies on the streaming path. Per-request bytes/op
// should stay bounded and independent of body size (no full-body buffering).
func BenchmarkHTTPInvoke(b *testing.B) {
	runHTTPInvokeBench(b, benchStreamingHandler(b))
}

func runHTTPInvokeBench(b *testing.B, handler http.Handler) {
	b.Helper()
	srv := httptest.NewServer(handler)
	defer srv.Close()
	client := srv.Client()

	bodies := []struct {
		name string
		size int
	}{
		{"0B", 0},
		{"1KiB", 1 << 10},
		{"1MiB", 1 << 20},
	}
	for _, tc := range bodies {
		payload := bytes.Repeat([]byte("a"), tc.size)
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(tc.size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				req, err := http.NewRequest(http.MethodPost, srv.URL+"/invoke", bytes.NewReader(payload))
				if err != nil {
					b.Fatal(err)
				}
				req.Header.Set(headerUserID, "1")
				req.Header.Set(headerFunctionID, "7")
				resp, err := client.Do(req)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := io.Copy(io.Discard, resp.Body); err != nil {
					b.Fatal(err)
				}
				_ = resp.Body.Close()
			}
		})
	}
}
