package lib

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseCLICommand(t *testing.T) {
	testCases := []struct {
		name       string
		input      string
		wantCmd    string
		wantSubcmd string
		wantErr    bool
	}{
		{
			name:    "simple command",
			input:   "route",
			wantCmd: "route",
			wantErr: false,
		},
		{
			name:       "command with subcommand",
			input:      "balancer real",
			wantCmd:    "balancer",
			wantSubcmd: "real",
			wantErr:    false,
		},
		{
			name:       "command with parameters",
			input:      "nat64 prefix add --cfg nat64test --prefix 64:ff9b::/96",
			wantCmd:    "nat64",
			wantSubcmd: "prefix",
			wantErr:    false,
		},
		{
			name:       "command with quoted parameter",
			input:      `forward module --cfg "test config" --instances 0,1`,
			wantCmd:    "forward",
			wantSubcmd: "module",
			wantErr:    false,
		},
		{
			name:    "empty command",
			input:   "",
			wantErr: true,
		},
		{
			name:    "whitespace only",
			input:   "   \t\n",
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := ParseCLICommand(tc.input)

			if tc.wantErr {
				require.Error(t, err)
				require.Nil(t, cmd)
			} else {
				require.NoError(t, err)
				require.NotNil(t, cmd)
				require.Equal(t, tc.wantCmd, cmd.Command)
				require.Equal(t, tc.wantSubcmd, cmd.Subcommand)
				require.Equal(t, tc.input, cmd.Raw)
			}
		})
	}
}

func TestExtractParameter(t *testing.T) {
	cmd, err := ParseCLICommand("balancer real enable --cfg balancer0 --instances 0 --real-ip 10.0.0.1")
	require.NoError(t, err)

	testCases := []struct {
		name      string
		paramName string
		wantValue string
		wantFound bool
	}{
		{
			name:      "existing parameter",
			paramName: "cfg",
			wantValue: "balancer0",
			wantFound: true,
		},
		{
			name:      "existing parameter with instances",
			paramName: "instances",
			wantValue: "0",
			wantFound: true,
		},
		{
			name:      "existing parameter with real-ip",
			paramName: "real-ip",
			wantValue: "10.0.0.1",
			wantFound: true,
		},
		{
			name:      "non-existing parameter",
			paramName: "nonexistent",
			wantValue: "",
			wantFound: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			value, found := ExtractParameter(cmd, tc.paramName)
			require.Equal(t, tc.wantValue, value)
			require.Equal(t, tc.wantFound, found)
		})
	}
}

func TestExtractParameterWithEquals(t *testing.T) {
	cmd, err := ParseCLICommand("nat64 prefix add --cfg=nat64test --prefix=64:ff9b::/96 --instances=0")
	require.NoError(t, err)

	value, found := ExtractParameter(cmd, "cfg")
	require.True(t, found)
	require.Equal(t, "nat64test", value)

	value, found = ExtractParameter(cmd, "prefix")
	require.True(t, found)
	require.Equal(t, "64:ff9b::/96", value)

	value, found = ExtractParameter(cmd, "instances")
	require.True(t, found)
	require.Equal(t, "0", value)
}

func TestExtractQuotedParameter(t *testing.T) {
	testCases := []struct {
		name      string
		command   string
		paramName string
		wantValue string
		wantFound bool
	}{
		{
			name:      "double quoted parameter",
			command:   `forward module --cfg "test config" --instances 0`,
			paramName: "cfg",
			wantValue: "test config",
			wantFound: true,
		},
		{
			name:      "single quoted parameter",
			command:   `forward module --cfg 'test config' --instances 0`,
			paramName: "cfg",
			wantValue: "test config",
			wantFound: true,
		},
		{
			name:      "unquoted parameter",
			command:   "forward module --cfg testconfig --instances 0",
			paramName: "cfg",
			wantValue: "testconfig",
			wantFound: true,
		},
		{
			name:      "quoted parameter with escapes",
			command:   `forward module --cfg "test \"config\"" --instances 0`,
			paramName: "cfg",
			wantValue: `test "config"`,
			wantFound: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := ParseCLICommand(tc.command)
			require.NoError(t, err)

			value, found := ExtractQuotedParameter(cmd, tc.paramName)
			require.Equal(t, tc.wantValue, value)
			require.Equal(t, tc.wantFound, found)
		})
	}
}

func TestFormatYanet2Command(t *testing.T) {
	testCases := []struct {
		name       string
		module     string
		action     string
		params     map[string]string
		instances  []int
		wantResult string
	}{
		{
			name:   "simple command",
			module: "route",
			action: "insert",
			params: map[string]string{
				"cfg":    "route0",
				"via":    "192.168.1.1",
				"prefix": "10.0.0.0/24",
			},
			instances:  []int{0},
			wantResult: "route insert --instances 0 --cfg route0 --prefix 10.0.0.0/24 --via 192.168.1.1",
		},
		{
			name:   "command with quoted parameter",
			module: "forward",
			action: "module",
			params: map[string]string{
				"cfg": "test config with spaces",
			},
			instances:  []int{0, 1},
			wantResult: "forward module --instances 0,1 --cfg \"test config with spaces\"",
		},
		{
			name:       "module only",
			module:     "balancer",
			params:     map[string]string{},
			instances:  []int{},
			wantResult: "balancer",
		},
		{
			name:   "no instances",
			module: "nat64",
			action: "mapping",
			params: map[string]string{
				"cfg": "nat64test",
			},
			instances:  []int{},
			wantResult: "nat64 mapping --cfg nat64test",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := FormatYanet2Command(tc.module, tc.action, tc.params, tc.instances...)
			require.Equal(t, tc.wantResult, result)
		})
	}
}

func TestValidateCLICommand(t *testing.T) {
	testCases := []struct {
		name    string
		command string
		wantErr bool
	}{
		{
			name:    "valid balancer command",
			command: "balancer real enable --cfg balancer0 --instances 0",
			wantErr: false,
		},
		{
			name:    "valid nat64 command",
			command: "nat64 prefix add --cfg nat64test --prefix 64:ff9b::/96",
			wantErr: false,
		},
		{
			name:    "valid route command",
			command: "route insert --cfg route0 --via 192.168.1.1 10.0.0.0/24",
			wantErr: false,
		},
		{
			name:    "unknown command",
			command: "unknown command",
			wantErr: true,
		},
		{
			name:    "unknown balancer subcommand",
			command: "balancer unknown --cfg balancer0",
			wantErr: true,
		},
		{
			name:    "unknown nat64 subcommand",
			command: "nat64 unknown --cfg nat64test",
			wantErr: true,
		},
		{
			name:    "empty command",
			command: "",
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := ParseCLICommand(tc.command)
			if err != nil {
				if tc.wantErr {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)
			}

			validationErr := ValidateCLICommand(cmd)
			if tc.wantErr {
				require.Error(t, validationErr)
			} else {
				require.NoError(t, validationErr)
			}
		})
	}
}

func TestCommandBuilder(t *testing.T) {
	// Test fluent API
	cmd := NewCommandBuilder("route").
		Action("insert").
		Config("route0").
		Instances(0).
		Param("via", "192.168.1.1").
		Param("prefix", "10.0.0.0/24").
		Build()

	expected := "route insert --instances 0 --cfg route0 --prefix 10.0.0.0/24 --via 192.168.1.1"
	require.Equal(t, expected, cmd)

	// Test minimal builder
	cmd2 := NewCommandBuilder("balancer").Build()
	require.Equal(t, "balancer", cmd2)

	// Test builder with only action
	cmd3 := NewCommandBuilder("nat64").Action("prefix").Build()
	require.Equal(t, "nat64 prefix", cmd3)
}

func TestSplitCommandLine(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple split",
			input:    "route insert --cfg route0",
			expected: []string{"route", "insert", "--cfg", "route0"},
		},
		{
			name:     "with quoted parameter",
			input:    `route insert --cfg "route 0" --via 192.168.1.1`,
			expected: []string{"route", "insert", "--cfg", "route 0", "--via", "192.168.1.1"},
		},
		{
			name:     "with escaped quote",
			input:    `route insert --cfg "route \"0\"" --via 192.168.1.1`,
			expected: []string{"route", "insert", "--cfg", `route "0"`, "--via", "192.168.1.1"},
		},
		{
			name:     "mixed quotes",
			input:    `route insert --cfg 'route 0' --via "192.168.1.1"`,
			expected: []string{"route", "insert", "--cfg", "route 0", "--via", "192.168.1.1"},
		},
		{
			name:     "incomplete quotes",
			input:    `route insert --cfg "incomplete quote`,
			expected: []string{`route insert --cfg "incomplete quote`},
		},
		{
			name:     "empty input",
			input:    "",
			expected: []string{},
		},
		{
			name:     "only whitespace",
			input:    "   ",
			expected: []string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := splitCommandLine(tc.input)
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestHelperFunctions(t *testing.T) {
	cmd, err := ParseCLICommand("balancer real enable --cfg balancer0 --proxy 10.0.0.1")
	require.NoError(t, err)

	// Test containsAllParts
	require.True(t, containsAllParts(cmd, []string{"balancer", "real"}))
	require.True(t, containsAllParts(cmd, []string{"balancer", "cfg"}))
	require.False(t, containsAllParts(cmd, []string{"balancer", "nonexistent"}))

	// Test contains
	require.True(t, contains(cmd, "balancer"))
	require.True(t, contains(cmd, "real"))
	require.True(t, contains(cmd, "proxy"))
	require.False(t, contains(cmd, "nonexistent"))

	// Test containsSubstring
	require.True(t, containsSubstring(cmd, "proxy"))
	require.True(t, containsSubstring(cmd, "balancer0"))
	require.False(t, containsSubstring(cmd, "nonexistent"))
}
