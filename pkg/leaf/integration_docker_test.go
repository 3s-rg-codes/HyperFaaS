//go:build integration

package leaf_test

/*
Leaf Docker integration tests require a running Docker daemon and the echo-grpc image:

	just build-echo-grpc-image
	go test ./pkg/leaf/... -tags=integration -count=1 -v
*/

import (
	"context"
	"os"
	"testing"

	"github.com/docker/docker/client"
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		_, _ = os.Stderr.WriteString("Docker integration: failed to create client: " + err.Error() + "\n")
		os.Exit(1)
	}
	if _, err := cli.Ping(ctx); err != nil {
		_, _ = os.Stderr.WriteString("Docker integration: daemon not reachable: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestDockerDaemonRequired(t *testing.T) {
	// TestMain already verified Docker; this documents the suite dependency.
}
