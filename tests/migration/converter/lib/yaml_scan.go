package lib

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Internal regex helpers for lightweight YAML scanning.
var (
	stepsHeaderRe       = regexp.MustCompile(`^\s*steps:\s*$`)
	stepStartRe         = regexp.MustCompile(`^(\s*)-\s*([A-Za-z0-9_]+):\s*$`)
	topLevelMapLineRe   = regexp.MustCompile(`^[A-Za-z0-9_\"].*:\s*$`)
	topLevelKeyRe       = regexp.MustCompile(`^([A-Za-z0-9_\-]+):\s*$`)
	quotedTopLevelKeyRe = regexp.MustCompile(`^"([^"]+)":\s*$`)
)

func nextStepOrDedent(baseIndent int) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`^\s{0,%d}-\s+[A-Za-z0-9_]+:\s*$`, baseIndent))
}

// ReadNormalizedLinesFromFile reads a file and normalizes newlines to '\n'.
func ReadNormalizedLinesFromFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ReadNormalizedLinesFromBytes(data), nil
}

// ReadNormalizedLinesFromBytes normalizes newlines to '\n' and splits into lines.
func ReadNormalizedLinesFromBytes(data []byte) []string {
	return strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
}

// ScanAutotestOriginalBlocksFromLines extracts original 'steps' YAML blocks from lines.
func ScanAutotestOriginalBlocksFromLines(lines []string) [][]string {
	var blocks [][]string
	inSteps := false
	i := 0
	for i < len(lines) {
		line := lines[i]
		if !inSteps {
			if stepsHeaderRe.MatchString(line) {
				inSteps = true
			}
			i++
			continue
		}
		m := stepStartRe.FindStringSubmatch(line)
		if m == nil {
			// end if we see a new top-level mapping key
			if (line == strings.TrimLeft(line, " \t")) && topLevelMapLineRe.MatchString(line) && !strings.HasPrefix(strings.TrimLeft(line, " \t"), "- ") {
				break
			}
			i++
			continue
		}
		baseIndent := len(m[1])
		var block []string
		block = append(block, line)
		i++
		for i < len(lines) {
			ln := lines[i]
			if strings.TrimSpace(ln) == "" {
				block = append(block, ln)
				i++
				continue
			}
			// next step or dedent
			if nextStepOrDedent(baseIndent).MatchString(ln) || (len(ln)-len(strings.TrimLeft(ln, " \t")) <= baseIndent-1) {
				break
			}
			block = append(block, ln)
			i++
		}
		blocks = append(blocks, block)
	}
	return blocks
}

// ParseTopLevelKeysBeforeMarkerFromContent parses top-level keys appearing before a marker.
func ParseTopLevelKeysBeforeMarkerFromContent(content, marker string) []string {
	before := content
	if strings.Contains(content, marker) {
		before = strings.Split(content, marker)[0]
	}
	var keys []string
	lines := strings.Split(strings.ReplaceAll(before, "\r\n", "\n"), "\n")
	for _, raw := range lines {
		ln := raw
		if strings.TrimSpace(ln) == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(ln), "#") {
			continue
		}
		if ln != strings.TrimLeft(ln, " \t") {
			continue // not top-level
		}
		// match unquoted or quoted key ending with colon
		if m := topLevelKeyRe.FindStringSubmatch(ln); m != nil {
			keys = append(keys, m[1])
			continue
		}
		if m := quotedTopLevelKeyRe.FindStringSubmatch(ln); m != nil {
			keys = append(keys, m[1])
			continue
		}
	}
	return keys
}


