package shared

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	echopb "hyperfaas-ideal-arch/functions/go/echo-grpc/pb"
)

func InvokeHTTP(ctx context.Context, ingressAddr string, userID, functionID uint64, body []byte) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+ingressAddr+"/invoke", bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("X-HyperFaaS-User-ID", formatUint(userID))
	req.Header.Set("X-HyperFaaS-Function-ID", formatUint(functionID))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	return out, resp.StatusCode, err
}

func InvokeGRPC(ctx context.Context, ingressAddr string, userID, functionID uint64, body []byte) ([]byte, error) {
	_ = userID
	authority := formatUint(functionID)
	conn, err := grpc.NewClient(
		"passthrough:///"+authority,
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "tcp", ingressAddr)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	resp, err := echopb.NewEchoClient(conn).Echo(ctx, &echopb.EchoRequest{Data: body})
	if err != nil {
		return nil, err
	}
	return resp.GetData(), nil
}

func formatUint(v uint64) string {
	return strconv.FormatUint(v, 10)
}

// WaitForInvoke retries HTTP invoke until success or timeout (function propagation + cold start).
func WaitForInvoke(ctx context.Context, cfg Config, userID, functionID uint64, payload []byte, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		body, status, err := InvokeHTTP(reqCtx, cfg.IngressHTTP, userID, functionID, payload)
		cancel()
		if err == nil && status == http.StatusOK && string(body) == string(payload) {
			return nil
		}
		lastErr = fmt.Errorf("status=%d err=%v body=%s", status, err, string(body))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("invoke not ready within %s: %w", timeout, lastErr)
}
