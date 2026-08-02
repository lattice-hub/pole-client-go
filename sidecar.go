package pole

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	sidecarv1 "github.com/lattice-hub/pole-client-go/internal/bootstrapv1/api/v1/sidecar"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	// DefaultSidecarSocket is the in-Pod bootstrap Unix domain socket path.
	DefaultSidecarSocket = "/var/run/pole/sidecar/bootstrap.sock"
	// SidecarSocketEnv overrides DefaultSidecarSocket for development and special deployments.
	SidecarSocketEnv = "POLE_SIDECAR_SOCKET"

	defaultInitializationTimeout = 10 * time.Second
	defaultInitialRetryDelay     = 50 * time.Millisecond
	defaultMaxRetryDelay         = time.Second
	defaultHandshakeTimeout      = 2 * time.Second
)

var (
	// ErrSidecarUnavailable means no validated Sidecar listener snapshot is installed.
	ErrSidecarUnavailable = errors.New("pole sidecar is unavailable")
	// ErrInitializationTimeout means Sidecar bootstrap did not complete in time.
	ErrInitializationTimeout = errors.New("pole sidecar bootstrap timed out")
)

// Protocol identifies a Sidecar business listener.
type Protocol string

const (
	ProtocolHTTP   Protocol = "http"
	ProtocolGRPC   Protocol = "grpc"
	ProtocolDubbo  Protocol = "dubbo"
	ProtocolThrift Protocol = "thrift"
)

var requiredProtocols = [...]Protocol{
	ProtocolHTTP,
	ProtocolGRPC,
	ProtocolDubbo,
	ProtocolThrift,
}

// BootstrapConfig configures the internal Sidecar bootstrap session.
// Zero values select the documented defaults.
type BootstrapConfig struct {
	SocketPath            string
	SDKVersion            string
	InitializationTimeout time.Duration
	InitialRetryDelay     time.Duration
	MaxRetryDelay         time.Duration
	HandshakeTimeout      time.Duration
}

type normalizedBootstrapConfig struct {
	socketPath            string
	sdkVersion            string
	initializationTimeout time.Duration
	initialRetryDelay     time.Duration
	maxRetryDelay         time.Duration
	handshakeTimeout      time.Duration
}

// SidecarClient maintains one internal gRPC-over-UDS session with the Sidecar.
// It never carries business traffic.
type SidecarClient struct {
	config normalizedBootstrapConfig

	listeners atomic.Pointer[listenerSnapshot]
	context   context.Context
	cancel    context.CancelFunc
	closed    sync.Once
	ready     chan struct{}
	readyOnce sync.Once
}

type listenerSnapshot struct {
	addresses map[Protocol]string
}

// NewSidecarClient establishes the bootstrap session and waits for its first valid snapshot.
// The supplied context bounds initialization only; use Close to stop the client afterwards.
func NewSidecarClient(ctx context.Context, config BootstrapConfig) (*SidecarClient, error) {
	if ctx == nil {
		return nil, fmt.Errorf("initialization context must not be nil")
	}
	normalizedConfig, err := normalizeBootstrapConfig(config)
	if err != nil {
		return nil, err
	}
	clientContext, cancel := context.WithCancel(context.Background())
	client := &SidecarClient{
		config:  normalizedConfig,
		context: clientContext,
		cancel:  cancel,
		ready:   make(chan struct{}),
	}
	go client.run()

	timer := time.NewTimer(normalizedConfig.initializationTimeout)
	defer timer.Stop()
	select {
	case <-client.ready:
		return client, nil
	case <-ctx.Done():
		client.Close()
		return nil, fmt.Errorf("initialize pole sidecar client: %w", ctx.Err())
	case <-timer.C:
		client.Close()
		return nil, ErrInitializationTimeout
	}
}

// ListenerAddress returns the current loopback address for protocol.
// It fails immediately when the Sidecar session has not produced a valid snapshot.
func (client *SidecarClient) ListenerAddress(protocol Protocol) (string, error) {
	if !isRequiredProtocol(protocol) {
		return "", fmt.Errorf("unsupported Sidecar protocol %q", protocol)
	}
	snapshot := client.listeners.Load()
	if snapshot == nil {
		return "", ErrSidecarUnavailable
	}
	return snapshot.addresses[protocol], nil
}

// Close stops the bootstrap session and immediately invalidates its listener snapshot.
func (client *SidecarClient) Close() {
	if client == nil {
		return
	}
	client.closed.Do(func() {
		client.listeners.Store(nil)
		client.cancel()
	})
}

func normalizeBootstrapConfig(config BootstrapConfig) (normalizedBootstrapConfig, error) {
	normalized := normalizedBootstrapConfig{
		socketPath:            resolveSidecarSocketPath(config.SocketPath),
		sdkVersion:            config.SDKVersion,
		initializationTimeout: config.InitializationTimeout,
		initialRetryDelay:     config.InitialRetryDelay,
		maxRetryDelay:         config.MaxRetryDelay,
		handshakeTimeout:      config.HandshakeTimeout,
	}
	if normalized.sdkVersion == "" {
		normalized.sdkVersion = "unknown"
	}
	if normalized.initializationTimeout == 0 {
		normalized.initializationTimeout = defaultInitializationTimeout
	}
	if normalized.initialRetryDelay == 0 {
		normalized.initialRetryDelay = defaultInitialRetryDelay
	}
	if normalized.maxRetryDelay == 0 {
		normalized.maxRetryDelay = defaultMaxRetryDelay
	}
	if normalized.handshakeTimeout == 0 {
		normalized.handshakeTimeout = defaultHandshakeTimeout
	}
	if normalized.socketPath == "" {
		return normalizedBootstrapConfig{}, fmt.Errorf("sidecar socket path must not be empty")
	}
	if normalized.initializationTimeout <= 0 || normalized.initialRetryDelay <= 0 || normalized.maxRetryDelay <= 0 || normalized.handshakeTimeout <= 0 {
		return normalizedBootstrapConfig{}, fmt.Errorf("bootstrap timeouts and retry delays must be positive")
	}
	if normalized.maxRetryDelay < normalized.initialRetryDelay {
		return normalizedBootstrapConfig{}, fmt.Errorf("max retry delay must not be smaller than initial retry delay")
	}
	return normalized, nil
}

func resolveSidecarSocketPath(configuredPath string) string {
	if configuredPath != "" {
		return configuredPath
	}
	if socketPath := os.Getenv(SidecarSocketEnv); socketPath != "" {
		return socketPath
	}
	return DefaultSidecarSocket
}

func (client *SidecarClient) run() {
	retryDelay := client.config.initialRetryDelay
	for {
		if client.context.Err() != nil {
			return
		}

		validSession := client.runSession()
		client.listeners.Store(nil)
		if validSession {
			retryDelay = client.config.initialRetryDelay
		}
		if !client.waitForRetry(retryDelay) {
			return
		}
		retryDelay = nextRetryDelay(retryDelay, client.config.maxRetryDelay)
	}
}

func (client *SidecarClient) runSession() bool {
	sessionContext, cancel := context.WithCancel(client.context)
	defer cancel()

	connection, err := grpc.DialContext(
		sessionContext,
		"passthrough:///pole-sidecar-bootstrap",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(client.unixDialer),
	)
	if err != nil {
		return false
	}
	defer connection.Close()

	stream, err := sidecarv1.NewSidecarSessionServiceClient(connection).OpenSession(
		sessionContext,
		&sidecarv1.ClientHello{
			SdkLanguage:        "go",
			SdkVersion:         client.config.sdkVersion,
			SupportedProtocols: supportedProtocolValues(),
		},
	)
	if err != nil {
		return false
	}

	event, err := receiveInitialEvent(sessionContext, stream, client.config.handshakeTimeout)
	if err != nil {
		return false
	}
	snapshot, err := snapshotFromEvent(event)
	if err != nil {
		return false
	}
	client.listeners.Store(snapshot)
	client.readyOnce.Do(func() { close(client.ready) })

	for {
		event, err := stream.Recv()
		if err != nil {
			return true
		}
		if event.GetListenerSnapshot() != nil {
			return true
		}
	}
}

func (client *SidecarClient) unixDialer(ctx context.Context, _ string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", client.config.socketPath)
}

func receiveInitialEvent(
	ctx context.Context,
	stream sidecarv1.SidecarSessionService_OpenSessionClient,
	timeout time.Duration,
) (*sidecarv1.SidecarEvent, error) {
	type receiveResult struct {
		event *sidecarv1.SidecarEvent
		err   error
	}
	result := make(chan receiveResult, 1)
	go func() {
		event, err := stream.Recv()
		result <- receiveResult{event: event, err: err}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case received := <-result:
		return received.event, received.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("sidecar bootstrap first event timed out")
	}
}

func snapshotFromEvent(event *sidecarv1.SidecarEvent) (*listenerSnapshot, error) {
	if event == nil || event.GetListenerSnapshot() == nil {
		return nil, fmt.Errorf("sidecar bootstrap first event must be listener_snapshot")
	}
	addresses := make(map[Protocol]string, len(requiredProtocols))
	for _, listener := range event.GetListenerSnapshot().GetListeners() {
		protocol, err := protocolFromProto(listener.GetProtocol())
		if err != nil {
			return nil, err
		}
		if _, found := addresses[protocol]; found {
			return nil, fmt.Errorf("sidecar listener snapshot contains duplicate %s protocol", protocol)
		}
		port := listener.GetPort()
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("sidecar listener snapshot has invalid %s port %d", protocol, port)
		}
		addresses[protocol] = net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port))
	}
	for _, protocol := range requiredProtocols {
		if _, found := addresses[protocol]; !found {
			return nil, fmt.Errorf("sidecar listener snapshot is missing %s protocol", protocol)
		}
	}
	return &listenerSnapshot{addresses: addresses}, nil
}

func protocolFromProto(protocol sidecarv1.Protocol) (Protocol, error) {
	switch protocol {
	case sidecarv1.Protocol_PROTOCOL_HTTP:
		return ProtocolHTTP, nil
	case sidecarv1.Protocol_PROTOCOL_GRPC:
		return ProtocolGRPC, nil
	case sidecarv1.Protocol_PROTOCOL_DUBBO:
		return ProtocolDubbo, nil
	case sidecarv1.Protocol_PROTOCOL_THRIFT:
		return ProtocolThrift, nil
	default:
		return "", fmt.Errorf("sidecar listener snapshot has unsupported protocol %s", protocol)
	}
}

func supportedProtocolValues() []sidecarv1.Protocol {
	return []sidecarv1.Protocol{
		sidecarv1.Protocol_PROTOCOL_HTTP,
		sidecarv1.Protocol_PROTOCOL_GRPC,
		sidecarv1.Protocol_PROTOCOL_DUBBO,
		sidecarv1.Protocol_PROTOCOL_THRIFT,
	}
}

func isRequiredProtocol(protocol Protocol) bool {
	for _, required := range requiredProtocols {
		if protocol == required {
			return true
		}
	}
	return false
}

func (client *SidecarClient) waitForRetry(delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-client.context.Done():
		return false
	case <-timer.C:
		return true
	}
}

func nextRetryDelay(current, maximum time.Duration) time.Duration {
	if current >= maximum/2 {
		return maximum
	}
	return current * 2
}
