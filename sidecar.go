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
	DefaultSidecarSocket = "/var/run/pole/sidecar/bootstrap.sock"
	SidecarSocketEnv     = "POLE_SIDECAR_SOCKET"

	defaultInitializationTimeout = 10 * time.Second
	defaultInitialRetryDelay     = 50 * time.Millisecond
	defaultMaxRetryDelay         = time.Second
	defaultHandshakeTimeout      = 2 * time.Second
)

var (
	ErrSidecarUnavailable    = errors.New("pole sidecar is unavailable")
	ErrInitializationTimeout = errors.New("pole sidecar bootstrap timed out")
	ErrSidecarClientClosed   = errors.New("pole sidecar client is closed")
	ErrLocalServiceNotFound  = errors.New("local service registration was not found")
)

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

type BootstrapConfig struct {
	SocketPath            string
	SDKVersion            string
	InitializationTimeout time.Duration
	InitialRetryDelay     time.Duration
	MaxRetryDelay         time.Duration
	HandshakeTimeout      time.Duration
}

type LocalServiceRegistration struct {
	RegistrationID string
	Namespace      string
	Service        string
	Protocol       Protocol
	LocalPort      uint32
}

type LocalServiceState string

const (
	LocalServiceRegistered   LocalServiceState = "registered"
	LocalServiceUnregistered LocalServiceState = "unregistered"
	LocalServiceRejected     LocalServiceState = "rejected"
)

type LocalServiceStatus struct {
	RegistrationID string
	State          LocalServiceState
	Message        string
}

type normalizedBootstrapConfig struct {
	socketPath            string
	sdkVersion            string
	initializationTimeout time.Duration
	initialRetryDelay     time.Duration
	maxRetryDelay         time.Duration
	handshakeTimeout      time.Duration
}

type SidecarClient struct {
	config normalizedBootstrapConfig

	listeners atomic.Pointer[listenerSnapshot]
	context   context.Context
	cancel    context.CancelFunc
	closed    sync.Once
	ready     chan struct{}
	readyOnce sync.Once

	registrationsMu      sync.Mutex
	desiredRegistrations map[string]LocalServiceRegistration
	registrationStatuses map[string]LocalServiceStatus
	activeSession        *activeControlSession
	registrationSequence atomic.Uint64
}

type listenerSnapshot struct {
	addresses map[Protocol]string
}

type activeControlSession struct {
	stream sidecarv1.SidecarSessionService_OpenControlSessionClient
	done   chan struct{}
}

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
		config:               normalizedConfig,
		context:              clientContext,
		cancel:               cancel,
		ready:                make(chan struct{}),
		desiredRegistrations: make(map[string]LocalServiceRegistration),
		registrationStatuses: make(map[string]LocalServiceStatus),
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

func (client *SidecarClient) RegisterLocalService(
	namespace string,
	service string,
	protocol Protocol,
	localPort uint32,
) (LocalServiceRegistration, error) {
	registrationID := fmt.Sprintf("sdk-local-service-%d", client.registrationSequence.Add(1))
	return client.RegisterLocalServiceWithID(registrationID, namespace, service, protocol, localPort)
}

func (client *SidecarClient) RegisterLocalServiceWithID(
	registrationID string,
	namespace string,
	service string,
	protocol Protocol,
	localPort uint32,
) (LocalServiceRegistration, error) {
	registration, err := newLocalServiceRegistration(
		registrationID,
		namespace,
		service,
		protocol,
		localPort,
	)
	if err != nil {
		return LocalServiceRegistration{}, err
	}
	client.registrationsMu.Lock()
	defer client.registrationsMu.Unlock()
	if client.context.Err() != nil {
		return LocalServiceRegistration{}, ErrSidecarClientClosed
	}
	if _, exists := client.desiredRegistrations[registration.RegistrationID]; exists {
		return LocalServiceRegistration{}, fmt.Errorf("local service registration %q already exists", registration.RegistrationID)
	}
	client.desiredRegistrations[registration.RegistrationID] = registration
	delete(client.registrationStatuses, registration.RegistrationID)
	if client.activeSession != nil {
		_ = client.activeSession.stream.Send(registrationEvent(registration))
	}
	return registration, nil
}

func (client *SidecarClient) UnregisterLocalService(registrationID string) error {
	if registrationID == "" {
		return fmt.Errorf("local service registration ID must not be empty")
	}
	client.registrationsMu.Lock()
	defer client.registrationsMu.Unlock()
	if client.context.Err() != nil {
		return ErrSidecarClientClosed
	}
	if _, exists := client.desiredRegistrations[registrationID]; !exists {
		return fmt.Errorf("%w: %s", ErrLocalServiceNotFound, registrationID)
	}
	delete(client.desiredRegistrations, registrationID)
	if client.activeSession != nil {
		_ = client.activeSession.stream.Send(unregistrationEvent(registrationID))
	}
	return nil
}

func (client *SidecarClient) LocalServiceStatus(registrationID string) (LocalServiceStatus, bool) {
	client.registrationsMu.Lock()
	defer client.registrationsMu.Unlock()
	status, exists := client.registrationStatuses[registrationID]
	return status, exists
}

func (client *SidecarClient) Close() {
	if client == nil {
		return
	}
	client.closed.Do(func() {
		client.listeners.Store(nil)
		client.registrationsMu.Lock()
		activeSession := client.activeSession
		client.activeSession = nil
		client.registrationStatuses = make(map[string]LocalServiceStatus)
		client.registrationsMu.Unlock()
		if activeSession != nil {
			_ = activeSession.stream.CloseSend()
			select {
			case <-activeSession.done:
			case <-time.After(time.Second):
			}
		}
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

	stream, err := sidecarv1.NewSidecarSessionServiceClient(connection).OpenControlSession(sessionContext)
	if err != nil {
		return false
	}
	if err := stream.Send(&sidecarv1.ClientEvent{Event: &sidecarv1.ClientEvent_Hello{Hello: &sidecarv1.ClientHello{
		SdkLanguage:        "go",
		SdkVersion:         client.config.sdkVersion,
		SupportedProtocols: supportedProtocolValues(),
	}}}); err != nil {
		return false
	}
	activeSession := &activeControlSession{stream: stream, done: make(chan struct{})}
	client.registrationsMu.Lock()
	if client.context.Err() != nil {
		client.registrationsMu.Unlock()
		return false
	}
	client.activeSession = activeSession
	for _, registration := range client.desiredRegistrations {
		if err := stream.Send(registrationEvent(registration)); err != nil {
			client.activeSession = nil
			client.registrationsMu.Unlock()
			close(activeSession.done)
			return false
		}
	}
	client.registrationsMu.Unlock()
	defer client.clearActiveSession(activeSession)

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
		if err := client.handleLocalServiceStatus(event); err != nil {
			return false
		}
	}
}

func (client *SidecarClient) clearActiveSession(activeSession *activeControlSession) {
	client.registrationsMu.Lock()
	if client.activeSession == activeSession {
		client.activeSession = nil
	}
	client.registrationStatuses = make(map[string]LocalServiceStatus)
	client.registrationsMu.Unlock()
	close(activeSession.done)
}

func (client *SidecarClient) handleLocalServiceStatus(event *sidecarv1.SidecarEvent) error {
	status := event.GetLocalServiceStatus()
	if status == nil {
		return fmt.Errorf("sidecar control session events after the snapshot must be local_service_status")
	}
	if status.GetRegistrationId() == "" {
		return fmt.Errorf("local service status registration ID must not be empty")
	}
	state, err := localServiceStateFromProto(status.GetState())
	if err != nil {
		return err
	}
	client.registrationsMu.Lock()
	client.registrationStatuses[status.GetRegistrationId()] = LocalServiceStatus{
		RegistrationID: status.GetRegistrationId(),
		State:          state,
		Message:        status.GetMessage(),
	}
	client.registrationsMu.Unlock()
	return nil
}

func receiveInitialEvent(
	ctx context.Context,
	stream sidecarv1.SidecarSessionService_OpenControlSessionClient,
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

func newLocalServiceRegistration(
	registrationID string,
	namespace string,
	service string,
	protocol Protocol,
	localPort uint32,
) (LocalServiceRegistration, error) {
	if registrationID == "" || namespace == "" || service == "" {
		return LocalServiceRegistration{}, fmt.Errorf("local service registration ID, namespace, and service must not be empty")
	}
	if !isRequiredProtocol(protocol) {
		return LocalServiceRegistration{}, fmt.Errorf("unsupported local service protocol %q", protocol)
	}
	if localPort < 1 || localPort > 65535 {
		return LocalServiceRegistration{}, fmt.Errorf("local service port %d must be in range 1..65535", localPort)
	}
	return LocalServiceRegistration{
		RegistrationID: registrationID,
		Namespace:      namespace,
		Service:        service,
		Protocol:       protocol,
		LocalPort:      localPort,
	}, nil
}

func registrationEvent(registration LocalServiceRegistration) *sidecarv1.ClientEvent {
	return &sidecarv1.ClientEvent{Event: &sidecarv1.ClientEvent_RegisterLocalService{
		RegisterLocalService: &sidecarv1.LocalServiceRegistration{
			RegistrationId: registration.RegistrationID,
			Namespace:      registration.Namespace,
			Service:        registration.Service,
			Protocol:       protocolToProto(registration.Protocol),
			LocalPort:      registration.LocalPort,
		},
	}}
}

func unregistrationEvent(registrationID string) *sidecarv1.ClientEvent {
	return &sidecarv1.ClientEvent{Event: &sidecarv1.ClientEvent_UnregisterLocalService{
		UnregisterLocalService: &sidecarv1.LocalServiceUnregistration{RegistrationId: registrationID},
	}}
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

func protocolToProto(protocol Protocol) sidecarv1.Protocol {
	switch protocol {
	case ProtocolHTTP:
		return sidecarv1.Protocol_PROTOCOL_HTTP
	case ProtocolGRPC:
		return sidecarv1.Protocol_PROTOCOL_GRPC
	case ProtocolDubbo:
		return sidecarv1.Protocol_PROTOCOL_DUBBO
	case ProtocolThrift:
		return sidecarv1.Protocol_PROTOCOL_THRIFT
	default:
		return sidecarv1.Protocol_PROTOCOL_UNSPECIFIED
	}
}

func localServiceStateFromProto(state sidecarv1.LocalServiceState) (LocalServiceState, error) {
	switch state {
	case sidecarv1.LocalServiceState_LOCAL_SERVICE_STATE_REGISTERED:
		return LocalServiceRegistered, nil
	case sidecarv1.LocalServiceState_LOCAL_SERVICE_STATE_UNREGISTERED:
		return LocalServiceUnregistered, nil
	case sidecarv1.LocalServiceState_LOCAL_SERVICE_STATE_REJECTED:
		return LocalServiceRejected, nil
	default:
		return "", fmt.Errorf("unsupported local service state %s", state)
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

func (client *SidecarClient) unixDialer(ctx context.Context, _ string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", client.config.socketPath)
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
