package fake

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sort"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	echopb "hyperfaas-ideal-arch/functions/go/echo-grpc/pb"
	"hyperfaas-ideal-arch/pkg/core"
)

func TestFakeEchoHTTP(t *testing.T) {
	rt := New(Config{}, nil)
	state, err := rt.Start(context.Background(), &core.StartSandboxRequest{
		InstanceId: 1,
		WorkerId:   1,
		Function: &core.FunctionSpec{
			FunctionId: 10,
			Runtime:    &core.RuntimeSpec{Image: "echo-http", Protocol: "http"},
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = rt.Stop(context.Background(), 1) }()

	if !state.GetReady() {
		t.Fatal("expected ready")
	}

	resp, err := http.Post("http://"+state.GetAddress()+"/", "application/octet-stream", bytes.NewReader([]byte("ping")))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if string(body) != "ping" {
		t.Fatalf("body=%q", body)
	}
	if resp.Header.Get("X-Cold") != "1" {
		t.Fatalf("X-Cold=%q want 1", resp.Header.Get("X-Cold"))
	}
}

func TestFakeEchoGRPC(t *testing.T) {
	rt := New(Config{}, nil)
	state, err := rt.Start(context.Background(), &core.StartSandboxRequest{
		InstanceId: 2,
		WorkerId:   1,
		Function: &core.FunctionSpec{
			FunctionId: 11,
			Runtime:    &core.RuntimeSpec{Image: "echo-grpc", Protocol: "grpc"},
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = rt.Stop(context.Background(), 2) }()

	conn, err := grpc.NewClient(state.GetAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	client := echopb.NewEchoClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := client.Echo(ctx, &echopb.EchoRequest{Data: []byte("grpc-ping")})
	if err != nil {
		t.Fatalf("Echo: %v", err)
	}
	if string(resp.GetData()) != "grpc-ping" {
		t.Fatalf("data=%q", resp.GetData())
	}
	if !resp.GetCold() {
		t.Fatal("expected cold=true on first call")
	}
}

func TestFakeHasImage(t *testing.T) {
	rt := New(Config{}, nil)
	for _, image := range []string{"echo-http", "echo-grpc", "sleep-http", "fib-http", "echo-http:latest"} {
		ok, err := rt.HasImage(context.Background(), image)
		if err != nil || !ok {
			t.Fatalf("HasImage(%q)=%v,%v", image, ok, err)
		}
	}
	ok, err := rt.HasImage(context.Background(), "unknown")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("unknown image should be false")
	}
}

func TestSampleStartLatencyPercentiles(t *testing.T) {
	const n = 5000
	samples := make([]time.Duration, n)
	for i := range samples {
		samples[i] = sampleStartLatency()
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })

	p50 := samples[n*50/100]
	p90 := samples[n*90/100]
	p99 := samples[n*99/100]

	// Loose bands around the target knots — distribution is piecewise-linear, not exact.
	if p50 < 50*time.Millisecond || p50 > 200*time.Millisecond {
		t.Fatalf("p50=%s want ~100ms", p50)
	}
	if p90 < 2*time.Second || p90 > 5*time.Second {
		t.Fatalf("p90=%s want ~3s", p90)
	}
	if p99 < 15*time.Second || p99 > 30*time.Second {
		t.Fatalf("p99=%s want ~20s", p99)
	}
}
