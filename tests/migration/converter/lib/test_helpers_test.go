package lib

import (
	"encoding/hex"
	"fmt"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/require"
)

// requireGoCodeParses fails the test unless code parses as valid Go
// statements. It wraps code in a minimal function body, since a
// ConvertedStep.GoCode fragment is only ever embedded inside a generated
// test's function body and is never a complete source file on its own.
func requireGoCodeParses(t *testing.T, code string) {
	t.Helper()

	src := "package generated\n\nfunc f() {\n" + code + "\n}\n"
	_, err := parser.ParseFile(token.NewFileSet(), "generated.go", src, 0)
	require.NoError(t, err, "generated code must be valid Go:\n%s", code)
}

// asInt converts common numeric JSON types to int.
func asInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int32:
		return int(t), true
	case int64:
		return int(t), true
	case uint:
		return int(t), true
	case uint32:
		return int(t), true
	case uint64:
		return int(t), true
	case float32:
		return int(t), true
	case float64:
		return int(t), true
	case string:
		var i int
		_, err := fmt.Sscanf(t, "%d", &i)
		if err == nil {
			return i, true
		}
		return 0, false
	default:
		return 0, false
	}
}

// printPacketDiff prints a hex dump diff for debugging mismatches.
func printPacketDiff(t *testing.T, expected, actual []byte, index int) {
	t.Logf("\n=== Packet %d Comparison ===", index)
	t.Logf("Expected (%d bytes):\n%s", len(expected), hex.Dump(expected))
	t.Logf("Actual (%d bytes):\n%s", len(actual), hex.Dump(actual))

	// Find first difference
	minLen := min(len(actual), len(expected))
	for i := range minLen {
		if expected[i] != actual[i] {
			t.Logf("First difference at byte %d: expected 0x%02x, got 0x%02x",
				i, expected[i], actual[i])
			break
		}
	}
	if len(expected) != len(actual) {
		t.Logf("Length difference: expected %d, got %d", len(expected), len(actual))
	}
}

// generatePacketFromIR builds a packet from IR using the packet builder and returns bytes.
func generatePacketFromIR(irPkt IRPacketDef, opts CodegenOpts) ([]byte, error) {
	var layerBuilders []LayerBuilder
	for _, layer := range irPkt.Layers {
		builder := buildLayerFromIR(layer, opts.IsExpect)
		if builder != nil {
			layerBuilders = append(layerBuilders, builder)
		}
	}
	pkt, err := NewPacket(nil, layerBuilders...)
	if err != nil {
		return nil, err
	}
	return pkt.Data(), nil
}
