package lib

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// ControlplaneConfig represents yanet1 controlplane configuration
type ControlplaneConfig struct {
	Modules map[string]Module `json:"modules"`
}

// Module represents a module in yanet1 configuration
type Module struct {
	Type                    string                 `json:"type"`
	PhysicalPort            string                 `json:"physicalPort"`
	VlanId                  string                 `json:"vlanId"`
	MacAddress              string                 `json:"macAddress"`
	NextModule              string                 `json:"nextModule"`
	NextModules             []string               `json:"nextModules"`
	IPv6Prefixes            []string               `json:"ipv6_prefixes"`
	IPv4Prefixes            []string               `json:"ipv4_prefixes"`
	IPv6DestinationPrefixes []string               `json:"ipv6DestinationPrefixes"`
	IPv4DestinationPrefixes []string               `json:"ipv4DestinationPrefixes"`
	Translations            []NAT64Translation     `json:"translations"`
	Announces               []string               `json:"announces"`
	Interfaces              map[string]Interface   `json:"interfaces"`
	RawData                 map[string]interface{} `json:"-"` // For storing other fields
}

// NAT64Translation represents a NAT64 translation entry
type NAT64Translation struct {
	IPv6Address            string `json:"ipv6Address"`
	IPv6DestinationAddress string `json:"ipv6DestinationAddress"`
	IPv4Address            string `json:"ipv4Address"`
}

// Interface represents an interface in route module
type Interface struct {
	IPv6Prefix          string `json:"ipv6Prefix"`
	IPv4Prefix          string `json:"ipv4Prefix"`
	NeighborIPv6Address string `json:"neighborIPv6Address"`
	NeighborIPv4Address string `json:"neighborIPv4Address"`
	NeighborMacAddress  string `json:"neighborMacAddress"`
	NextModule          string `json:"nextModule"`
}

// parseControlplaneConfig parses yanet1 controlplane.conf file
func (c *Converter) parseControlplaneConfig(configPath string) (*ControlplaneConfig, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read controlplane config: %w", err)
	}

	var config ControlplaneConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse controlplane config: %w", err)
	}

	// Validate required fields
	if config.Modules == nil {
		return nil, fmt.Errorf("controlplane config missing 'modules' field")
	}

	if len(config.Modules) == 0 {
		c.debugLog("Warning: controlplane config has empty modules map")
	}

	// Validate module structure
	for moduleName, module := range config.Modules {
		if module.Type == "" {
			return nil, fmt.Errorf("module %s has no type specified", moduleName)
		}
		c.debugLog("Found module: %s (type: %s)", moduleName, module.Type)
	}

	return &config, nil
}

// extractModuleInventory scans the parsed controlplane config and builds a module inventory
func (c *Converter) extractModuleInventory(config *ControlplaneConfig) moduleInventory {
	inv := moduleInventory{
		balancerModules: make(map[string]struct{}),
	}

	if config == nil || config.Modules == nil {
		return inv
	}

	for moduleName, module := range config.Modules {
		switch module.Type {
		case "nat64stateful":
			// Take the first NAT64 module found
			if inv.nat64Module == "" {
				inv.nat64Module = moduleName
			}
		case "balancer":
			inv.balancerModules[moduleName] = struct{}{}
			// Set first balancer as default
			if inv.defaultBalancer == "" {
				inv.defaultBalancer = moduleName
			}
		}
	}

	return inv
}

// validateModuleName checks if a module name exists in the inventory for the given type
func (c *Converter) validateModuleName(moduleName, moduleType string) error {
	switch moduleType {
	case "nat64":
		if c.moduleInventory.nat64Module == "" {
			return fmt.Errorf("NAT64 module not found in controlplane config")
		}
		if moduleName != "" && moduleName != c.moduleInventory.nat64Module {
			return fmt.Errorf("NAT64 module '%s' not found in config, available: '%s'",
				moduleName, c.moduleInventory.nat64Module)
		}
	case "balancer":
		if len(c.moduleInventory.balancerModules) == 0 {
			return fmt.Errorf("no balancer modules found in controlplane config")
		}
		if moduleName != "" {
			if _, exists := c.moduleInventory.balancerModules[moduleName]; !exists {
				var available []string
				for name := range c.moduleInventory.balancerModules {
					available = append(available, name)
				}
				return fmt.Errorf("balancer module '%s' not found in config, available: %v",
					moduleName, available)
			}
		}
	}
	return nil
}

// getDefaultModuleName returns the default module name for a given type
func (c *Converter) getDefaultModuleName(moduleType string) string {
	switch moduleType {
	case "nat64":
		return c.moduleInventory.nat64Module
	case "balancer":
		return c.moduleInventory.defaultBalancer
	}
	return ""
}

// generateForwardModuleCommands generates commands for forward module based on logicalPort
func (c *Converter) generateForwardModuleCommands(config *ControlplaneConfig) []string {
	// Forward module is already configured in framework.go, no additional commands needed
	return nil
}

// convertBalancerCommand converts balancer commands using CLI utilities
func (c *Converter) convertBalancerCommand(cmd *CLICommand) string {
	if cmd.Subcommand != "real" {
		return fmt.Sprintf(`"# Unsupported balancer subcommand: %s"`, cmd.Subcommand)
	}

	if len(cmd.Parameters) < 2 {
		return fmt.Sprintf(`"# Invalid balancer real command: %s"`, cmd.Raw)
	}

	action := cmd.Parameters[0] // enable, disable, flush
	module := cmd.Parameters[1]

	// Validate module name
	if err := c.validateModuleName(module, "balancer"); err != nil {
		c.debugLog("Warning: %v, using module anyway", err)
	}

	builder := NewCommandBuilder(framework.CLIBalancer).
		Action("real").
		Config(module).
		Instances(0)

	switch action {
	case "enable", "disable":
		if len(cmd.Parameters) >= 6 {
			virtualIP := cmd.Parameters[2]
			proto := cmd.Parameters[3]
			virtualPort := cmd.Parameters[4]
			realIP := cmd.Parameters[5]
			realPort := "any"
			if len(cmd.Parameters) >= 7 {
				realPort = cmd.Parameters[6]
			}

			builder.Param("virtual-ip", virtualIP).
				Param("proto", proto).
				Param("virtual-port", virtualPort).
				Param("real-ip", realIP).
				Param("real-port", realPort).
				Param(action, "")
		}
	case "flush":
		builder.Param(action, "")
	default:
		return fmt.Sprintf(`"# Unsupported balancer real action: %s"`, action)
	}

	return fmt.Sprintf(`"%s"`, builder.Build())
}

// convertNat64Command converts nat64 commands using CLI utilities
func (c *Converter) convertNat64Command(cmd *CLICommand) string {
	// Get NAT64 module name from inventory
	nat64Module := c.getDefaultModuleName("nat64")
	if nat64Module == "" {
		nat64Module = "nat64_0" // fallback
		c.debugLog("Warning: NAT64 module not found in config, using fallback: %s", nat64Module)
	}

	if cmd.Subcommand == "prefix" && len(cmd.Parameters) >= 2 && cmd.Parameters[0] == "add" {
		prefix := cmd.Parameters[1]
		return NewCommandBuilder(framework.CLINAT64).
			Action("prefix").
			Action("add").
			Config(nat64Module).
			Instances(0).
			Param("prefix", prefix).
			Build()
	}

	if cmd.Subcommand == "mapping" && len(cmd.Parameters) >= 2 && cmd.Parameters[0] == "add" {
		ipv4 := cmd.Parameters[1]
		ipv6 := ""
		if len(cmd.Parameters) >= 3 {
			ipv6 = cmd.Parameters[2]
		}

		return NewCommandBuilder(framework.CLINAT64).
			Action("mapping").
			Action("add").
			Config(nat64Module).
			Instances(0).
			Param("ipv4", ipv4).
			Param("ipv6", ipv6).
			Param("prefix-index", "0").
			Build()
	}

	if cmd.Subcommand == "drop" {
		dropCmd := strings.Join(cmd.Parameters, " ")
		return NewCommandBuilder(framework.CLINAT64).
			Action("drop").
			Config(nat64Module).
			Instances(0).
			Build() + " " + dropCmd
	}

	// Default for NAT64 commands
	return fmt.Sprintf(`"%s %s"`, framework.CLINAT64, cmd.Subcommand)
}

// convertRouteCommand converts route commands using CLI utilities
func (c *Converter) convertRouteCommand(cmd *CLICommand) string {
	if cmd.Subcommand != "insert" && cmd.Subcommand != "remove" {
		return fmt.Sprintf(`"# Unsupported route subcommand: %s"`, cmd.Subcommand)
	}

	builder := NewCommandBuilder(framework.CLIRoute).
		Action(cmd.Subcommand).
		Config("route0").
		Instances(0)

	if cmd.Subcommand == "insert" {
		via, found := ExtractParameter(cmd, "via")
		if found {
			builder.Param("via", via)
		}

		label, found := ExtractParameter(cmd, "label")
		if found {
			builder.Param("label", label)
		}

		// Add network prefix as final parameter
		if len(cmd.Parameters) > 0 {
			// Find the network prefix (usually first parameter or after 'via')
			prefix := cmd.Parameters[0]
			if prefix == "insert" {
				// Skip the action keyword
				if len(cmd.Parameters) > 1 {
					prefix = cmd.Parameters[1]
				}
			}
			result := builder.Build()
			return fmt.Sprintf(`"%s %s"`, result, prefix)
		}
	} else if cmd.Subcommand == "remove" && len(cmd.Parameters) > 0 {
		// Remove command - prefix is usually first parameter
		prefix := cmd.Parameters[0]
		if prefix == "remove" {
			if len(cmd.Parameters) > 1 {
				prefix = cmd.Parameters[1]
			}
		}
		result := builder.Build()
		return fmt.Sprintf(`"%s %s"`, result, prefix)
	}

	return fmt.Sprintf(`"%s"`, builder.Build())
}

