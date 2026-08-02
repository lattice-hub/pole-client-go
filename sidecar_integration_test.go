package pole

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLiveSidecarBootstrap(t *testing.T) {
	socketPath := os.Getenv("POLE_SIDECAR_INTEGRATION_SOCKET")
	if socketPath == "" {
		t.Skip("POLE_SIDECAR_INTEGRATION_SOCKET is not set")
	}

	client, err := NewSidecarClient(context.Background(), BootstrapConfig{
		SocketPath:            socketPath,
		SDKVersion:            "integration-test",
		InitializationTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("bootstrap live Sidecar: %v", err)
	}
	defer client.Close()

	for _, protocol := range requiredProtocols {
		address, err := client.ListenerAddress(protocol)
		if err != nil {
			t.Fatalf("resolve %s listener: %v", protocol, err)
		}
		if address == "" {
			t.Fatalf("resolve %s listener: empty address", protocol)
		}
	}
}
