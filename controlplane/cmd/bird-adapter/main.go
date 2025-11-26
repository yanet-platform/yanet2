package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/yanet-platform/yanet2/controlplane/internal/version"
)

var rootCmd = &cobra.Command{
	Use:     "yanet-bird-adapter",
	Short:   "YANET BIRD route adapter service",
	Version: version.Version(),
}

func init() {
	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(clientCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
}
