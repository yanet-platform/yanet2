package internal

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// ScapyPacketDef represents a Scapy packet definition from gen.py
type ScapyPacketDef struct {
	Layers       []ScapyLayer
	RawCode      string
	IsFragmented bool
	RequiresRaw  bool
}

// ScapyLayer represents a single layer in Scapy packet
type ScapyLayer struct {
	Name   string            // e.g., "Ether", "IP", "IPv6", "TCP"
	Params map[string]string // e.g., {"dst": "00:11:22:33:44:55", "src": "00:00:00:00:00:01"}
}

// ScapyPcapPair represents a send/expect PCAP pair from gen.py
type ScapyPcapPair struct {
	SendFile      string
	ExpectFile    string
	SendPackets   []ScapyPacketDef
	ExpectPackets []ScapyPacketDef
}

// AutotestYAML represents the structure of autotest.yaml
type AutotestYAML struct {
	Steps []Step `yaml:"steps"`
}

type Step struct {
	SendPackets *[]SendPacketStep `yaml:"sendPackets,omitempty"`
}

type SendPacketStep struct {
	Port   string `yaml:"port"`
	Send   string `yaml:"send"`
	Expect string `yaml:"expect"`
}

// ScapyParser parses gen.py files to extract Scapy packet definitions
type ScapyParser struct {
	verbose bool
}

// HelperFunctionCall represents a call to a helper function
type HelperFunctionCall struct {
	FuncName string
	Filename string
}

// NewScapyParser creates a new Scapy parser
func NewScapyParser(verbose bool) *ScapyParser {
	return &ScapyParser{
		verbose: verbose,
	}
}

// ParseGenPy parses gen.py file and extracts all PCAP pairs with their packet definitions
func (sp *ScapyParser) ParseGenPy(genPyPath string) ([]ScapyPcapPair, error) {
	content, err := os.ReadFile(genPyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read gen.py: %w", err)
	}

	fileContent := string(content)

	// First, extract helper function definitions
	helperFuncs := sp.extractHelperFunctions(fileContent)

	// Then parse direct write_pcap calls and helper function calls
	var pairs []ScapyPcapPair

	// Parse direct write_pcap calls
	directCalls := sp.extractDirectWritePcapCalls(fileContent)
	for filename, packets := range directCalls {
		sp.addToPairs(&pairs, filename, packets)
	}

	// Parse helper function calls (e.g., write_pcap_step1("001-send.pcap"))
	helperCalls := sp.extractHelperFunctionCalls(fileContent)
	if sp.verbose {
		fmt.Printf("Found %d helper functions: %v\n", len(helperFuncs), getKeys(helperFuncs))
		fmt.Printf("Found %d helper calls\n", len(helperCalls))
	}
	for _, call := range helperCalls {
		if packets, exists := helperFuncs[call.FuncName]; exists {
			sp.addToPairs(&pairs, call.Filename, packets)
			if sp.verbose {
				fmt.Printf("Matched %s -> %s (%d packets)\n", call.FuncName, call.Filename, len(packets))
			}
		} else if sp.verbose {
			fmt.Printf("No match for %s (wanted %s)\n", call.FuncName, call.Filename)
		}
	}

	return pairs, nil
}

// addToPairs adds packets to the appropriate pair (send or expect)
func (sp *ScapyParser) addToPairs(pairs *[]ScapyPcapPair, filename string, packets []ScapyPacketDef) {
	if !strings.HasSuffix(filename, ".pcap") {
		return // Only process .pcap files
	}

	// Determine if this is send or expect based on filename
	isSend := strings.Contains(filename, "send") || (!strings.Contains(filename, "expect") && filename != "expect.pcap")
	isExpect := strings.Contains(filename, "expect") || filename == "expect.pcap"

	// For files with explicit -send/-expect suffixes
	if strings.Contains(filename, "-send.pcap") {
		isSend = true
		isExpect = false
	} else if strings.Contains(filename, "-expect.pcap") {
		isSend = false
		isExpect = true
	}

	// Extract base name for pairing
	baseName := ""
	if strings.Contains(filename, "-send.pcap") {
		baseName = strings.TrimSuffix(filename, "-send.pcap")
	} else if strings.Contains(filename, "-expect.pcap") {
		baseName = strings.TrimSuffix(filename, "-expect.pcap")
	} else {
		// For files without standard suffixes, try to find a common base
		// e.g., "decap.pcap" and "decap_expect.pcap" should pair as "decap"
		nameWithoutExt := strings.TrimSuffix(filename, ".pcap")
		if strings.HasSuffix(nameWithoutExt, "_expect") {
			baseName = strings.TrimSuffix(nameWithoutExt, "_expect")
		} else if strings.HasSuffix(nameWithoutExt, "_send") {
			baseName = strings.TrimSuffix(nameWithoutExt, "_send")
		} else {
			// For single files like "send.pcap" and "expect.pcap"
			if filename == "send.pcap" {
				baseName = "send"
			} else if filename == "expect.pcap" {
				baseName = "expect"
			} else {
				baseName = nameWithoutExt
			}
		}
	}

	// Find existing pair
	var pair *ScapyPcapPair
	for i := range *pairs {
		p := &(*pairs)[i]

		// Extract base name from existing pair files for comparison
		var existingBaseName string
		var fileToCheck string
		if p.SendFile != "" {
			fileToCheck = p.SendFile
		} else if p.ExpectFile != "" {
			fileToCheck = p.ExpectFile
		}

		if fileToCheck != "" {
			nameWithoutExt := strings.TrimSuffix(fileToCheck, ".pcap")
			if strings.HasSuffix(nameWithoutExt, "_expect") {
				existingBaseName = strings.TrimSuffix(nameWithoutExt, "_expect")
			} else if strings.HasSuffix(nameWithoutExt, "_send") {
				existingBaseName = strings.TrimSuffix(nameWithoutExt, "_send")
			} else if strings.Contains(fileToCheck, "-send.pcap") {
				existingBaseName = strings.TrimSuffix(fileToCheck, "-send.pcap")
			} else if strings.Contains(fileToCheck, "-expect.pcap") {
				existingBaseName = strings.TrimSuffix(fileToCheck, "-expect.pcap")
			} else {
				// For single files like "send.pcap" and "expect.pcap"
				if fileToCheck == "send.pcap" {
					existingBaseName = "send"
				} else if fileToCheck == "expect.pcap" {
					existingBaseName = "expect"
				} else {
					existingBaseName = nameWithoutExt
				}
			}
		}

		if existingBaseName == baseName {
			pair = p
			break
		}

		// Special cases for single send.pcap/expect.pcap files
		if (baseName == "send" && p.SendFile == "send.pcap") ||
			(baseName == "expect" && p.ExpectFile == "expect.pcap") {
			pair = p
			break
		}
	}

	// For single send/expect.pcap files, create a pair that links them
	if pair == nil && (filename == "send.pcap" || filename == "expect.pcap") {
		// Look for the complementary file
		complement := "expect.pcap"
		if filename == "expect.pcap" {
			complement = "send.pcap"
		}

		for i := range *pairs {
			p := &(*pairs)[i]
			if p.SendFile == complement || p.ExpectFile == complement {
				pair = p
				break
			}
		}
	}

	if pair == nil {
		*pairs = append(*pairs, ScapyPcapPair{})
		pair = &(*pairs)[len(*pairs)-1]
	}

	if isSend || filename == "send.pcap" {
		pair.SendFile = filename
		pair.SendPackets = packets
	} else if isExpect || filename == "expect.pcap" {
		pair.ExpectFile = filename
		pair.ExpectPackets = packets
	}
}

// extractHelperFunctions extracts helper function definitions (e.g., write_pcap_step1)
func (sp *ScapyParser) extractHelperFunctions(content string) map[string][]ScapyPacketDef {
	helperFuncs := make(map[string][]ScapyPacketDef)

	// Regex to match function definitions like: def write_pcap_stepN(filename):
	funcDefRegex := regexp.MustCompile(`def\s+(write_pcap_\w+)\s*\([^)]*\)\s*:`)
	matches := funcDefRegex.FindAllStringSubmatchIndex(content, -1)

	for _, match := range matches {
		funcName := content[match[2]:match[3]]

		// Find the end of this function by looking for the next line with module-level indentation (0 spaces)
		funcStart := match[1]
		funcEnd := len(content)

		lines := strings.Split(content[funcStart:], "\n")
		currentPos := funcStart

		for _, line := range lines {
			lineStart := currentPos
			currentPos += len(line) + 1 // +1 for \n

			// Skip empty lines
			if strings.TrimSpace(line) == "" {
				continue
			}

			// If line starts with non-whitespace (module level), this is the end of function
			if len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
				// But skip if it's a comment
				if !strings.HasPrefix(strings.TrimSpace(line), "#") {
					funcEnd = lineStart
					break
				}
			}
		}

		funcBody := content[funcStart:funcEnd]

		// Extract write_pcap call inside this function
		writePcapCalls := sp.extractWritePcapFromString(funcBody)
		if sp.verbose {
			fmt.Printf("Helper function %s has %d write_pcap calls\n", funcName, len(writePcapCalls))
			for filename, packets := range writePcapCalls {
				fmt.Printf("  %s: %d packets\n", filename, len(packets))
			}
		}
		if len(writePcapCalls) > 0 {
			// Take the first write_pcap call with packets (skip empty ones)
			for _, packets := range writePcapCalls {
				if len(packets) > 0 {
					helperFuncs[funcName] = packets
					break
				}
			}
		}
	}

	return helperFuncs
}

// extractDirectWritePcapCalls extracts direct write_pcap calls with filenames
func (sp *ScapyParser) extractDirectWritePcapCalls(content string) map[string][]ScapyPacketDef {
	calls := make(map[string][]ScapyPacketDef)

	// Find write_pcap("filename", at module level (not indented)
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip indented lines (inside functions)
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			continue
		}

		if strings.HasPrefix(trimmed, "write_pcap(") {
			// Extract filename
			filenameRegex := regexp.MustCompile(`write_pcap\("([^"]+)"`)
			if matches := filenameRegex.FindStringSubmatch(trimmed); len(matches) > 1 {
				filename := matches[1]

				// Find the full call content by concatenating lines until we find the closing )
				var callContent strings.Builder
				callContent.WriteString(line)

				// Find start position in original content
				startPos := strings.Index(content, line)
				if startPos == -1 {
					continue
				}

				// Find matching closing parenthesis from start of write_pcap(
				openParenPos := strings.Index(content[startPos:], "write_pcap(")
				if openParenPos == -1 {
					continue
				}
				openParenPos += startPos

				closeParenPos := sp.findMatchingParen(content, openParenPos)
				if closeParenPos == -1 {
					continue
				}

				// Extract full call content
				fullCall := content[openParenPos : closeParenPos+1]

				// Extract content between filename and closing paren
				afterFilename := strings.Index(fullCall, `",`)
				var callArgs string
				if afterFilename != -1 {
					afterFilename += 2                                   // skip ",
					callArgs = fullCall[afterFilename : len(fullCall)-1] // remove closing )
				} else {
					// No arguments after filename
					callArgs = ""
				}

				if sp.verbose {
					fmt.Printf("Found direct write_pcap call: %s with content length %d\n", filename, len(callArgs))
				}

				packets := sp.parseWritePcapContent(callArgs)
				calls[filename] = packets
			}
		}
	}

	return calls
}

// extractHelperFunctionCalls extracts calls to helper functions (e.g., write_pcap_step1("001-send.pcap"))
func (sp *ScapyParser) extractHelperFunctionCalls(content string) []HelperFunctionCall {
	var calls []HelperFunctionCall

	// Split by lines to find module-level calls
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip lines inside function definitions (they start with whitespace)
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			continue
		}

		// Skip comment lines
		if strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Match calls like: write_pcap_stepN("filename.pcap")
		callRegex := regexp.MustCompile(`^(write_pcap_\w+)\("([^"]+)"\)`)
		if matches := callRegex.FindStringSubmatch(trimmed); len(matches) >= 3 {
			funcName := matches[1]
			filename := matches[2]
			calls = append(calls, HelperFunctionCall{
				FuncName: funcName,
				Filename: filename,
			})
		}
	}

	return calls
}

// extractWritePcapFromString extracts write_pcap content from a string
func (sp *ScapyParser) extractWritePcapFromString(content string) map[string][]ScapyPacketDef {
	calls := make(map[string][]ScapyPacketDef)

	// Find write_pcap calls (with or without parameters)
	writePcapRegex := regexp.MustCompile(`write_pcap\(([^)]+)\)`)
	matches := writePcapRegex.FindAllStringSubmatchIndex(content, -1)

	for _, match := range matches {
		// Extract the full call
		callStart := match[0]
		callEnd := sp.findMatchingParen(content, callStart)
		if callEnd == -1 {
			continue
		}

		callContent := content[callStart : callEnd+1]

		// Extract just the packet arguments (after filename)
		afterFilename := strings.Index(callContent, ",")
		var packetArgs string
		if afterFilename != -1 {
			afterFilename++                                              // skip comma
			packetArgs = callContent[afterFilename : len(callContent)-1] // remove closing )
		} else {
			// No parameters, empty packet list
			packetArgs = ""
		}

		packets := sp.parseWritePcapContent(packetArgs)

		// Use a dummy filename for helper functions
		calls["helper"] = packets
	}

	return calls
}

// findMatchingParen finds the matching closing parenthesis
func (sp *ScapyParser) findMatchingParen(content string, start int) int {
	depth := 0
	for i := start; i < len(content); i++ {
		if content[i] == '(' {
			depth++
		} else if content[i] == ')' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// parseWritePcapContent parses the content of a write_pcap call
func (sp *ScapyParser) parseWritePcapContent(content string) []ScapyPacketDef {
	var packets []ScapyPacketDef

	// Check if this uses fragment6() helper
	if strings.Contains(content, "fragment6(") {
		// Handle fragmented packets
		packets = append(packets, ScapyPacketDef{
			RawCode:      content,
			IsFragmented: true,
		})
		return packets
	}

	// Parse packet definitions by finding balanced parentheses and commas
	// This handles multi-line packets correctly
	packetStrings := sp.splitPacketDefinitions(content)
	for _, packetStr := range packetStrings {
		packetStr = strings.TrimSpace(packetStr)
		if packetStr == "" {
			continue
		}

		packet := sp.parsePacketDef(packetStr)
		if len(packet.Layers) > 0 {
			packets = append(packets, packet)
		}
	}

	return packets
}

// splitPacketDefinitions splits write_pcap content into individual packet definitions
func (sp *ScapyParser) splitPacketDefinitions(content string) []string {
	var packets []string
	var current strings.Builder
	depth := 0
	inString := false
	stringChar := byte(0)

	if sp.verbose {
		fmt.Printf("Splitting content (length %d): %s\n", len(content), content[:min(100, len(content))])
	}

	for i := 0; i < len(content); i++ {
		ch := content[i]

		// Handle string literals
		if !inString && (ch == '"' || ch == '\'') {
			inString = true
			stringChar = ch
		} else if inString && ch == stringChar && (i == 0 || content[i-1] != '\\') {
			inString = false
			stringChar = 0
		}

		// Track parentheses depth
		if !inString {
			if ch == '(' {
				depth++
			} else if ch == ')' {
				depth--
			} else if ch == ',' && depth == 0 {
				// Comma at top level - packet separator
				packetStr := strings.TrimSpace(current.String())
				if packetStr != "" {
					packets = append(packets, packetStr)
					if sp.verbose {
						fmt.Printf("Found packet: %s\n", packetStr[:min(50, len(packetStr))])
					}
				}
				current.Reset()
				continue
			}
		}

		current.WriteByte(ch)
	}

	// Add the last packet
	packetStr := strings.TrimSpace(current.String())
	if packetStr != "" {
		packets = append(packets, packetStr)
		if sp.verbose {
			fmt.Printf("Found last packet: %s\n", packetStr[:min(50, len(packetStr))])
		}
	}

	if sp.verbose {
		fmt.Printf("Split into %d packets\n", len(packets))
	}

	return packets
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// parsePacketDef parses a single Scapy packet definition
func (sp *ScapyParser) parsePacketDef(code string) ScapyPacketDef {
	packet := ScapyPacketDef{
		RawCode: code,
	}

	// Extract layers using regex
	// Match patterns like: LayerName(param1=value1, param2=value2)
	layerRegex := regexp.MustCompile(`(\w+)\(([^)]*)\)`)
	matches := layerRegex.FindAllStringSubmatch(code, -1)

	for _, match := range matches {
		if len(match) < 3 {
			continue
		}

		layerName := match[1]
		paramsStr := match[2]

		// Skip non-layer names
		if layerName == "write_pcap" || layerName == "fragment6" {
			continue
		}

		layer := ScapyLayer{
			Name:   layerName,
			Params: sp.parseLayerParams(paramsStr),
		}

		packet.Layers = append(packet.Layers, layer)

		// Mark packets requiring raw handling
		if layerName == "IPv6ExtHdrDestOpt" || layerName == "IPv6ExtHdrRouting" ||
			layerName == "IPv6ExtHdrFragment" {
			packet.RequiresRaw = true
		}
		if _, ok := layer.Params["plen"]; ok {
			packet.RequiresRaw = true // IPv6 payload length override
		}
		if _, ok := layer.Params["nh"]; ok {
			if val := layer.Params["nh"]; val == "0x1B" || val == "27" {
				packet.RequiresRaw = true // RUDP protocol
			}
		}
		// Mark packets with payload as raw to avoid padding issues
		if layerName == "Raw" || layerName == "Padding" {
			packet.RequiresRaw = true
		}
		// Mark ICMP packets with payload as raw
		if layerName == "ICMP" && len(packet.Layers) > 1 {
			// Check if there's a payload after ICMP
			for i := len(packet.Layers) - 1; i >= 0; i-- {
				if packet.Layers[i].Name == "Raw" {
					packet.RequiresRaw = true
					break
				}
			}
		}
	}

	// Mark packets with range expressions as requiring raw handling
	if sp.hasRangeExpressions(code) {
		packet.RequiresRaw = true
		if sp.verbose {
			fmt.Printf("Packet requires raw handling due to range expressions: %s\n", code[:min(50, len(code))])
		}
	}

	// For now, mark all packets as requiring raw handling to avoid builder issues
	// TODO: Fix builder to handle all packet types correctly
	packet.RequiresRaw = true

	return packet
}

// parseLayerParams parses layer parameters from string like "dst='...', src='...'"
func (sp *ScapyParser) parseLayerParams(paramsStr string) map[string]string {
	params := make(map[string]string)

	// Split by comma, but be careful with nested structures
	parts := strings.Split(paramsStr, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len(part) == 0 {
			continue
		}

		// Split by = to get key and value
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}

		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])

		// Remove quotes if present
		value = strings.Trim(value, `"'`)

		params[key] = value
	}

	return params
}

// hasRangeExpressions checks if the packet definition contains range expressions like (1,65535)
func (sp *ScapyParser) hasRangeExpressions(packetStr string) bool {
	// Look for patterns like (number,number) in the packet string
	// This is a simple heuristic - more complex parsing would be needed for full accuracy
	return strings.Contains(packetStr, "(") && strings.Contains(packetStr, ")") &&
		(strings.Contains(packetStr, ",") || strings.Contains(packetStr, ".."))
}

// HasGenPy checks if gen.py exists in the test directory
func HasGenPy(testPath string) bool {
	genPyPath := testPath + "/gen.py"
	_, err := os.Stat(genPyPath)
	return err == nil
}

// getKeys returns keys from a map
func getKeys(m map[string][]ScapyPacketDef) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
