package main

import (
	"io"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"hyperfaas-ideal-arch/pkg/functionruntime"
)

var isCold int32 = 1

func main() {
	memoryBytes := envMiB("MEMORY_MB", 256) * 1024 * 1024
	holdFor := envDurationMillis("HOLD_MS", 250*time.Millisecond)
	fn := functionruntime.NewHTTP()
	fn.Ready(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		buf := make([]byte, memoryBytes)
		for i := 0; i < len(buf); i += 4096 {
			buf[i] = byte(i)
		}
		if holdFor > 0 {
			time.Sleep(holdFor)
		}

		cold := "0"
		if atomic.CompareAndSwapInt32(&isCold, 1, 0) {
			cold = "1"
		}
		w.Header().Set("X-Cold", cold)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		_ = buf
	}))
}

func envMiB(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

func envDurationMillis(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	ms, err := strconv.Atoi(value)
	if err != nil || ms < 0 {
		return fallback
	}
	return time.Duration(ms) * time.Millisecond
}
