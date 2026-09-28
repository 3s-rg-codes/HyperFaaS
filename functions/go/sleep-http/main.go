package main

import (
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"hyperfaas-ideal-arch/pkg/functionruntime"
)

var isCold int32 = 1

func main() {
	fn := functionruntime.NewHTTP()
	fn.Ready(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		time.Sleep(time.Second)
		cold := "0"
		if atomic.CompareAndSwapInt32(&isCold, 1, 0) {
			cold = "1"
		}
		w.Header().Set("X-Cold", cold)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
}
