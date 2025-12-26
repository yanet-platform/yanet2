package lib

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// validateGeneratedCode validates the correctness of generated test data before writing to file.
// It checks for required fields and validates CLI command formats.
func (c *Converter) validateGeneratedCode(testData *GoTestData) error {
	c.debugLog("Validating generated test data for: %s", testData.TestName)

	// Check that all required fields are present
	if testData.TestName == "" {
		return fmt.Errorf("test name is empty")
	}

	// Check that CLI commands are in correct format
	for _, step := range testData.Steps {
		if step.Type == "cli" && strings.Contains(step.GoCode, "yanet-cli ") {
			if !strings.Contains(step.GoCode, "--cfg") {
				c.debugLog("WARNING: CLI command may be in old format in step type %s: %s", step.Type, step.GoCode)
			}
		}
	}

	return nil
}

// formatGoCode formats generated Go code using go/format
func (c *Converter) formatGoCode(code string) string {
	// Use go/format to properly format the code
	formatted, err := format.Source([]byte(code))
	if err != nil {
		// If formatting fails, log warning and return original
		c.verbose("Warning: failed to format code with go/format: %v", err)
		return code
	}
	return string(formatted)
}

// generateGoTest generates a complete Go test file from the converted test data.
// It selects the appropriate template based on test type (NAT64, balancer, route, etc.),
// formats the code with go/format, and writes it to the output directory.
func (c *Converter) generateGoTest(testData *GoTestData) error {
	c.debugLog("Generating Go test for: %s", testData.TestName)

	// Validate before generating
	if err := c.validateGeneratedCode(testData); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}
	// Collect all functions from all steps
	var allFunctions []string
	for _, step := range testData.Steps {
		allFunctions = append(allFunctions, step.Functions...)
	}

	// Choose template based on test type
	var tmpl string
	switch testData.TestType {
	case "nat64":
		tmpl = c.generateNAT64TestTemplate(testData, allFunctions)
	case "balancer":
		tmpl = c.generateBalancerTestTemplate(testData, allFunctions)
	case "route":
		tmpl = c.generateRouteTestTemplate(testData, allFunctions)
	case "acl":
		tmpl = c.generateACLTestTemplate(testData, allFunctions)
	case "decap":
		tmpl = c.generateDecapTestTemplate(testData, allFunctions)
	default:
		tmpl = c.generateGenericTestTemplate(testData, allFunctions)
	}

	t, err := template.New("gotest").Parse(tmpl)
	if err != nil {
		return fmt.Errorf("template creation error: %w", err)
	}

	// Generate code to string first for formatting
	var codeBuffer strings.Builder
	if err := t.Execute(&codeBuffer, testData); err != nil {
		return fmt.Errorf("code generation error: %w", err)
	}

	// Format the generated code
	formattedCode := c.formatGoCode(codeBuffer.String())

	// Create output directory
	c.debugLog("Creating output directory: %s", c.config.OutputDir)
	if err := os.MkdirAll(c.config.OutputDir, 0755); err != nil {
		return fmt.Errorf("output directory creation error: %w", err)
	}

	// Create test file
	// Use OriginalTestName to preserve the original format (e.g., "001_nat64stateless")
	// instead of sanitized TestName which may have "Test_" prefix
	outputFile := filepath.Join(c.config.OutputDir, fmt.Sprintf("%s_test.go", strings.ToLower(testData.OriginalTestName)))
	c.debugLog("Creating test file: %s", outputFile)
	file, err := os.Create(outputFile)
	if err != nil {
		return fmt.Errorf("file creation error %s: %w", outputFile, err)
	}
	defer file.Close()

	// Write formatted code to file
	c.debugLog("Writing %d bytes to file", len(formattedCode))
	if _, err := file.WriteString(formattedCode); err != nil {
		return fmt.Errorf("error writing formatted code to file: %w", err)
	}

	c.debugLog("Successfully generated test file: %s", outputFile)
	c.verbose("Generated test: %s", outputFile)

	return nil
}

// generateTestStepsInOrder generates all test steps in the order they appear in autotest.yaml
func (c *Converter) generateTestStepsInOrder(steps []ConvertedStep) string {
	var result strings.Builder
	routeCounter := 0
	packetCounter := 0
	isFirstPacketTest := true

	for _, step := range steps {
		// Skip empty steps (skipped due to skiplist rules)
		if step.Type == "" {
			continue
		}

		// Handle route configuration steps
		if step.Type == "ipv4Update" || step.Type == "ipv6Update" || step.Type == "ipv4LabelledUpdate" || step.Type == "ipv4Remove" || step.Type == "ipv4LabelledRemove" {
			routeCounter++
			result.WriteString(fmt.Sprintf(`
	fw.Run("Step_%03d_Configure_Routes", func(fw *framework.F, t *testing.T) {
		// %s
		%s
	})`, routeCounter, step.Description, step.GoCode))
		} else if step.Type == "sendPackets" {
			// Add delay before the first packet test (after all configuration steps)
			if isFirstPacketTest {
				result.WriteString(`

	// Wait 3 seconds for configuration changes to take effect (pipeline updates are asynchronous)
	time.Sleep(3 * time.Second)
`)
				isFirstPacketTest = false
			}

			// Generate test cases for each packet group (per PCAP entry)
			for _, testCase := range step.PacketTests {
				packetCounter++
				// Build the test code using strings.Builder

				// Note: Error handling removed - new socket-based code handles drops gracefully

				result.WriteString(fmt.Sprintf(`
	fw.Run("Step_%03d_Test_Packet", func(fw *framework.F, t *testing.T) {
		// Test case: %s -> %s
		sendPackets := %s
		require.NotNil(t, sendPackets)
		require.NotEqual(t, 0, len(sendPackets), "Expected at least one packet to send")

		%s

		// Get socket client
		client, err := fw.GetSocketClient(0)
		require.NoError(t, err, "Failed to get socket client")
		defer client.Close()
		require.NoError(t, client.Connect(), "Failed to connect to socket")

		var receivedPackets []gopacket.Packet
		for idx, pkt := range sendPackets {
			t.Logf("Sending packet %%d of %%d from `+testCase.SendPcap+`", idx+1, len(sendPackets))
			packetBytes := pkt.Data()

			// Send packet
			require.NoError(t, client.SendPacket(packetBytes), "Failed to send packet %%d", idx)

			// Receive packet (ignore errors - packet may be dropped)
			responseData, _ := client.ReceivePacket(100 * time.Millisecond)
			if responseData != nil {
				receivedPkt := gopacket.NewPacket(responseData, layers.LayerTypeEthernet, gopacket.Default)
				receivedPackets = append(receivedPackets, receivedPkt)
			}

			// Small delay to prevent socket buffer overflow when sending many packets rapidly
			// This gives the dataplane time to process packets before the socket buffer fills up
			if idx < len(sendPackets)-1 {
				time.Sleep(1 * time.Millisecond)
			}
		}

`,
					packetCounter, testCase.SendPcap, testCase.ExpectPcap,
					c.generatePacketFunctionCall(testCase.FunctionName),
					c.generateExpectedPacketSetup(&testCase),
				))

				// Add packet validation after all packets are sent and received
				if !testCase.IsDropExpected {
					result.WriteString(c.generateBatchPacketValidation(&testCase))
				}
				result.WriteString(`
	})`)
			}
			continue
		} else {
			// Handle other step types (cli, checkCounters, etc.)
			result.WriteString(fmt.Sprintf(`
	fw.Run("Step_%s", func(fw *framework.F, t *testing.T) {
		// %s
		%s
	})`, step.Type, step.Description, step.GoCode))
		}
	}
	return result.String()
}

// generatePacketFunctions generates functions for packet creation
func (c *Converter) generatePacketFunctions(testData *GoTestData) string {
	var result strings.Builder

	// Generate functions only from steps (they are already created in convertSendPackets with correct numbers)
	for _, step := range testData.Steps {
		for _, fn := range step.Functions {
			result.WriteString(fn + "\n\n")
		}
	}

	return result.String()
}

// generateTestHeader creates unified header for all test types
func (c *Converter) generateTestHeader(testName, originalTestName, testType string) string {
	// Always include full imports for packet testing
	imports := `import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/tests/migration/converter/lib"
)`

	silenceCode := `
	// Silence potentially unused imports PCAP vs AST parser
	_ = cmp.Diff
	_ = lib.CmpStdOpts
	_ = lib.NewPacket
	_ = net.ParseIP
	_ = strings.Join`

	return fmt.Sprintf(`package converted

%s

// Test%s - automatically generated test from yanet1
// Original test: %s
// Test type: %s
func Test%s(t *testing.T) {
	fw := globalFramework.ForTest(t)
	require.NotNil(t, fw, "Global framework should be initialized")%s
`, imports, testName, originalTestName, testType, testName, silenceCode)
}

// generateNAT64TestTemplate generates template for NAT64 tests
func (c *Converter) generateNAT64TestTemplate(testData *GoTestData, functions []string) string {
	header := c.generateTestHeader(testData.TestName, testData.OriginalTestName, testData.TestType)

	// Generate NAT64 configuration commands from parsed config
	var nat64Commands []string
	var nat64ModuleName string
	if testData.ParsedConfig != nil {
		for moduleName, module := range testData.ParsedConfig.Modules {
			if module.Type == "nat64stateless" || module.Type == "nat64stateful" {
				nat64ModuleName = moduleName
				// Collect unique prefixes from translations
				prefixMap := make(map[string]int) // prefix -> index
				var prefixes []string

				for _, trans := range module.Translations {
					if trans.IPv6DestinationAddress != "" {
						// Normalize prefix: ensure it ends with /96 if no mask specified
						prefix := trans.IPv6DestinationAddress
						if !strings.Contains(prefix, "/") {
							prefix = prefix + "/96"
						}
						if _, exists := prefixMap[prefix]; !exists {
							prefixMap[prefix] = len(prefixes)
							prefixes = append(prefixes, prefix)
						}
					}
				}

				// Generate prefix add commands
				for _, prefix := range prefixes {
					nat64Commands = append(nat64Commands,
						fmt.Sprintf(`"%s prefix add --cfg %s --instances 0 --prefix %s"`, framework.CLINAT64, moduleName, prefix))
				}

				// Generate mapping add commands
				for _, trans := range module.Translations {
					prefix := trans.IPv6DestinationAddress
					if !strings.Contains(prefix, "/") {
						prefix = prefix + "/96"
					}
					prefixIndex := prefixMap[prefix]
					nat64Commands = append(nat64Commands,
						fmt.Sprintf(`"%s mapping add --cfg %s --instances 0 --ipv4 %s --ipv6 %s --prefix-index %d"`,
							framework.CLINAT64, moduleName, trans.IPv4Address, trans.IPv6Address, prefixIndex))
				}
				break // Use the first NAT64 module found
			}
		}
	}

	// Fallback to default name if no NAT64 module found
	if nat64ModuleName == "" {
		nat64ModuleName = "nat64_0"
	}

	var nat64Cmds string
	if len(nat64Commands) > 0 {
		nat64Cmds = "\n\t\t\t" + strings.Join(nat64Commands, ",\n\t\t\t") + ",\n"
	}

	return fmt.Sprintf(`%s
	fw.Run("Step_000_Configure_NAT64_Environment", func(fw *framework.F, t *testing.T) {
		// Configure NAT64 module
		commands := []string{%s%s
			"%s update --name=test --chains chain2:1=forward:forward0,nat64:%s,route:route0 --instance=0",
			"%s update --name=test --functions test --instance=0",
		}
		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure NAT64 module")

	})

%s
}

%s
`, header,
		c.generateForwardModuleConfig(testData.ParsedConfig),
		nat64Cmds,
		framework.CLIFunction,
		nat64ModuleName,
		framework.CLIPipeline,
		c.generateTestStepsInOrder(testData.Steps),
		c.generatePacketFunctions(testData))
}

// generateForwardModuleConfig generates forward module configuration
func (c *Converter) generateForwardModuleConfig(config *ControlplaneConfig) string {
	commands := c.generateForwardModuleCommands(config)
	if len(commands) == 0 {
		return ""
	}

	var result strings.Builder
	for _, cmd := range commands {
		result.WriteString(fmt.Sprintf("\t\t\t\"%s\",\n", cmd))
	}
	return result.String()
}

// generateBalancerTestTemplate generates template for balancer tests
func (c *Converter) generateBalancerTestTemplate(testData *GoTestData, functions []string) string {
	header := c.generateTestHeader(testData.TestName, testData.OriginalTestName, testData.TestType)
	return fmt.Sprintf(`%s
	fw.Run("Step_000_Configure_Balancer_Environment", func(fw *framework.F, t *testing.T) {
		// Configure balancer module
		commands := []string{
			"%s service add --cfg balancer0 --instances 0 --virtual-ip 10.0.0.16 --proto tcp --virtual-port any",
			"%s update --name=test --chains chain2:1=balancer:balancer0,route:route0 --instance=0",
			"%s update --name=test --functions test --instance=0",
		}
		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure balancer module")

	})

%s
}

%s
`, header,
		framework.CLIBalancer,
		framework.CLIFunction,
		framework.CLIPipeline,
		c.generateTestStepsInOrder(testData.Steps),
		c.generatePacketFunctions(testData))
}

// generateRouteTestTemplate generates template for route tests
func (c *Converter) generateRouteTestTemplate(testData *GoTestData, functions []string) string {
	header := c.generateTestHeader(testData.TestName, testData.OriginalTestName, testData.TestType)
	return fmt.Sprintf(`%s
%s
}

%s
`, header,
		c.generateTestStepsInOrder(testData.Steps),
		c.generatePacketFunctions(testData))
}

// generateACLTestTemplate generates template for ACL tests
func (c *Converter) generateACLTestTemplate(testData *GoTestData, functions []string) string {
	header := c.generateTestHeader(testData.TestName, testData.OriginalTestName, testData.TestType)
	return fmt.Sprintf(`%s
	fw.Run("Step_000_Configure_ACL_Environment", func(fw *framework.F, t *testing.T) {
		// Configure ACL module
		commands := []string{
			"%s update --name=test --chains chain2:1=acl:acl0,route:route0 --instance=0",
			"%s update --name=test --functions test --instance=0",
		}
		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure ACL module")

	})

%s
}

%s
`, header,
		framework.CLIFunction,
		framework.CLIPipeline,
		c.generateTestStepsInOrder(testData.Steps),
		c.generatePacketFunctions(testData))
}

// generateDecapTestTemplate generates template for Decap tests
func (c *Converter) generateDecapTestTemplate(testData *GoTestData, functions []string) string {
	header := c.generateTestHeader(testData.TestName, testData.OriginalTestName, testData.TestType)

	// Generate decap configuration commands from parsed config
	var decapCommands []string
	if testData.ParsedConfig != nil {
		for moduleName, module := range testData.ParsedConfig.Modules {
			if module.Type == "decap" {
				// Add IPv4 destination prefixes
				for _, prefix := range module.IPv4DestinationPrefixes {
					decapCommands = append(decapCommands,
						fmt.Sprintf(`"%s prefix-add --cfg %s --instances 0 -p %s"`, framework.CLIDecap, moduleName, prefix))
				}
				// Add IPv6 destination prefixes
				for _, prefix := range module.IPv6DestinationPrefixes {
					decapCommands = append(decapCommands,
						fmt.Sprintf(`"%s prefix-add --cfg %s --instances 0 -p %s"`, framework.CLIDecap, moduleName, prefix))
				}
			}
		}
	}

	var decapCmds string
	if len(decapCommands) > 0 {
		decapCmds = "\n\t\t\t" + strings.Join(decapCommands, ",\n\t\t\t") + ",\n"
	}

	return fmt.Sprintf(`%s
	fw.Run("Step_000_Configure_Decap_Environment", func(fw *framework.F, t *testing.T) {
		// Configure Decap module
		commands := []string{%s
			"%s update --name=test --chains chain2:1=forward:forward0,decap:decap0,route:route0 --instance=0",
			"%s update --name=test --functions test --instance=0",
		}
		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure Decap module")
	})

%s
}

%s
`, header,
		decapCmds,
		framework.CLIFunction,
		framework.CLIPipeline,
		c.generateTestStepsInOrder(testData.Steps),
		c.generatePacketFunctions(testData))
}

// generateGenericTestTemplate generates generic template for tests
func (c *Converter) generateGenericTestTemplate(testData *GoTestData, functions []string) string {
	header := c.generateTestHeader(testData.TestName, testData.OriginalTestName, testData.TestType)
	return fmt.Sprintf(`%s
	fw.Run("Step_000_Configure_Test_Environment", func(fw *framework.F, t *testing.T) {
		// Configure test environment with forward (required for packet processing)
		commands := []string{
			"%s update --name=test --chains chain2:1=forward:forward0,route:route0 --instance=0",
			"%s update --name=test --functions test --instance=0",
		}
		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure test environment")
	})

%s
}

%s
`, header,
		framework.CLIFunction,
		framework.CLIPipeline,
		c.generateTestStepsInOrder(testData.Steps),
		c.generatePacketFunctions(testData))
}

func (c *Converter) generatePacketFunctionCall(functionName string) string {
	if functionName == "" {
		return "nil"
	}
	return functionName + "(t)"
}

func (c *Converter) generateExpectedPacketSetup(testCase *PacketTestCase) string {
	// Check if this is a drop test or no expect function was generated
	if testCase.IsDropExpected || testCase.ExpectFunctionName == "" {
		return "// No expected packets needed (drop test or empty expect file)"
	}
	// Generate expected packets without checking count (fragmentation may change packet count)
	return fmt.Sprintf(`expectedPackets := %s(t)
	require.NotNil(t, expectedPackets)`, testCase.ExpectFunctionName)
}

func (c *Converter) generateBatchPacketValidation(testCase *PacketTestCase) string {
	// If no expect function, just check we received packets
	if testCase.ExpectFunctionName == "" {
		return `
		require.NotEmpty(t, receivedPackets, "Should have received at least one packet")`
	}

	// Generate batch validation comparing all received packets with expected
	return `
		// Validate all received packets against expected packets
		t.Logf("Received %d packets, expected %d packets", len(receivedPackets), len(expectedPackets))

		require.Equalf(t, len(expectedPackets), len(receivedPackets),
			"Packet count mismatch: expected %d, received %d", len(expectedPackets), len(receivedPackets))

		for idx, expectedPkt := range expectedPackets {
			actualPkt := receivedPackets[idx]

			diff := cmp.Diff(expectedPkt.Layers(), actualPkt.Layers(), lib.CmpStdOpts...)
			require.Emptyf(t, diff, "Packet layers mismatch for index %d", idx)
		}`
}
