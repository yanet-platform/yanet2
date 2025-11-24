package lib

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConvertCheckCounters_GeneratesValidation(t *testing.T) {
	converter, err := NewConverter(&Config{})
	require.NoError(t, err)

	// Sample counter check content from YAML - needs to be map[interface{}]interface{}
	content := map[interface{}]interface{}{
		"counter1": 100,
		"counter2": 95,
	}

	result := converter.convertCheckCounters(content)

	// Should generate actual validation code with fw.ValidateCounter
	require.Contains(t, result.GoCode, "fw.ValidateCounter", "Expected ValidateCounter function call")
	require.Contains(t, result.GoCode, `"counter1", 100`, "Expected counter1 validation")
	require.Contains(t, result.GoCode, `"counter2", 95`, "Expected counter2 validation")
	require.Equal(t, "checkCounters", result.Type, "Expected step type to be checkCounters")

	// Generated code should aggregate errors using fully formatted strings, not format verbs
	require.Contains(t, result.GoCode, "var counterErrors []string")
	require.NotContains(t, result.GoCode, "%%v\", err", "Should not append format verbs with err separately")
	require.Contains(t, result.GoCode, "counterErrors = append(counterErrors, \"Counter counter1:100: \"+err.Error())")
}

func TestConvertCheckCounters_HandlesInvalidContent(t *testing.T) {
	converter, err := NewConverter(&Config{})
	require.NoError(t, err)

	testCases := []struct {
		name    string
		content interface{}
	}{
		{
			name:    "nil content",
			content: nil,
		},
		{
			name:    "string content",
			content: "invalid",
		},
		{
			name:    "array content",
			content: []interface{}{},
		},
		{
			name: "missing counters",
			content: map[string]interface{}{
				"other": "data",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := converter.convertCheckCounters(tc.content)

			// Should return skip step for invalid content
			if result.Type != "skip" {
				t.Errorf("Expected skip step for %s, got: %s", tc.name, result.Type)
			}
		})
	}
}

func TestConvertRouteUpdate_IPv4(t *testing.T) {
	converter, err := NewConverter(&Config{})
	require.NoError(t, err)

	// Route format should be "prefix -> nexthop" as string array
	content := []interface{}{
		"10.0.0.0/24 -> 192.168.1.1",
	}

	result := converter.convertIPv4Update(content, "ipv4Update")

	// Should generate IPV4 update commands
	require.Contains(t, result.GoCode, "yanet-cli-route", "Expected yanet-cli-route command")
	require.Contains(t, result.GoCode, "10.0.0.0/24", "Expected prefix in command")
	require.Contains(t, result.GoCode, "192.168.1.1", "Expected nexthop in command")
	require.Equal(t, "ipv4Update", result.Type, "Expected step type to be ipv4Update")
}

func TestConvertRouteUpdate_IPv6(t *testing.T) {
	converter, err := NewConverter(&Config{})
	require.NoError(t, err)

	// Route format should be "prefix -> nexthop" as string array
	content := []interface{}{
		"2001:db8::/32 -> fe80::1",
	}

	result := converter.convertIPv6Update(content, "ipv6Update")

	// Should generate IPv6 update commands
	require.Contains(t, result.GoCode, "yanet-cli-route", "Expected yanet-cli-route command")
	require.Contains(t, result.GoCode, "2001:db8::/32", "Expected prefix in command")
	require.Contains(t, result.GoCode, "fe80::1", "Expected nexthop in command")
	require.Equal(t, "ipv6Update", result.Type, "Expected step type to be ipv6Update")
}

func TestConvertRouteUpdate_PreservesOriginalBehavior(t *testing.T) {
	converter, err := NewConverter(&Config{})
	require.NoError(t, err)

	// Route format should be "prefix -> nexthop" as string array
	content := []interface{}{
		"10.0.0.0/24 -> 192.168.1.1",
	}

	// Test IPv4 behavior
	ipv4Result := converter.convertIPv4Update(content, "ipv4Update")
	require.Contains(t, ipv4Result.GoCode, "IPv4", "IPv4 result should contain IPv4 reference")

	// Test IPv6 behavior
	ipv6Result := converter.convertIPv6Update(content, "ipv6Update")
	require.Contains(t, ipv6Result.GoCode, "IPv6", "IPv6 result should contain IPv6 reference")

	// Results should be different due to protocol flag
	require.NotEqual(t, ipv4Result.GoCode, ipv6Result.GoCode, "IPv4 and IPv6 results should differ")
}

func TestConvertRouteRemove_Regular(t *testing.T) {
	converter, err := NewConverter(&Config{})
	require.NoError(t, err)

	content := []interface{}{
		"10.0.0.0/24 -> 192.168.1.1",
		"10.1.0.0/24 -> 192.168.1.2",
	}

	result := converter.convertIPv4Remove(content, "ipv4Remove")

	// Should generate route removal commands
	require.Contains(t, result.GoCode, "remove", "Expected remove operation")
	require.Contains(t, result.GoCode, "10.0.0.0/24", "Expected prefix in command")
	require.Contains(t, result.GoCode, "192.168.1.1", "Expected nexthop in command")
	require.Equal(t, "ipv4Remove", result.Type, "Expected step type to be ipv4Remove")
}

func TestConvertRouteRemove_Labelled(t *testing.T) {
	converter, err := NewConverter(&Config{})
	require.NoError(t, err)

	content := []interface{}{
		"10.0.0.0/24 -> 192.168.1.1 label:transport1",
		"10.1.0.0/24 -> 192.168.1.2 label:transport2",
	}

	result := converter.convertIPv4LabelledRemove(content, "ipv4LabelledRemove")

	// Should generate labelled route removal commands
	require.Contains(t, result.GoCode, "remove", "Expected remove operation")
	require.Contains(t, result.GoCode, "label", "Expected label in command")
	require.Contains(t, result.GoCode, "transport1", "Expected transport label")
	require.Equal(t, "ipv4LabelledRemove", result.Type, "Expected step type to be ipv4LabelledRemove")
}

func TestConvertRouteRemove_PreservesOriginalBehavior(t *testing.T) {
	converter, err := NewConverter(&Config{})
	require.NoError(t, err)

	content := []interface{}{
		"10.0.0.0/24 -> 192.168.1.1",
	}

	// Test regular removal
	regularResult := converter.convertIPv4Remove(content, "ipv4Remove")
	require.Contains(t, regularResult.GoCode, "remove", "Regular result should contain remove operation")

	// Test labelled removal
	labelledResult := converter.convertIPv4LabelledRemove(content, "ipv4LabelledRemove")
	require.Contains(t, labelledResult.GoCode, "remove", "Labelled result should contain remove operation")

	// Results should be different because step types are different
	require.NotEqual(t, regularResult.Type, labelledResult.Type, "Regular and labelled types should differ")
}
