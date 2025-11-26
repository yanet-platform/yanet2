package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	commonpb "github.com/yanet-platform/yanet2/common/proto"
	adapterpb "github.com/yanet-platform/yanet2/modules/route/bird-adapter/proto"
)

// uint32SliceValue implements pflag.Value interface for []uint32
type uint32SliceValue []uint32

func (u *uint32SliceValue) String() string {
	if u == nil || len(*u) == 0 {
		return "[]"
	}
	strs := make([]string, len(*u))
	for i, v := range *u {
		strs[i] = strconv.FormatUint(uint64(v), 10)
	}
	return "[" + strings.Join(strs, ",") + "]"
}

func (u *uint32SliceValue) Set(value string) error {
	parts := strings.Split(value, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		val, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return fmt.Errorf("invalid uint32 value '%s': %w", part, err)
		}
		*u = append(*u, uint32(val))
	}
	return nil
}

func (u *uint32SliceValue) Type() string {
	return "uint32Slice"
}

var clientCmdArgs struct {
	ServerConfigPath string
	ConfigName       string
	Instances        uint32SliceValue
	Sockets          []string
}

var clientCmd = &cobra.Command{
	Use:   "client",
	Short: "Configure the BIRD adapter server",
	Long: `Send configuration to the BIRD adapter gRPC server.
This command connects to the adapter server and configures BIRD route import
for the specified dataplane instances.`,
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
	clientCmd.Flags().Var(&clientCmdArgs.Instances, "instances", "Comma-separated list of dataplane instance IDs (required)")
	clientCmd.Flags().StringSliceVar(&clientCmdArgs.Sockets, "sockets", nil, "List of BIRD socket paths (required)")

	clientCmd.MarkFlagRequired("server-config")
	clientCmd.MarkFlagRequired("config")
	clientCmd.MarkFlagRequired("instances")
	clientCmd.MarkFlagRequired("sockets")
}

func runClient() error {
	if len(clientCmdArgs.Instances) == 0 {
		return fmt.Errorf("at least one instance ID must be provided")
	}

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

	// Send configuration for each instance
	for _, instanceID := range clientCmdArgs.Instances {
		fmt.Printf("Configuring instance %d with config '%s'...\n", instanceID, clientCmdArgs.ConfigName)

		req := &adapterpb.SetupConfigRequest{
			Target: &commonpb.TargetModule{
				ConfigName:        clientCmdArgs.ConfigName,
				DataplaneInstance: instanceID,
			},
			Config: &adapterpb.ImportConfig{
				Sockets: clientCmdArgs.Sockets,
			},
		}

		_, err := client.SetupConfig(ctx, req)
		if err != nil {
			return fmt.Errorf("failed to setup config for instance %d: %w", instanceID, err)
		}

		fmt.Printf("Successfully configured instance %d\n", instanceID)
	}

	fmt.Println("All instances configured successfully")
	return nil
}
