package gateway

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/siderolabs/grpc-proxy/proxy"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/yanet-platform/yanet2/controlplane/internal/xgrpc"
)

// TransparentWebGRPCProxy is an HTTP handler that translates HTTP requests
// with binary protobuf payloads to gRPC calls.
type TransparentWebGRPCProxy struct {
	registry *BackendRegistry
	log      *zap.SugaredLogger
}

// NewHTTPHandler creates a new HTTPHandler.
func NewHTTPHandler(registry *BackendRegistry, log *zap.SugaredLogger) *TransparentWebGRPCProxy {
	return &TransparentWebGRPCProxy{
		registry: registry,
		log:      log,
	}
}

// ServeHTTP handles HTTP requests.
func (m *TransparentWebGRPCProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Only support POST method for actual gRPC calls.
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed. Only POST is supported", http.StatusMethodNotAllowed)
		return
	}

	// Convert URL path to gRPC fullMethodName format.
	fullMethodName := r.URL.Path
	fullMethodName = strings.TrimPrefix(fullMethodName, "/api")
	if !strings.HasPrefix(fullMethodName, "/") {
		fullMethodName = "/" + fullMethodName
	}

	service, method, err := xgrpc.ParseFullMethod(fullMethodName)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid method name: %v", err), http.StatusBadRequest)
		return
	}

	backend, ok := m.registry.GetBackend(service)
	if !ok {
		http.Error(w, fmt.Sprintf("Service not found: %s", service), http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read request body: %v", err), http.StatusBadRequest)
		return
	}

	requestMsg, err := m.jsonToProtobuf(fullMethodName, body, true)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse JSON request: %v", err), http.StatusBadRequest)
		return
	}
	protobufBody, err := proto.Marshal(requestMsg)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal protobuf: %v", err), http.StatusInternalServerError)
		return
	}

	// Create context with metadata from HTTP headers.
	ctx := r.Context()
	md := metadata.New(nil)
	for k, v := range r.Header {
		k = strings.ToLower(k)
		if !strings.HasPrefix(k, "sec-") && k != "host" && k != "connection" {
			md.Set(k, v...)
		}
	}
	ctx = metadata.NewOutgoingContext(ctx, md)

	outCtx, conn, err := backend.GetConnection(ctx, fullMethodName)
	if err != nil {
		m.log.Errorw("failed to get connection to backend",
			zap.String("service", service),
			zap.String("method", method),
			zap.Error(err),
		)
		http.Error(w, fmt.Sprintf("Failed to connect to backend: %v", err), http.StatusInternalServerError)
		return
	}

	// We use a custom codec to directly send/receive binary protobuf data.
	responseBuffer := proxy.NewFrame(nil)

	// Invoke the gRPC method with the binary protobuf payload.
	err = conn.Invoke(
		outCtx,
		fullMethodName,
		proxy.NewFrame(protobufBody),
		responseBuffer,
		grpc.ForceCodecV2(proxy.Codec()),
	)
	if err != nil {
		m.log.Errorw("failed to proxy gRPC",
			zap.String("service", service),
			zap.String("method", method),
			zap.Error(err),
		)

		// Convert gRPC error to HTTP error
		statusErr, ok := status.FromError(err)
		if ok {
			code := statusErr.Code()
			http.Error(w, statusErr.Message(), grpcCodeToHTTPStatus(code))
		} else {
			http.Error(w, fmt.Sprintf("failed to proxy gRPC: %v", err), http.StatusInternalServerError)
		}
		return
	}

	// We need to re-marshal the response to get the binary data.
	codec := proxy.Codec()
	bufSlice, err := codec.Marshal(responseBuffer)
	if err != nil {
		m.log.Errorw("failed to marshal response",
			zap.String("service", service),
			zap.String("method", method),
			zap.Error(err),
		)
		http.Error(w, fmt.Sprintf("Failed to marshal response: %v", err), http.StatusInternalServerError)
		return
	}

	// Materialize the buffer slice.
	respData := bufSlice.Materialize()
	responseMessage, err := m.protobufToMessage(fullMethodName, respData, false)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to unmarshal protobuf response: %v", err), http.StatusInternalServerError)
		return
	}

	options := protojson.MarshalOptions{
		UseEnumNumbers: true,
	}
	responseBody, err := options.Marshal(responseMessage)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal JSON response: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(responseBody); err != nil {
		m.log.Errorw("failed to write HTTP response",
			zap.String("service", service),
			zap.String("method", method),
			zap.Error(err),
		)
	}
}

// jsonToProtobuf converts JSON to protobuf message for the given method.
func (m *TransparentWebGRPCProxy) jsonToProtobuf(fullMethodName string, jsonData []byte, isRequest bool) (proto.Message, error) {
	ty, err := m.getMessageType(fullMethodName, isRequest)
	if err != nil {
		return nil, err
	}

	msg := ty.New().Interface()
	if err := protojson.Unmarshal(jsonData, msg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	return msg, nil
}

// protobufToMessage converts protobuf bytes to message.
func (m *TransparentWebGRPCProxy) protobufToMessage(fullMethodName string, protobufData []byte, isRequest bool) (proto.Message, error) {
	ty, err := m.getMessageType(fullMethodName, isRequest)
	if err != nil {
		return nil, err
	}

	msg := ty.New().Interface()
	if err := proto.Unmarshal(protobufData, msg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal protobuf: %w", err)
	}

	return msg, nil
}

// getMessageType returns the protobuf message type for the given method.
func (m *TransparentWebGRPCProxy) getMessageType(fullMethodName string, isRequest bool) (protoreflect.MessageType, error) {
	service, method, err := xgrpc.ParseFullMethod(fullMethodName)
	if err != nil {
		return nil, err
	}

	serviceName := protoreflect.FullName(service)
	serviceDesc, err := protoregistry.GlobalFiles.FindDescriptorByName(serviceName)
	if err != nil {
		return nil, fmt.Errorf("service not found: %s", service)
	}

	svcDesc, ok := serviceDesc.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("descriptor is not a service: %s", service)
	}

	methodDesc := svcDesc.Methods().ByName(protoreflect.Name(method))
	if methodDesc == nil {
		return nil, fmt.Errorf("method not found: %s.%s", service, method)
	}

	var msgDesc protoreflect.MessageDescriptor
	if isRequest {
		msgDesc = methodDesc.Input()
	} else {
		msgDesc = methodDesc.Output()
	}

	msgType, err := protoregistry.GlobalTypes.FindMessageByName(msgDesc.FullName())
	if err != nil {
		return nil, fmt.Errorf("message type not found: %s", msgDesc.FullName())
	}

	return msgType, nil
}

// grpcCodeToHTTPStatus maps gRPC status codes to HTTP status codes.
func grpcCodeToHTTPStatus(code codes.Code) int {
	switch code {
	case codes.OK:
		return http.StatusOK
	case codes.Canceled:
		return http.StatusRequestTimeout
	case codes.Unknown:
		return http.StatusInternalServerError
	case codes.InvalidArgument:
		return http.StatusBadRequest
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout
	case codes.NotFound:
		return http.StatusNotFound
	case codes.AlreadyExists:
		return http.StatusConflict
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests
	case codes.FailedPrecondition:
		return http.StatusBadRequest
	case codes.Aborted:
		return http.StatusConflict
	case codes.OutOfRange:
		return http.StatusBadRequest
	case codes.Unimplemented:
		return http.StatusNotImplemented
	case codes.Internal:
		return http.StatusInternalServerError
	case codes.Unavailable:
		return http.StatusServiceUnavailable
	case codes.DataLoss:
		return http.StatusInternalServerError
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	default:
		return http.StatusInternalServerError
	}
}
