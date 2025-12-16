package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/yanet-platform/yanet2/common/commonpb"
	adapterpb "github.com/yanet-platform/yanet2/modules/route/bird-adapter/proto"
)

var clientCmdArgs struct {
	ServerConfigPath string
	ConfigName       string
	Sockets          []string
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
	)
	if err != nil {
		return fmt.Errorf("failed to connect to adapter server: %w", err)
	}
	defer conn.Close()

	client := adapterpb.NewAdapterServiceClient(conn)

	fmt.Printf("Configuring with config '%s'...\n", clientCmdArgs.ConfigName)

	req := &adapterpb.SetupConfigRequest{
		Target: &commonpb.TargetModule{
			ConfigName: clientCmdArgs.ConfigName,
		},
		Config: &adapterpb.ImportConfig{
			Sockets: clientCmdArgs.Sockets,
		},
	}

	_, err = client.SetupConfig(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to setup config: %w", err)
	}

	fmt.Println("Successfully configured")
	return nil
}
