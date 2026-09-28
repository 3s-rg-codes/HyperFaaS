package main

import "testing"

func TestFib(t *testing.T) {
	if got := fib(10); got != 55 {
		t.Fatalf("fib(10)=%d want 55", got)
	}
}

func TestEnvBoundedIntClamps(t *testing.T) {
	t.Setenv("FIB_N", "99")
	if got := envBoundedInt("FIB_N", defaultFibN, maxFibN); got != maxFibN {
		t.Fatalf("got %d want %d", got, maxFibN)
	}
}
