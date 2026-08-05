package pole

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	server := startControlServer(t, socketPath, func(stream grpc.BidiStreamingServer[sidecarv1.ClientEvent, sidecarv1.SidecarEvent]) error {
		hello, err := receiveHello(stream)
		if err != nil {
			return err
		}
		helloReceived <- hello
		if err := stream.Send(validSnapshot(18101)); err != nil {
			return err
		}
		<-stream.Context().Done()
		return nil
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

func TestSidecarClientRegistersStatusesAndEndsControlSession(t *testing.T) {
	socketPath := newTestSocketPath(t)
	registrationReceived := make(chan *sidecarv1.LocalServiceRegistration, 1)
	unregistrationReceived := make(chan *sidecarv1.LocalServiceUnregistration, 1)
	closed := make(chan struct{})
	server := startControlServer(t, socketPath, func(stream grpc.BidiStreamingServer[sidecarv1.ClientEvent, sidecarv1.SidecarEvent]) error {
		if _, err := receiveHello(stream); err != nil {
			return err
		}
		if err := stream.Send(validSnapshot(18401)); err != nil {
			return err
		}
		registrationEvent, err := stream.Recv()
		if err != nil {
			return err
		}
		registration := registrationEvent.GetRegisterLocalService()
		registrationReceived <- registration
		if err := stream.Send(localServiceStatusEvent(
			registration.GetRegistrationId(),
			sidecarv1.LocalServiceState_LOCAL_SERVICE_STATE_REGISTERED,
			"accepted")); err != nil {
			return err
		}
		unregistrationEvent, err := stream.Recv()
		if err != nil {
			return err
		}
		unregistration := unregistrationEvent.GetUnregisterLocalService()
		unregistrationReceived <- unregistration
		if err := stream.Send(localServiceStatusEvent(
			unregistration.GetRegistrationId(),
			sidecarv1.LocalServiceState_LOCAL_SERVICE_STATE_UNREGISTERED,
			"removed")); err != nil {
			return err
		}
		_, err = stream.Recv()
		if !errors.Is(err, io.EOF) {
			return err
		}
		close(closed)
		return nil
	})
	defer server.stop()

	client, err := NewSidecarClient(context.Background(), testBootstrapConfig(socketPath))
	if err != nil {
		t.Fatalf("NewSidecarClient() error = %v", err)
	}
	registration, err := client.RegisterLocalServiceWithID(
		"payments-http",
		"prod",
		"payments",
		ProtocolHTTP,
		8080,
	)
	if err != nil {
		t.Fatalf("RegisterLocalServiceWithID() error = %v", err)
	}
	select {
	case received := <-registrationReceived:
		if received.GetRegistrationId() != registration.RegistrationID || received.GetLocalPort() != 8080 {
			t.Fatalf("registration = %#v", received)
		}
	case <-time.After(time.Second):
		t.Fatal("Sidecar did not receive local service registration")
	}
	eventually(t, time.Second, func() bool {
		status, found := client.LocalServiceStatus(registration.RegistrationID)
		return found && status.State == LocalServiceRegistered && status.Message == "accepted"
	})

	if err := client.UnregisterLocalService(registration.RegistrationID); err != nil {
		t.Fatalf("UnregisterLocalService() error = %v", err)
	}
	select {
	case received := <-unregistrationReceived:
		if received.GetRegistrationId() != registration.RegistrationID {
			t.Fatalf("unregistration = %#v", received)
		}
	case <-time.After(time.Second):
		t.Fatal("Sidecar did not receive local service unregistration")
	}
	eventually(t, time.Second, func() bool {
		status, found := client.LocalServiceStatus(registration.RegistrationID)
		return found && status.State == LocalServiceUnregistered && status.Message == "removed"
	})
	client.Close()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Sidecar did not observe client stream completion")
	}
}

func TestSidecarClientReplaysDesiredRegistrationsAfterReconnect(t *testing.T) {
	socketPath := newTestSocketPath(t)
	firstRegistration := make(chan *sidecarv1.LocalServiceRegistration, 1)
	firstServer := startControlServer(t, socketPath, func(stream grpc.BidiStreamingServer[sidecarv1.ClientEvent, sidecarv1.SidecarEvent]) error {
		if _, err := receiveHello(stream); err != nil {
			return err
		}
		if err := stream.Send(validSnapshot(18201)); err != nil {
			return err
		}
		event, err := stream.Recv()
		if err != nil {
			return err
		}
		firstRegistration <- event.GetRegisterLocalService()
		<-stream.Context().Done()
		return nil
	})

	client, err := NewSidecarClient(context.Background(), testBootstrapConfig(socketPath))
	if err != nil {
		firstServer.stop()
		t.Fatalf("NewSidecarClient() error = %v", err)
	}
	defer client.Close()
	registration, err := client.RegisterLocalServiceWithID("catalog-grpc", "prod", "catalog", ProtocolGRPC, 9090)
	if err != nil {
		firstServer.stop()
		t.Fatalf("RegisterLocalServiceWithID() error = %v", err)
	}
	select {
	case <-firstRegistration:
	case <-time.After(time.Second):
		firstServer.stop()
		t.Fatal("first Sidecar session did not receive registration")
	}
	firstServer.stop()

	replayedRegistration := make(chan *sidecarv1.LocalServiceRegistration, 1)
	secondServer := startControlServer(t, socketPath, func(stream grpc.BidiStreamingServer[sidecarv1.ClientEvent, sidecarv1.SidecarEvent]) error {
		if _, err := receiveHello(stream); err != nil {
			return err
		}
		event, err := stream.Recv()
		if err != nil {
			return err
		}
		replayedRegistration <- event.GetRegisterLocalService()
		if err := stream.Send(validSnapshot(18301)); err != nil {
			return err
		}
		<-stream.Context().Done()
		return nil
	})
	defer secondServer.stop()
	select {
	case replayed := <-replayedRegistration:
		if replayed.GetRegistrationId() != registration.RegistrationID || replayed.GetLocalPort() != registration.LocalPort {
			t.Fatalf("replayed registration = %#v", replayed)
		}
	case <-time.After(time.Second):
		t.Fatal("reconnected Sidecar session did not replay registration")
	}
	eventually(t, time.Second, func() bool {
		address, err := client.ListenerAddress(ProtocolHTTP)
		return err == nil && address == "127.0.0.1:18301"
	})
}

func TestNewSidecarClientRejectsInvalidFirstSnapshotUntilTimeout(t *testing.T) {
	socketPath := newTestSocketPath(t)
	server := startControlServer(t, socketPath, func(stream grpc.BidiStreamingServer[sidecarv1.ClientEvent, sidecarv1.SidecarEvent]) error {
		if _, err := receiveHello(stream); err != nil {
			return err
		}
		return stream.Send(&sidecarv1.SidecarEvent{Event: &sidecarv1.SidecarEvent_ListenerSnapshot{
			ListenerSnapshot: &sidecarv1.ListenerSnapshot{Listeners: []*sidecarv1.Listener{{
				Protocol: sidecarv1.Protocol_PROTOCOL_HTTP,
				Port:     15001,
			}}},
		}})
	})
	defer server.stop()

	config := testBootstrapConfig(socketPath)
	config.InitializationTimeout = 120 * time.Millisecond
	if _, err := NewSidecarClient(context.Background(), config); !errors.Is(err, ErrInitializationTimeout) {
		t.Fatalf("NewSidecarClient() error = %v, want ErrInitializationTimeout", err)
	}
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

func startControlServer(t *testing.T, socketPath string, open func(grpc.BidiStreamingServer[sidecarv1.ClientEvent, sidecarv1.SidecarEvent]) error) *bootstrapTestServer {
	t.Helper()
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on %s: %v", socketPath, err)
	}
	grpcServer := grpc.NewServer()
	sidecarv1.RegisterSidecarSessionServiceServer(grpcServer, bootstrapService{open: open})
	go func() { _ = grpcServer.Serve(listener) }()
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
	open func(grpc.BidiStreamingServer[sidecarv1.ClientEvent, sidecarv1.SidecarEvent]) error
}

func (service bootstrapService) OpenControlSession(stream grpc.BidiStreamingServer[sidecarv1.ClientEvent, sidecarv1.SidecarEvent]) error {
	return service.open(stream)
}

func receiveHello(stream grpc.BidiStreamingServer[sidecarv1.ClientEvent, sidecarv1.SidecarEvent]) (*sidecarv1.ClientHello, error) {
	event, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	if event.GetHello() == nil {
		return nil, fmt.Errorf("first client event must be hello")
	}
	return event.GetHello(), nil
}

func validSnapshot(firstPort uint32) *sidecarv1.SidecarEvent {
	return &sidecarv1.SidecarEvent{Event: &sidecarv1.SidecarEvent_ListenerSnapshot{ListenerSnapshot: &sidecarv1.ListenerSnapshot{Listeners: []*sidecarv1.Listener{
		{Protocol: sidecarv1.Protocol_PROTOCOL_HTTP, Port: firstPort},
		{Protocol: sidecarv1.Protocol_PROTOCOL_GRPC, Port: firstPort + 1},
		{Protocol: sidecarv1.Protocol_PROTOCOL_DUBBO, Port: firstPort + 2},
		{Protocol: sidecarv1.Protocol_PROTOCOL_THRIFT, Port: firstPort + 3},
	}}}}
}

func localServiceStatusEvent(registrationID string, state sidecarv1.LocalServiceState, message string) *sidecarv1.SidecarEvent {
	return &sidecarv1.SidecarEvent{Event: &sidecarv1.SidecarEvent_LocalServiceStatus{LocalServiceStatus: &sidecarv1.LocalServiceStatus{
		RegistrationId: registrationID,
		State:          state,
		Message:        message,
	}}}
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
