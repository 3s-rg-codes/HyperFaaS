package main

import (
	"io"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"

	"hyperfaas-ideal-arch/pkg/functionruntime"
)

const (
	defaultFibN = 35
	maxFibN     = 40
)

var isCold int32 = 1

func main() {
	n := envBoundedInt("FIB_N", defaultFibN, maxFibN)
	fn := functionruntime.NewHTTP()
	fn.Ready(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = fib(n)
		cold := "0"
		if atomic.CompareAndSwapInt32(&isCold, 1, 0) {
			cold = "1"
		}
		w.Header().Set("X-Cold", cold)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
}

func envBoundedInt(key string, fallback, max int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return fallback
	}
	if n > max {
		return max
	}
	return n
}

func fib(n int) uint64 {
	if n < 2 {
		return uint64(n)
	}
	return fib(n-1) + fib(n-2)
}
