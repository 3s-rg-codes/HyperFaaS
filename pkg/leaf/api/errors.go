package api

import (
	"fmt"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/state"
)

type WorkerDownError struct {
	WorkerID state.WorkerID
	err      error
}

func (e *WorkerDownError) Error() string {
	return fmt.Sprintf("worker %s is down, error: %v", e.WorkerID, e.err)
}
