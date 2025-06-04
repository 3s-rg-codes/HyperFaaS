package keyValueStore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

type Store struct {
	Lock sync.RWMutex
	Data map[string]PostRequest
}

type PostRequest struct {
	ImageTag string `json:"image_tag"`
	Config   Config `json:"config"`
}

type PostResponse struct {
	FunctionID string `json:"function_id"`
}

type Config struct {
	MemLimit       int   `json:"mem_limit"`
	CpuQuota       int   `json:"cpu_quota"`
	CpuPeriod      int   `json:"cpu_period"`
	Timeout        int32 `json:"timeout"`
	MaxConcurrency int32 `json:"max_concurrency"`
}

func (t *Store) Start() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			t.handleGet(w, r)
		case http.MethodPost:
			t.handlePost(w, r)
		default:
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})

	server := http.Server{
		Addr:    ":8999",
		Handler: nil,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		fmt.Println("Server starting on :8999")
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Printf("Server error: %v\n", err)
		}
	}()

	<-stop
	fmt.Println("Gracefully shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		fmt.Printf("Server forced to shutdown: %v\n", err)
	}

	fmt.Println("Server gracefully stopped")
}

func (t *Store) handleGet(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Get request received")
	b, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			fmt.Printf("error closing the response body, %v", err)
		}
	}(r.Body)

	t.Lock.RLock()
	defer t.Lock.RUnlock()

	functionID := string(b)                 //get requested id
	if data, ok := t.Data[functionID]; ok { //look in db to see if we have data for id
		w.WriteHeader(http.StatusOK)
		jsonData, err := json.Marshal(data)
		if err != nil {
			http.Error(w, "Could not marshal json", http.StatusInternalServerError)
		}
		_, err = w.Write(jsonData)
		if err != nil {
			fmt.Printf("Error writing data %v\n", err)
		}
		return
	}

	http.Error(w, "Not Found", http.StatusNotFound)
}

func (t *Store) handlePost(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Post request received")
	//read request body
	b, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			fmt.Printf("error closing the response body, %v", err)
		}
	}(r.Body)

	// Parse JSON request
	var p PostRequest
	if err := json.Unmarshal(b, &p); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	t.Lock.Lock()
	defer t.Lock.Unlock()

	unique := uuid.New().String()

	t.Data[unique] = p

	resp := &PostResponse{FunctionID: unique}

	jsonData, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusCreated)
	_, err = w.Write(jsonData)
	if err != nil {
		fmt.Printf("Error writing response: %v\n", err)
	}
}
