package framework

import (
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"go.uber.org/zap"
)

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
	conn       net.Conn           // Active network connection to QEMU socket
	port       int                // TCP port number for TCP socket connections
	socketPath string             // Unix socket path for Unix socket connections
	timeout    time.Duration      // Default timeout for read/write operations
	log        *zap.SugaredLogger // Logger for debugging and monitoring
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
		sc.timeout = timeout
		return nil
	}
}

// SocketClientWithLog configures the SocketClient to use the specified logger
// for debugging and monitoring network operations. This enables detailed logging
// of packet flows, connection events, and error conditions.
//
// Parameters:
//   - log: A zap.SugaredLogger instance for structured logging
//
// Returns:
//   - SocketClientOption: Functional option that sets the logger
func SocketClientWithLog(log *zap.SugaredLogger) SocketClientOption {
	return func(sc *SocketClient) error {
		sc.log = log
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
		port:    port,
		timeout: 1 * time.Second,      // default 1s timeout
		log:     zap.NewNop().Sugar(), // default noop logger
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
		socketPath: socketPath,
		timeout:    100 * time.Millisecond,
		log:        zap.NewNop().Sugar(), // default noop logger
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
	var err error

	if sc.conn != nil {
		return nil
	}

	// Try to connect with retries
	for i := range 10 {
		if sc.socketPath != "" {
			// Unix socket connection (legacy)
			sc.conn, err = net.Dial("unix", sc.socketPath)
			if err == nil {
				sc.log.Debugf("Connected to Unix socket at %s", sc.socketPath)
				return nil
			}
			sc.log.Warnf("Unix socket connection attempt %d failed: %v, retrying...", i+1, err)
		} else {
			// TCP socket connection to QEMU
			sc.conn, err = net.Dial("tcp", fmt.Sprintf("localhost:%d", sc.port))
			if err == nil {
				sc.log.Debugf("Connected to TCP socket on port %d", sc.port)
				return nil
			}
			sc.log.Warnf("TCP connection attempt %d failed: %v, retrying...", i+1, err)
		}

		time.Sleep(2 * time.Second)
	}

	if sc.socketPath != "" {
		return fmt.Errorf("failed to connect to Unix socket %s after 10 attempts: %w", sc.socketPath, err)
	} else {
		return fmt.Errorf("failed to connect to TCP socket on port %d after 10 attempts: %w", sc.port, err)
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
//
// The packet format follows QEMU socket networking protocol:
//
//	[4-byte length][packet data]
//
// Parameters:
//   - packet: Raw packet bytes to transmit through the socket
//
// Returns:
//   - error: An error if the connection is not established, timeout occurs, or transmission fails
//
// Example:
//
//	packetData := []byte{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa1, ...} // Ethernet frame
//	if err := client.SendPacket(packetData); err != nil {
//	    log.Fatalf("Packet transmission failed: %v", err)
//	}
func (sc *SocketClient) SendPacket(packet []byte) error {
	if sc.conn == nil {
		return fmt.Errorf("not connected to socket")
	}

	err := sc.conn.SetWriteDeadline(time.Now().Add(sc.timeout))
	if err != nil {
		return fmt.Errorf("failed to set write deadline: %w", err)
	}

	// Create a buffer with the packet length in network byte order followed by the packet data
	packetWithLength := make([]byte, 4+len(packet))
	binary.BigEndian.PutUint32(packetWithLength, uint32(len(packet)))
	copy(packetWithLength[4:], packet)

	sc.log.Debugf("Sending packet with length prefix: % x", packetWithLength)

	_, err = sc.conn.Write(packetWithLength)
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
//
// The method continuously reads packets until it finds one with the correct
// destination MAC address matching the test framework's source MAC, ensuring
// proper packet isolation in multi-test scenarios.
//
// Parameters:
//   - timeout: Maximum time to wait for packet reception
//
// Returns:
//   - []byte: Raw packet data with correct destination MAC address
//   - error: An error if connection fails, timeout occurs, or no matching packet is received
//
// Example:
//
//	packet, err := client.ReceivePacket(5 * time.Second)
//	if err != nil {
//	    log.Fatalf("Packet reception failed: %v", err)
//	}
//	fmt.Printf("Received packet: %x", packet)
func (sc *SocketClient) ReceivePacket(timeout time.Duration) ([]byte, error) {
	if sc.conn == nil {
		return nil, fmt.Errorf("not connected to socket")
	}

	// Create packet parser for filtering
	parser := NewPacketParser()
	ourMAC := MustParseMAC(SrcMAC)

	// Keep reading packets until we find one with the correct SrcMAC
	for {
		err := sc.conn.SetReadDeadline(time.Now().Add(timeout))
		if err != nil {
			return nil, fmt.Errorf("failed to set read deadline: %w", err)
		}

		// Read the packet length prefix (4 bytes)
		lengthPrefix := make([]byte, 4)
		_, err = sc.conn.Read(lengthPrefix)
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
		_, err = sc.conn.Read(packetData)
		if err != nil {
			return nil, fmt.Errorf("failed to read packet data: %w", err)
		}
		sc.log.Debugf("Received packet data: % x", packetData)

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
	if sc.conn != nil {
		err := sc.conn.Close()
		sc.conn = nil // Make Close() idempotent - safe to call multiple times
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
	return sc.port
}
