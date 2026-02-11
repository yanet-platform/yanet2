package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip"

	adapterpb "github.com/yanet-platform/yanet2/modules/route/bird-adapter/proto"
)

// logLevelFlag wraps zapcore.Level to implement pflag.Value interface.
type logLevelFlag struct {
	zapcore.Level
}

func (f *logLevelFlag) Type() string {
	return "level"
}

var clientCmdArgs struct {
	ServerConfigPath string
	ConfigName       string
	Sockets          []string
	LogLevel         logLevelFlag
}

func init() {
	// Initialize LogLevel with InvalidLevel to distinguish "not set" from "info" (0)
	clientCmdArgs.LogLevel.Level = zapcore.InvalidLevel
}

var clientCmd = &cobra.Command{
	Use:   "client",
	Short: "Configure the BIRD adapter server",
	Long: `Send configuration to the BIRD adapter gRPC server.
This command connects to the adapter server and configures BIRD route import.`,
	Run: func(cmd *cobra.Command, args []string) {
		if err := runClient(); err != nil {
			fmt.Printf("ERROR: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	clientCmd.Flags().StringVarP(&clientCmdArgs.ServerConfigPath, "server-config", "s", "", "Path to the server configuration file (required)")
	clientCmd.Flags().StringVar(&clientCmdArgs.ConfigName, "config", "", "Configuration name (required)")
	clientCmd.Flags().StringSliceVar(&clientCmdArgs.Sockets, "sockets", nil, "List of BIRD socket paths (required)")
	clientCmd.Flags().Var(&clientCmdArgs.LogLevel, "log-level", "Log level for this client. If not set, logging is disabled.")

	clientCmd.MarkFlagRequired("server-config")
	clientCmd.MarkFlagRequired("config")
	clientCmd.MarkFlagRequired("sockets")
}

func runClient() error {
	if len(clientCmdArgs.Sockets) == 0 {
		return fmt.Errorf("at least one BIRD socket path must be provided")
	}

	// Load server config to get the adapter address
	serverCfg, err := LoadServerConfig(clientCmdArgs.ServerConfigPath)
	if err != nil {
		return fmt.Errorf("failed to load server config: %w", err)
	}

	fmt.Printf("Connecting to BIRD adapter at %s...\n", serverCfg.ListenAddr)

	// Create gRPC connection to the adapter server
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(
		serverCfg.ListenAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.UseCompressor(gzip.Name)),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to adapter server: %w", err)
	}
	defer conn.Close()

	client := adapterpb.NewAdapterServiceClient(conn)

	fmt.Printf("Configuring with config '%s'...\n", clientCmdArgs.ConfigName)

	var logLevel string
	if clientCmdArgs.LogLevel.Level != zapcore.InvalidLevel {
		logLevel = clientCmdArgs.LogLevel.String()
	}

	req := &adapterpb.SetupConfigRequest{
		Name: clientCmdArgs.ConfigName,
		Config: &adapterpb.ImportConfig{
			Sockets:  clientCmdArgs.Sockets,
			LogLevel: logLevel,
		},
	}

	_, err = client.SetupConfig(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to setup config: %w", err)
	}

	fmt.Println("Successfully configured")
	return nil
}

var listSessionsCmdArgs struct {
	ServerConfigPath string
}

var listSessionsCmd = &cobra.Command{
	Use:   "list-sessions",
	Short: "List active BIRD import sessions",
	Long:  `List all active BIRD import sessions on the adapter server.`,
	Run: func(cmd *cobra.Command, args []string) {
		if err := runListSessions(); err != nil {
			fmt.Printf("ERROR: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	listSessionsCmd.Flags().StringVarP(&listSessionsCmdArgs.ServerConfigPath, "server-config", "s", "", "Path to the server configuration file (required)")
	listSessionsCmd.MarkFlagRequired("server-config")
}

func runListSessions() error {
	// Load server config to get the adapter address
	serverCfg, err := LoadServerConfig(listSessionsCmdArgs.ServerConfigPath)
	if err != nil {
		return fmt.Errorf("failed to load server config: %w", err)
	}

	// Create gRPC connection to the adapter server
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(
		serverCfg.ListenAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.UseCompressor(gzip.Name)),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to adapter server: %w", err)
	}
	defer conn.Close()

	client := adapterpb.NewAdapterServiceClient(conn)

	resp, err := client.ListSessions(ctx, &adapterpb.ListSessionsRequest{})
	if err != nil {
		return fmt.Errorf("failed to list sessions: %w", err)
	}

	if len(resp.Sessions) == 0 {
		fmt.Println("No active sessions")
		return nil
	}

	fmt.Printf("Active sessions (%d):\n", len(resp.Sessions))
	fmt.Println(strings.Repeat("-", 80))

	for _, session := range resp.Sessions {
		createdAt := time.Unix(0, session.CreatedAt)
		uptime := time.Since(createdAt).Round(time.Second)

		connStateStr := connectionStateToString(session.ConnectionState)

		fmt.Printf("Name:       %s\n", session.Name)
		fmt.Printf("Sockets:    %s\n", strings.Join(session.Sockets, ", "))
		fmt.Printf("Created:    %s (uptime: %s)\n", createdAt.Format(time.RFC3339), uptime)
		fmt.Printf("Connection: %s\n", connStateStr)
		fmt.Println(strings.Repeat("-", 80))
	}

	return nil
}

func connectionStateToString(state adapterpb.ConnectionState) string {
	switch state {
	case adapterpb.ConnectionState_CONNECTION_STATE_IDLE:
		return "IDLE"
	case adapterpb.ConnectionState_CONNECTION_STATE_CONNECTING:
		return "CONNECTING"
	case adapterpb.ConnectionState_CONNECTION_STATE_READY:
		return "READY"
	case adapterpb.ConnectionState_CONNECTION_STATE_TRANSIENT_FAILURE:
		return "TRANSIENT_FAILURE"
	case adapterpb.ConnectionState_CONNECTION_STATE_SHUTDOWN:
		return "SHUTDOWN"
	default:
		return "UNKNOWN"
	}
}
