package pole

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	sidecarv1 "github.com/lattice-hub/pole-client-go/internal/bootstrapv1/api/v1/sidecar"
	"google.golang.org/grpc"
)

func TestNewSidecarClientInstallsFirstSnapshot(t *testing.T) {
	socketPath := newTestSocketPath(t)
	helloReceived := make(chan *sidecarv1.ClientHello, 1)
	server := startBootstrapServer(t, socketPath, func(hello *sidecarv1.ClientHello, stream grpc.ServerStreamingServer[sidecarv1.SidecarEvent]) error {
		helloReceived <- hello
		if err := stream.Send(validSnapshot(18101)); err != nil {
			return err
		}
		<-stream.Context().Done()
		return stream.Context().Err()
	})
	defer server.stop()

	client, err := NewSidecarClient(context.Background(), testBootstrapConfig(socketPath))
	if err != nil {
		t.Fatalf("NewSidecarClient() error = %v", err)
	}
	defer client.Close()

	assertListenerAddress(t, client, ProtocolHTTP, "127.0.0.1:18101")
	assertListenerAddress(t, client, ProtocolGRPC, "127.0.0.1:18102")
	assertListenerAddress(t, client, ProtocolDubbo, "127.0.0.1:18103")
	assertListenerAddress(t, client, ProtocolThrift, "127.0.0.1:18104")

	select {
	case hello := <-helloReceived:
		if hello.GetSdkLanguage() != "go" || hello.GetSdkVersion() != "test" {
			t.Fatalf("ClientHello = %#v", hello)
		}
		if want := supportedProtocolValues(); !reflect.DeepEqual(hello.GetSupportedProtocols(), want) {
			t.Fatalf("supported protocols = %#v, want %#v", hello.GetSupportedProtocols(), want)
		}
	case <-time.After(time.Second):
		t.Fatal("Sidecar did not receive ClientHello")
	}
}

func TestNewSidecarClientRejectsInvalidFirstSnapshotUntilTimeout(t *testing.T) {
	socketPath := newTestSocketPath(t)
	server := startBootstrapServer(t, socketPath, func(_ *sidecarv1.ClientHello, stream grpc.ServerStreamingServer[sidecarv1.SidecarEvent]) error {
		return stream.Send(&sidecarv1.SidecarEvent{
			Event: &sidecarv1.SidecarEvent_ListenerSnapshot{
				ListenerSnapshot: &sidecarv1.ListenerSnapshot{
					Listeners: []*sidecarv1.Listener{{Protocol: sidecarv1.Protocol_PROTOCOL_HTTP, Port: 15001}},
				},
			},
		})
	})
	defer server.stop()

	config := testBootstrapConfig(socketPath)
	config.InitializationTimeout = 120 * time.Millisecond
	if _, err := NewSidecarClient(context.Background(), config); !errors.Is(err, ErrInitializationTimeout) {
		t.Fatalf("NewSidecarClient() error = %v, want ErrInitializationTimeout", err)
	}
}

func TestSidecarClientInvalidatesSnapshotOnDisconnectAndReconnects(t *testing.T) {
	socketPath := newTestSocketPath(t)
	firstServer := startBootstrapServer(t, socketPath, func(_ *sidecarv1.ClientHello, stream grpc.ServerStreamingServer[sidecarv1.SidecarEvent]) error {
		if err := stream.Send(validSnapshot(18201)); err != nil {
			return err
		}
		<-stream.Context().Done()
		return nil
	})

	client, err := NewSidecarClient(context.Background(), testBootstrapConfig(socketPath))
	if err != nil {
		firstServer.stop()
		t.Fatalf("NewSidecarClient() error = %v", err)
	}
	defer client.Close()
	assertListenerAddress(t, client, ProtocolHTTP, "127.0.0.1:18201")

	firstServer.stop()
	eventually(t, time.Second, func() bool {
		_, err := client.ListenerAddress(ProtocolHTTP)
		return errors.Is(err, ErrSidecarUnavailable)
	})

	secondServer := startBootstrapServer(t, socketPath, func(_ *sidecarv1.ClientHello, stream grpc.ServerStreamingServer[sidecarv1.SidecarEvent]) error {
		if err := stream.Send(validSnapshot(18301)); err != nil {
			return err
		}
		<-stream.Context().Done()
		return nil
	})
	defer secondServer.stop()
	eventually(t, time.Second, func() bool {
		address, err := client.ListenerAddress(ProtocolHTTP)
		return err == nil && address == "127.0.0.1:18301"
	})
}

func TestSnapshotFromEventRejectsDuplicateUnknownAndInvalidPorts(t *testing.T) {
	tests := []struct {
		name  string
		event *sidecarv1.SidecarEvent
	}{
		{name: "duplicate", event: &sidecarv1.SidecarEvent{Event: &sidecarv1.SidecarEvent_ListenerSnapshot{ListenerSnapshot: &sidecarv1.ListenerSnapshot{Listeners: []*sidecarv1.Listener{
			{Protocol: sidecarv1.Protocol_PROTOCOL_HTTP, Port: 15001},
			{Protocol: sidecarv1.Protocol_PROTOCOL_HTTP, Port: 15002},
			{Protocol: sidecarv1.Protocol_PROTOCOL_GRPC, Port: 15003},
			{Protocol: sidecarv1.Protocol_PROTOCOL_DUBBO, Port: 15004},
			{Protocol: sidecarv1.Protocol_PROTOCOL_THRIFT, Port: 15005},
		}}}}},
		{name: "unspecified", event: &sidecarv1.SidecarEvent{Event: &sidecarv1.SidecarEvent_ListenerSnapshot{ListenerSnapshot: &sidecarv1.ListenerSnapshot{Listeners: []*sidecarv1.Listener{
			{Protocol: sidecarv1.Protocol_PROTOCOL_HTTP, Port: 15001},
			{Protocol: sidecarv1.Protocol_PROTOCOL_GRPC, Port: 15002},
			{Protocol: sidecarv1.Protocol_PROTOCOL_DUBBO, Port: 15003},
			{Protocol: sidecarv1.Protocol_PROTOCOL_THRIFT, Port: 15004},
			{Protocol: sidecarv1.Protocol_PROTOCOL_UNSPECIFIED, Port: 15005},
		}}}}},
		{name: "port out of range", event: &sidecarv1.SidecarEvent{Event: &sidecarv1.SidecarEvent_ListenerSnapshot{ListenerSnapshot: &sidecarv1.ListenerSnapshot{Listeners: []*sidecarv1.Listener{
			{Protocol: sidecarv1.Protocol_PROTOCOL_HTTP, Port: 0},
			{Protocol: sidecarv1.Protocol_PROTOCOL_GRPC, Port: 15002},
			{Protocol: sidecarv1.Protocol_PROTOCOL_DUBBO, Port: 15003},
			{Protocol: sidecarv1.Protocol_PROTOCOL_THRIFT, Port: 15004},
		}}}}},
		{name: "port too large", event: &sidecarv1.SidecarEvent{Event: &sidecarv1.SidecarEvent_ListenerSnapshot{ListenerSnapshot: &sidecarv1.ListenerSnapshot{Listeners: []*sidecarv1.Listener{
			{Protocol: sidecarv1.Protocol_PROTOCOL_HTTP, Port: 65536},
			{Protocol: sidecarv1.Protocol_PROTOCOL_GRPC, Port: 15002},
			{Protocol: sidecarv1.Protocol_PROTOCOL_DUBBO, Port: 15003},
			{Protocol: sidecarv1.Protocol_PROTOCOL_THRIFT, Port: 15004},
		}}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := snapshotFromEvent(test.event); err == nil {
				t.Fatal("snapshotFromEvent() error = nil, want validation error")
			}
		})
	}
}

func TestResolveSidecarSocketPath(t *testing.T) {
	t.Setenv(SidecarSocketEnv, "/tmp/from-env.sock")
	if got := resolveSidecarSocketPath(""); got != "/tmp/from-env.sock" {
		t.Fatalf("resolveSidecarSocketPath() = %q", got)
	}
	if got := resolveSidecarSocketPath("/tmp/configured.sock"); got != "/tmp/configured.sock" {
		t.Fatalf("resolveSidecarSocketPath() = %q", got)
	}
}

type bootstrapTestServer struct {
	grpcServer *grpc.Server
	listener   net.Listener
	path       string
	once       sync.Once
}

func startBootstrapServer(t *testing.T, socketPath string, open func(*sidecarv1.ClientHello, grpc.ServerStreamingServer[sidecarv1.SidecarEvent]) error) *bootstrapTestServer {
	t.Helper()
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on %s: %v", socketPath, err)
	}
	grpcServer := grpc.NewServer()
	sidecarv1.RegisterSidecarSessionServiceServer(grpcServer, bootstrapService{open: open})
	go func() {
		_ = grpcServer.Serve(listener)
	}()
	return &bootstrapTestServer{grpcServer: grpcServer, listener: listener, path: socketPath}
}

func (server *bootstrapTestServer) stop() {
	server.once.Do(func() {
		server.grpcServer.Stop()
		_ = server.listener.Close()
		_ = os.Remove(server.path)
	})
}

type bootstrapService struct {
	sidecarv1.UnimplementedSidecarSessionServiceServer
	open func(*sidecarv1.ClientHello, grpc.ServerStreamingServer[sidecarv1.SidecarEvent]) error
}

func (service bootstrapService) OpenSession(hello *sidecarv1.ClientHello, stream grpc.ServerStreamingServer[sidecarv1.SidecarEvent]) error {
	return service.open(hello, stream)
}

func validSnapshot(firstPort uint32) *sidecarv1.SidecarEvent {
	return &sidecarv1.SidecarEvent{Event: &sidecarv1.SidecarEvent_ListenerSnapshot{ListenerSnapshot: &sidecarv1.ListenerSnapshot{Listeners: []*sidecarv1.Listener{
		{Protocol: sidecarv1.Protocol_PROTOCOL_HTTP, Port: firstPort},
		{Protocol: sidecarv1.Protocol_PROTOCOL_GRPC, Port: firstPort + 1},
		{Protocol: sidecarv1.Protocol_PROTOCOL_DUBBO, Port: firstPort + 2},
		{Protocol: sidecarv1.Protocol_PROTOCOL_THRIFT, Port: firstPort + 3},
	}}}}
}

func testBootstrapConfig(socketPath string) BootstrapConfig {
	return BootstrapConfig{
		SocketPath:            socketPath,
		SDKVersion:            "test",
		InitializationTimeout: time.Second,
		InitialRetryDelay:     10 * time.Millisecond,
		MaxRetryDelay:         20 * time.Millisecond,
		HandshakeTimeout:      100 * time.Millisecond,
	}
}

func newTestSocketPath(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "pole-go-")
	if err != nil {
		t.Fatalf("create temporary socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return filepath.Join(directory, "bootstrap.sock")
}

func assertListenerAddress(t *testing.T, client *SidecarClient, protocol Protocol, want string) {
	t.Helper()
	got, err := client.ListenerAddress(protocol)
	if err != nil {
		t.Fatalf("ListenerAddress(%q) error = %v", protocol, err)
	}
	if got != want {
		t.Fatalf("ListenerAddress(%q) = %q, want %q", protocol, got, want)
	}
}

func eventually(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not satisfied before timeout")
}
