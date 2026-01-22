//go:build unit

package controller

import (
	"testing"
	"time"
)

func TestReadySignalsSignalBeforeAdd(t *testing.T) {
	signals := NewReadySignals(false)
	instanceID := "instance-1"

	signals.SignalReady(instanceID)
	signals.AddInstance(instanceID)

	done := make(chan struct{})
	go func() {
		signals.WaitReady(instanceID)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("WaitReady timed out after ready signal")
	}
}

func TestReadySignalsAddBeforeSignal(t *testing.T) {
	signals := NewReadySignals(false)
	instanceID := "instance-2"

	signals.AddInstance(instanceID)

	done := make(chan struct{})
	go func() {
		signals.WaitReady(instanceID)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("WaitReady returned before signal")
	case <-time.After(50 * time.Millisecond):
	}

	signals.SignalReady(instanceID)

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("WaitReady did not return after signal")
	}
}
