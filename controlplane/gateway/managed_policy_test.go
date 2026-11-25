package gateway_test

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/testutils/tlscert"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/gateway"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/testpolicy"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

const clientPolicyURI = "spiffe://example.test/ns/default/installation/installation-uid/node/node-uid/role/client"

// managedGatewayFromEnv uses the same stable inputs as an operator's Pod.
func managedGatewayFromEnv(t *testing.T, ca *tlscert.CA, directory string) gateway.Config {
	t.Helper()
	scope := testpolicy.Scope()
	t.Setenv("YANET_GATEWAY_AUTH_MODE", "enabled")
	t.Setenv("YANET_GATEWAY_AUTH_MANAGED_POLICY_DIRECTORY", directory)
	t.Setenv("YANET_GATEWAY_AUTH_MANAGED_POLICY_EXPECTED_SCOPE_EPOCH", scope.Epoch)
	t.Setenv("YANET_GATEWAY_AUTH_MANAGED_POLICY_EXPECTED_SCOPE_NODE_UID", scope.NodeUID)
	t.Setenv("YANET_GATEWAY_AUTH_MANAGED_POLICY_EXPECTED_SCOPE_SERVER_URI", scope.ServerURI)
	settings := struct {
		Gateway gateway.Config `yaml:"gateway"`
	}{Gateway: managedX509GatewayConfig(t, ca, directory)}
	settings.Gateway.Auth.ManagedPolicy = nil
	settings.Gateway.Auth.Mode = ""
	require.NoError(t, xcfg.Decode(
		[]byte("gateway: {instance_id: 1, auth: {managed_policy: {refresh_interval: 5ms}}}"),
		&settings, xcfg.WithEnv(),
	))
	return settings.Gateway
}

// workloadPolicy grants methods to the mTLS certificate's role URI.
func workloadPolicy(sequence uint64, methods ...string) testpolicy.Generation {
	return testpolicy.ForAccount(sequence, clientPolicyURI, methods...)
}

// managedX509GatewayConfig keeps the mTLS authenticator but replaces file RBAC.
func managedX509GatewayConfig(t *testing.T, ca *tlscert.CA, directory string) gateway.Config {
	t.Helper()
	config := gateway.DefaultConfig()
	config.Auth = auth.Config{
		Mode: auth.ModeEnabled, Authenticators: newX509AuthConfig(t, ca).Authenticators,
		ManagedPolicy: testpolicy.Config(directory),
	}
	return config
}

// managedClientTLS presents the same role identity on either transport.
func managedClientTLS(t *testing.T, ca *tlscert.CA) *tls.Config {
	t.Helper()
	subject, err := url.Parse(clientPolicyURI)
	require.NoError(t, err)
	return &tls.Config{
		RootCAs:      ca.Pool(),
		Certificates: []tls.Certificate{ca.IssueClient(t, "", tlscert.WithURIs(subject)).Certificate},
		MinVersion:   tls.VersionTLS12,
	}
}

// managedClient counts TLS handshakes, not logical gRPC connections.
func managedClient(t *testing.T, ca *tlscert.CA, address string, options ...grpc.DialOption) (*grpc.ClientConn, *atomic.Int32) {
	t.Helper()
	var handshakes atomic.Int32
	tlsConfig := managedClientTLS(t, ca)
	tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
		handshakes.Add(1)
		return nil
	}
	conn, err := grpc.NewClient(address, append(options, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	return conn, &handshakes
}

type managedEchoBackend struct {
	grpc_testing.UnimplementedTestServiceServer
	revokedDone chan error
	httpDone    chan error
}

func (m *managedEchoBackend) FullDuplexCall(stream grpc.BidiStreamingServer[grpc_testing.StreamingOutputCallRequest, grpc_testing.StreamingOutputCallResponse]) error {
	err := echoManagedStream(stream)
	m.revokedDone <- err
	return err
}

func (m *managedEchoBackend) HalfDuplexCall(stream grpc.BidiStreamingServer[grpc_testing.StreamingOutputCallRequest, grpc_testing.StreamingOutputCallResponse]) error {
	return echoManagedStream(stream)
}

func (m *managedEchoBackend) StreamingOutputCall(request *grpc_testing.StreamingOutputCallRequest, stream grpc.ServerStreamingServer[grpc_testing.StreamingOutputCallResponse]) error {
	reply := &grpc_testing.StreamingOutputCallResponse{Payload: request.GetPayload()}
	if err := stream.Send(reply); err != nil {
		return err
	}
	<-stream.Context().Done()
	err := stream.Context().Err()
	m.httpDone <- err
	return err
}

func echoManagedStream(stream grpc.BidiStreamingServer[grpc_testing.StreamingOutputCallRequest, grpc_testing.StreamingOutputCallResponse]) error {
	for {
		request, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		response := &grpc_testing.StreamingOutputCallResponse{Payload: request.GetPayload()}
		if err := stream.Send(response); err != nil {
			return err
		}
	}
}

// managedBackend runs the same gRPC backend for both client surfaces.
func managedBackend(t *testing.T) (string, *managedEchoBackend) {
	t.Helper()
	listener := NewTestListener(t)
	backend := &managedEchoBackend{revokedDone: make(chan error, 1), httpDone: make(chan error, 1)}
	server := grpc.NewServer()
	grpc_testing.RegisterTestServiceServer(server, backend)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String(), backend
}

// exchangeManagedMessage proves that the original duplex stream still works.
func exchangeManagedMessage(t *testing.T, stream grpc.BidiStreamingClient[grpc_testing.StreamingOutputCallRequest, grpc_testing.StreamingOutputCallResponse], body string) {
	t.Helper()
	request := &grpc_testing.StreamingOutputCallRequest{Payload: &grpc_testing.Payload{Body: []byte(body)}}
	require.NoError(t, stream.Send(request), "%s", body)
	reply, err := stream.Recv()
	require.NoError(t, err, "%s", body)
	require.Equal(t, []byte(body), reply.GetPayload().GetBody())
}

// Test_Gateway_ManagedPolicy_UpdatesLiveConnections verifies that grants and
// accounts change on one mTLS connection and only affected streams are canceled.
func Test_Gateway_ManagedPolicy_UpdatesLiveConnections(t *testing.T) {
	ca := tlscert.NewCA(t)
	directory := t.TempDir()
	register := ynpb.Gateway_Register_FullMethodName
	list := ynpb.Gateway_ListServices_FullMethodName
	metrics := ynpb.MetricsService_GetMetrics_FullMethodName
	introspect := ynpb.AuthService_IntrospectToken_FullMethodName
	revoke := grpc_testing.TestService_FullDuplexCall_FullMethodName
	keep := grpc_testing.TestService_HalfDuplexCall_FullMethodName
	sse := grpc_testing.TestService_StreamingOutputCall_FullMethodName
	retained := []string{register, metrics, introspect, keep}
	workloadPolicy(1, append(retained, revoke, sse)...).Publish(t, directory)
	backend, echo := managedBackend(t)
	https := NewTestListener(t)
	config := managedGatewayFromEnv(t, ca, directory)
	config.Server.HTTPEndpoint = https.Addr().String()
	address := startX509Gateway(t, ca, config, gateway.WithHTTPListener(https))
	conn, handshakes := managedClient(t, ca, address)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	gatewayClient := ynpb.NewGatewayClient(conn)
	_, err := gatewayClient.ListServices(ctx, &ynpb.ListServicesRequest{}, grpc.WaitForReady(true))
	require.Equal(t, codes.PermissionDenied, status.Code(err), "%v", err)
	workloadPolicy(2, append(retained, revoke, sse, list)...).Publish(t, directory)
	require.Eventually(t, func() bool {
		_, err := gatewayClient.ListServices(ctx, &ynpb.ListServicesRequest{})
		return err == nil
	}, time.Second, 5*time.Millisecond, "grant did not take effect")

	_, err = gatewayClient.Register(ctx, &ynpb.RegisterRequest{Backend: &ynpb.BackendDesc{
		Name: grpc_testing.TestService_ServiceDesc.ServiceName, Endpoint: backend,
	}}, grpc.WaitForReady(true))
	require.NoError(t, err)
	client := grpc_testing.NewTestServiceClient(conn)
	revoked, err := client.FullDuplexCall(ctx)
	require.NoError(t, err)
	unaffected, err := client.HalfDuplexCall(ctx)
	require.NoError(t, err)
	exchangeManagedMessage(t, revoked, "before revoke")
	exchangeManagedMessage(t, unaffected, "before revoke")

	var httpHandshakes atomic.Int32
	httpTLS := managedClientTLS(t, ca)
	httpTLS.VerifyConnection = func(state tls.ConnectionState) error {
		httpHandshakes.Add(1)
		return nil
	}
	httpTransport := &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: httpTLS}
	t.Cleanup(httpTransport.CloseIdleConnections)
	httpClient := &http.Client{Transport: httpTransport}
	post := func(method string) *http.Response {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, http.MethodPost,
			"https://"+config.Server.HTTPEndpoint+"/api"+method, strings.NewReader("{}"),
		)
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		response, err := httpClient.Do(request)
		require.NoError(t, err)
		t.Cleanup(func() { _ = response.Body.Close() })
		return response
	}
	response := post(sse)
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, 2, response.ProtoMajor)
	require.Equal(t, "text/event-stream", response.Header.Get("Content-Type"))
	events := bufio.NewReader(response.Body)
	first, err := events.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "event: message\n", first)

	workloadPolicy(3, retained...).Publish(t, directory)
	metricsClient := ynpb.NewMetricsServiceClient(conn)
	require.Eventually(t, func() bool {
		response, err := metricsClient.GetMetrics(ctx, &commonpb.GetMetricsRequest{})
		return err == nil && testpolicy.Metric(response.GetMetrics(), "auth_policy_active_revision").GetGauge() == 3
	}, time.Second, 5*time.Millisecond)

	_, err = revoked.Recv()
	// Transport cancellation can finish before the interceptor reports denial.
	require.Contains(t, []codes.Code{codes.Canceled, codes.PermissionDenied}, status.Code(err), "active gRPC stream: %v", err)
	select {
	case err := <-echo.revokedDone:
		require.Equal(t, codes.Canceled, status.Code(err), "%v", err)
	case <-ctx.Done():
		t.Fatal("backend did not observe gRPC revocation")
	}
	remainder, err := io.ReadAll(events)
	require.NoError(t, err)
	require.NotContains(t, string(remainder), "event: end")
	_, data, found := strings.Cut(string(remainder), "event: error\ndata: ")
	require.True(t, found, "missing SSE error event: %s", remainder)
	var event struct {
		Code int `json:"code"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &event))
	require.Contains(t, []codes.Code{codes.Canceled, codes.PermissionDenied}, codes.Code(event.Code), "%s", data)
	select {
	case err := <-echo.httpDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-ctx.Done():
		t.Fatal("HTTP stream revocation did not reach the backend")
	}
	require.Equal(t, http.StatusForbidden, post(sse).StatusCode)
	require.Equal(t, http.StatusOK, post(metrics).StatusCode)
	require.EqualValues(t, 1, httpHandshakes.Load(), "HTTPS/2 reconnected after revocation")
	refused, err := client.FullDuplexCall(ctx)
	require.NoError(t, err)
	_, err = refused.Recv()
	require.Equal(t, codes.PermissionDenied, status.Code(err), "%v", err)
	exchangeManagedMessage(t, unaffected, "after revoke")
	_, err = gatewayClient.ListServices(ctx, &ynpb.ListServicesRequest{})
	require.Equal(t, codes.PermissionDenied, status.Code(err), "%v", err)
	otherConn, _ := managedClient(t, ca, address)
	_, err = ynpb.NewGatewayClient(otherConn).ListServices(ctx, &ynpb.ListServicesRequest{}, grpc.WaitForReady(true))
	require.Equal(t, codes.PermissionDenied, status.Code(err), "%v", err)

	identityClient := ynpb.NewAuthServiceClient(conn)
	principal, err := introspectSelf(t, ctx, identityClient)
	require.NoError(t, err)
	require.Equal(t, clientPolicyURI, principal.GetUser())
	removed := workloadPolicy(4, retained...)
	removed.Identities = "identities: []\n"
	removed.Publish(t, directory)
	require.Eventually(t, func() bool {
		_, err := introspectSelf(t, ctx, identityClient)
		return status.Code(err) == codes.Unauthenticated
	}, time.Second, 5*time.Millisecond, "removed account still authenticates")
	workloadPolicy(5, retained...).Publish(t, directory)
	require.Eventually(t, func() bool {
		principal, err := introspectSelf(t, ctx, identityClient)
		return err == nil && principal.GetUser() == clientPolicyURI
	}, time.Second, 5*time.Millisecond, "restored account did not authenticate")
	require.EqualValues(t, 1, handshakes.Load())
}

type managedFlowControlService struct {
	grpc_testing.UnimplementedTestServiceServer
	attemptingSend chan struct{}
	sendDone       chan error
}

func (m *managedFlowControlService) Name() string     { return "flow-control" }
func (m *managedFlowControlService) Endpoint() string { return "" }
func (m *managedFlowControlService) ServicesNames() []string {
	return []string{grpc_testing.TestService_ServiceDesc.ServiceName}
}
func (m *managedFlowControlService) RegisterService(server *grpc.Server) {
	grpc_testing.RegisterTestServiceServer(server, m)
}

func (m *managedFlowControlService) StreamingOutputCall(request *grpc_testing.StreamingOutputCallRequest, stream grpc.ServerStreamingServer[grpc_testing.StreamingOutputCallResponse]) error {
	// Larger than the receiver's fixed stream window and transport quota.
	reply := &grpc_testing.StreamingOutputCallResponse{Payload: &grpc_testing.Payload{Body: make([]byte, 2<<20)}}
	if _, err := rand.Read(reply.Payload.Body); err != nil {
		return err
	}
	if err := stream.Send(reply); err != nil {
		return err
	}
	close(m.attemptingSend)
	err := stream.Send(reply)
	m.sendDone <- err
	return err
}

// Test_Gateway_ManagedPolicy_UnblocksTransportWrite verifies revoke releases a
// Send blocked by flow control without first letting the client read.
func Test_Gateway_ManagedPolicy_UnblocksTransportWrite(t *testing.T) {
	t.Parallel()
	ca := tlscert.NewCA(t)
	directory := t.TempDir()
	method := grpc_testing.TestService_StreamingOutputCall_FullMethodName
	list := ynpb.Gateway_ListServices_FullMethodName
	workloadPolicy(1, method, list).Publish(t, directory)
	service := &managedFlowControlService{attemptingSend: make(chan struct{}), sendDone: make(chan error, 1)}
	address := startX509Gateway(t, ca, managedX509GatewayConfig(t, ca, directory), gateway.WithBuiltinService(service))
	conn, handshakes := managedClient(t, ca, address,
		grpc.WithStaticStreamWindowSize(64<<10), grpc.WithStaticConnWindowSize(1<<20),
	)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := grpc_testing.NewTestServiceClient(conn).StreamingOutputCall(ctx,
		&grpc_testing.StreamingOutputCallRequest{}, grpc.WaitForReady(true),
	)
	require.NoError(t, err)
	select {
	case <-service.attemptingSend:
	case <-ctx.Done():
		t.Fatal("server did not queue its first message")
	}
	select {
	case err := <-service.sendDone:
		t.Fatalf("second send completed without a window: %v", err)
	default:
	}
	workloadPolicy(2, list).Publish(t, directory)
	select {
	case err := <-service.sendDone:
		require.Equal(t, codes.Canceled, status.Code(err), "revoked transport write: %v", err)
	case <-ctx.Done():
		t.Fatal("revocation did not unblock the transport write")
	}
	_, err = ynpb.NewGatewayClient(conn).ListServices(ctx, &ynpb.ListServicesRequest{})
	require.NoError(t, err)
	require.EqualValues(t, 1, handshakes.Load())
}
