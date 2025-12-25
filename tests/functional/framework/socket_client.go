package framework

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"

	"go.uber.org/zap"
)

// socketClientInner holds the shared connection state that should not be copied
// between SocketClient instances. This allows multiple SocketClient wrappers
// with different loggers to share the same underlying connection.
type socketClientInner struct {
	conn       net.Conn      // Active network connection to QEMU socket
	connMutex  sync.Mutex    // Protects conn field during Connect/Close operations
	port       int           // TCP port number for TCP socket connections
	socketPath string        // Unix socket path for Unix socket connections
	timeout    time.Duration // Default timeout for read/write operations
}

// SocketClient handles bidirectional communication with QEMU virtual machine
// network interfaces through socket connections. It supports both TCP and Unix
// socket connections for packet injection and capture in testing scenarios.
//
// The client provides:
//   - Reliable packet transmission with length prefixing
//   - Packet reception with filtering and timeout handling
//   - Connection management with automatic retry logic
//   - MAC address-based packet filtering for test isolation
//   - Configurable timeouts and logging for debugging
//
// All network operations include proper error handling and timeout management
// to ensure robust testing in various network conditions.
type SocketClient struct {
	inner *socketClientInner // Shared connection state (not copied)
	log   *zap.SugaredLogger // Logger for debugging and monitoring (can be different per instance)
}

// SocketClientOption defines functional options for configuring SocketClient
// instances. This pattern allows flexible initialization with optional parameters
// while maintaining clean API design and backward compatibility.
type SocketClientOption func(*SocketClient) error

// WithTimeout configures the default timeout for socket read and write operations.
// This timeout applies to both packet transmission and reception operations,
// providing consistent behavior across all network interactions.
//
// Parameters:
//   - timeout: Duration for socket operation timeouts (must be positive)
//
// Returns:
//   - SocketClientOption: Functional option that sets the timeout
//   - error: An error if the timeout value is invalid (zero or negative)
//
// Example:
//
//	client, err := NewSocketClient(path, WithTimeout(5*time.Second))
func WithTimeout(timeout time.Duration) SocketClientOption {
	return func(sc *SocketClient) error {
		if timeout <= 0 {
			return fmt.Errorf("timeout must be positive, got: %v", timeout)
		}
		sc.inner.timeout = timeout
		return nil
	}
}

// NewSocketClientTCP creates a new socket client configured for TCP connections
// to QEMU network interfaces. This method is used when QEMU is configured with
// TCP socket networking for packet injection and capture.
//
// The client is initialized with default settings and can be customized using
// functional options for timeout configuration and logging setup.
//
// Parameters:
//   - port: TCP port number for QEMU socket connection (1-65535)
//   - opts: Optional functional options for client customization
//
// Returns:
//   - *SocketClient: Configured socket client ready for TCP connections
//   - error: An error if the port number is invalid or options cannot be applied
//
// Example:
//
//	client, err := NewSocketClientTCP(8080, WithTimeout(10*time.Second))
//	if err != nil {
//	    log.Fatalf("Failed to create TCP socket client: %v", err)
//	}
func NewSocketClientTCP(port int, opts ...SocketClientOption) (*SocketClient, error) {
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port number: %d", port)
	}

	sc := &SocketClient{
		inner: &socketClientInner{
			port:    port,
			timeout: 1 * time.Second, // default 1s timeout
		},
		log: zap.NewNop().Sugar(), // default noop logger
	}

	// Apply functional options
	for _, opt := range opts {
		if err := opt(sc); err != nil {
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}

	return sc, nil
}

// NewSocketClient creates a new socket client configured for Unix socket connections
// to QEMU network interfaces. This method provides the primary interface for
// packet injection and capture through Unix domain sockets.
//
// Unix socket connections offer better performance and lower latency compared to
// TCP sockets, making them ideal for high-throughput network testing scenarios.
//
// Parameters:
//   - socketPath: Path to the Unix socket file created by QEMU
//   - opts: Optional functional options for client customization
//
// Returns:
//   - *SocketClient: Configured socket client ready for Unix socket connections
//   - error: An error if the socket path is empty or options cannot be applied
//
// Example:
//
//	client, err := NewSocketClient("/tmp/qemu-socket.sock", SocketClientWithLog(logger))
//	if err != nil {
//	    log.Fatalf("Failed to create Unix socket client: %v", err)
//	}
func NewSocketClient(socketPath string, opts ...SocketClientOption) (*SocketClient, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("socket path cannot be empty")
	}

	sc := &SocketClient{
		inner: &socketClientInner{
			socketPath: socketPath,
			timeout:    100 * time.Millisecond,
		},
		log: zap.NewNop().Sugar(), // default noop logger (will be replaced via WithLog)
	}

	// Apply functional options
	for _, opt := range opts {
		if err := opt(sc); err != nil {
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}

	return sc, nil
}

// Connect establishes a network connection to the QEMU socket interface with
// automatic retry logic and proper error handling. The method supports both
// TCP and Unix socket connections based on the client configuration.
//
// The connection process includes:
//   - Automatic detection of connection type (TCP vs Unix socket)
//   - Retry logic with exponential backoff for handling timing issues
//   - Proper error reporting and logging for debugging
//   - Connection state management to prevent duplicate connections
//
// The method attempts connection up to 10 times with 2-second intervals
// to accommodate QEMU startup timing and network interface availability.
//
// Returns:
//   - error: An error if connection cannot be established after all retry attempts
//
// Example:
//
//	if err := client.Connect(); err != nil {
//	    log.Fatalf("Failed to connect to QEMU socket: %v", err)
//	}
func (sc *SocketClient) Connect() error {
	// Lock to prevent concurrent connection attempts
	sc.inner.connMutex.Lock()
	defer sc.inner.connMutex.Unlock()

	// Check if already connected
	if sc.inner.conn != nil {
		return nil
	}

	var err error
	// Try to connect with retries
	for i := range 10 {
		if sc.inner.socketPath != "" {
			// Unix socket connection
			sc.inner.conn, err = net.Dial("unix", sc.inner.socketPath)
			if err == nil {
				sc.log.Debugf("Connected to Unix socket at %s", sc.inner.socketPath)
				return nil
			}
			sc.log.Warnf("Unix socket connection attempt %d failed: %v, retrying...", i+1, err)
		} else {
			// TCP socket connection to QEMU
			sc.inner.conn, err = net.Dial("tcp", fmt.Sprintf("localhost:%d", sc.inner.port))
			if err == nil {
				sc.log.Debugf("Connected to TCP socket on port %d", sc.inner.port)
				return nil
			}
			sc.log.Warnf("TCP connection attempt %d failed: %v, retrying...", i+1, err)
		}

		time.Sleep(2 * time.Second)
	}

	if sc.inner.socketPath != "" {
		return fmt.Errorf("failed to connect to Unix socket %s after 10 attempts: %w", sc.inner.socketPath, err)
	} else {
		return fmt.Errorf("failed to connect to TCP socket on port %d after 10 attempts: %w", sc.inner.port, err)
	}
}

// SendPacket transmits a raw network packet through the QEMU socket connection
// with proper length prefixing and timeout handling. The method implements the
// QEMU socket protocol by prefixing each packet with its length in network byte order.
//
// The transmission process includes:
//   - Connection state validation
//   - Write timeout configuration for reliable transmission
//   - Length prefix encoding in big-endian format
//   - Complete packet data transmission
//   - Comprehensive error handling and logging
//   - Optional dumping to file if dumpFilePaths is provided
//
// The packet format follows QEMU socket networking protocol:
//
//	[4-byte length][packet data]
//
// Parameters:
//   - packet: Raw packet bytes to transmit through the socket
//   - dumpPath: Path to dump file for recording socket data (empty string to skip dumping)
//
// Returns:
//   - error: An error if the connection is not established, timeout occurs, or transmission fails
//
// Example:
//
//	packetData := []byte{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa1, ...} // Ethernet frame
//	if err := client.SendPacket(packetData, "/path/to/dump.file"); err != nil {
//	    log.Fatalf("Packet transmission failed: %v", err)
//	}
func (sc *SocketClient) SendPacket(packet []byte, dumpPath string) error {
	if sc.inner.conn == nil {
		return fmt.Errorf("not connected to socket")
	}

	err := sc.inner.conn.SetWriteDeadline(time.Now().Add(sc.inner.timeout))
	if err != nil {
		return fmt.Errorf("failed to set write deadline: %w", err)
	}

	// Create a buffer with the packet length in network byte order followed by the packet data
	packetWithLength := make([]byte, 4+len(packet))
	binary.BigEndian.PutUint32(packetWithLength, uint32(len(packet)))
	copy(packetWithLength[4:], packet)

	sc.log.Debugf("Sending packet with length prefix: % x", packetWithLength)

	// Write raw socket data to dump file if path is provided
	if err := writeToDumpFile(dumpPath, packetWithLength); err != nil {
		sc.log.Warnf("Failed to write to dump file: %v", err)
	}

	_, err = sc.inner.conn.Write(packetWithLength)
	if err != nil {
		return fmt.Errorf("failed to send packet: %w", err)
	}

	return nil
}

// ReceivePacket captures a network packet from the QEMU socket connection with
// MAC address filtering and timeout handling. The method implements the QEMU
// socket protocol by reading length-prefixed packets and filtering based on
// destination MAC address for test isolation.
//
// The reception process includes:
//   - Connection state validation and timeout configuration
//   - Length prefix parsing from network byte order
//   - Packet size validation to prevent buffer overflows
//   - Complete packet data reception
//   - MAC address-based filtering for test packet isolation
//   - Automatic packet parsing and validation
//   - Optional dumping to file if dumpFilePaths is provided
//
// The method continuously reads packets until it finds one with the correct
// destination MAC address matching the test framework's source MAC, ensuring
// proper packet isolation in multi-test scenarios.
//
// Parameters:
//   - timeout: Maximum time to wait for packet reception
//   - dumpPath: Path to dump file for recording socket data (empty string to skip dumping)
//
// Returns:
//   - []byte: Raw packet data with correct destination MAC address
//   - error: An error if connection fails, timeout occurs, or no matching packet is received
//
// Example:
//
//	packet, err := client.ReceivePacket(5 * time.Second, "/path/to/dump.file")
//	if err != nil {
//	    log.Fatalf("Packet reception failed: %v", err)
//	}
//	fmt.Printf("Received packet: %x", packet)
func (sc *SocketClient) ReceivePacket(timeout time.Duration, dumpPath string) ([]byte, error) {
	if sc.inner.conn == nil {
		return nil, fmt.Errorf("not connected to socket")
	}

	// Create packet parser for filtering
	parser := NewPacketParser()
	ourMAC := MustParseMAC(SrcMAC)

	// Keep reading packets until we find one with the correct SrcMAC
	for {
		err := sc.inner.conn.SetReadDeadline(time.Now().Add(timeout))
		if err != nil {
			return nil, fmt.Errorf("failed to set read deadline: %w", err)
		}

		// Read the packet length prefix (4 bytes)
		lengthPrefix := make([]byte, 4)
		_, err = sc.inner.conn.Read(lengthPrefix)
		if err != nil {
			return nil, fmt.Errorf("failed to read packet length prefix: %w", err)
		}
		sc.log.Debugf("Received packet length prefix: % x", lengthPrefix)

		packetLength := binary.BigEndian.Uint32(lengthPrefix)
		if packetLength > 9000 {
			return nil, fmt.Errorf("packet length %d exceeds maximum buffer size", packetLength)
		}

		// Read the packet data
		packetData := make([]byte, packetLength)
		_, err = sc.inner.conn.Read(packetData)
		if err != nil {
			return nil, fmt.Errorf("failed to read packet data: %w", err)
		}
		sc.log.Debugf("Received packet data: % x", packetData)

		// Write raw socket data to dump file if path is provided (with length prefix)
		packetWithLength := make([]byte, 0, 4+len(packetData))
		packetWithLength = append(append(packetWithLength, lengthPrefix...), packetData...)
		if err := writeToDumpFile(dumpPath, packetWithLength); err != nil {
			sc.log.Warnf("Failed to write to dump file: %v", err)
		}

		// Parse the packet to check SrcMAC
		packetInfo, err := parser.ParsePacket(packetData)
		if err != nil {
			// If packet parsing fails, return it anyway (may be intentionally invalid)
			sc.log.Warnf("Failed to parse packet (may be intentionally invalid), returning anyway: %v", err)
			return packetData, nil
		}

		// Check if the packet has the correct SrcMAC
		if packetInfo.DstMAC.String() == ourMAC.String() {
			return packetData, nil
		}

		// Skip packets with incorrect SrcMAC
		sc.log.Debugf("Skipping packet with incorrect DstMAC: %s (expected: %s)", packetInfo.DstMAC, ourMAC)
	}
}

// Close gracefully terminates the socket connection and releases associated
// network resources. This method should be called when the socket client is
// no longer needed to prevent resource leaks.
//
// The method safely handles cases where no connection is established and
// provides proper error reporting for connection closure failures.
//
// Returns:
//   - error: An error if connection closure fails, or nil if successful or no connection exists
//
// Example:
//
//	defer func() {
//	    if err := client.Close(); err != nil {
//	        log.Errorf("Failed to close socket connection: %v", err)
//	    }
//	}()
func (sc *SocketClient) Close() error {
	sc.inner.connMutex.Lock()
	defer sc.inner.connMutex.Unlock()

	if sc.inner.conn != nil {
		err := sc.inner.conn.Close()
		sc.inner.conn = nil // Make Close() idempotent - safe to call multiple times
		return err
	}
	return nil
}

// GetSocketPort returns the TCP port number configured for this socket client.
// This method is useful for debugging and logging purposes, particularly when
// working with multiple socket clients on different ports.
//
// Returns:
//   - int: TCP port number, or 0 if the client is configured for Unix sockets
//
// Example:
//
//	port := client.GetSocketPort()
//	if port > 0 {
//	    log.Infof("Client configured for TCP port %d", port)
//	} else {
//	    log.Info("Client configured for Unix socket")
//	}
func (sc *SocketClient) GetSocketPort() int {
	return sc.inner.port
}

// WithLog creates a new SocketClient instance with a different logger
// while sharing the same underlying connection (inner state).
// This allows each test to have its own logging context while sharing
// the socket connection.
//
// Parameters:
//   - log: Logger to use for this client instance
//
// Returns:
//   - *SocketClient: A new socket client instance with the specified logger
//
// Example:
//
//	namedClient := client.WithLog(logger.Named("test1"))
func (sc *SocketClient) WithLog(log *zap.SugaredLogger) *SocketClient {
	return &SocketClient{
		inner: sc.inner, // Share the same inner state (connection)
		log:   log,
	}
}
