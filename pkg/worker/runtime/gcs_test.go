package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDownloadFromGCSAtomicAndSingleFlight(t *testing.T) {
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads.Add(1)
		_, _ = fmt.Fprint(w, "hello world")
	}))
	defer server.Close()

	origURL := gcsDownloadURL
	gcsDownloadURL = func(bucket, object string) string {
		_ = bucket
		_ = object
		return server.URL
	}
	t.Cleanup(func() { gcsDownloadURL = origURL })

	dest := filepath.Join(t.TempDir(), "artifact.ext4")
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- DownloadFromGCS(ctx, "bucket", "object", dest, logger)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("download failed: %v", err)
		}
	}

	if downloads.Load() != 1 {
		t.Fatalf("expected one HTTP download, got %d", downloads.Load())
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(data) != "hello world" {
		t.Fatalf("unexpected file contents: %q", string(data))
	}
}

func TestDownloadFromGCSRejectsIncompleteBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "20")
		_, _ = fmt.Fprint(w, "short")
	}))
	defer server.Close()

	origURL := gcsDownloadURL
	gcsDownloadURL = func(bucket, object string) string {
		return server.URL
	}
	t.Cleanup(func() { gcsDownloadURL = origURL })

	dest := filepath.Join(t.TempDir(), "artifact.ext4")
	err := DownloadFromGCS(context.Background(), "bucket", "object", dest, nil)
	if err == nil {
		t.Fatal("expected incomplete download error")
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Fatal("expected destination file to be absent after failed download")
	}
}
