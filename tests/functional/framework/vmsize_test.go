package framework

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// lookupFrom returns a lookup function over values, in the os.LookupEnv
// shape, so a test never touches the real host environment.
func lookupFrom(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

// Test_VMSizeFromEnv_DefaultsWhenUnset verifies that an empty environment
// keeps today's VM shape: no suffix and no fingerprint contribution.
func Test_VMSizeFromEnv_DefaultsWhenUnset(t *testing.T) {
	size, err := vmSizeFrom(lookupFrom(nil))

	require.NoError(t, err)
	require.Equal(t, DefaultVMSize(), size)
	require.True(t, size.IsDefault())
	require.Empty(t, size.machineSuffix())
	require.Empty(t, size.fingerprint())
}

// Test_VMSizeFromEnv_ReadsOverrides verifies that both variables are read
// and that a non-default size renders a machine suffix and a fingerprint.
func Test_VMSizeFromEnv_ReadsOverrides(t *testing.T) {
	size, err := vmSizeFrom(lookupFrom(map[string]string{EnvVMCPUs: "8", EnvVMMemory: "16G"}))

	require.NoError(t, err)
	require.Equal(t, VMSize{CPUs: 8, Memory: "16G"}, size)
	require.False(t, size.IsDefault())
	require.Equal(t, "-c8-m16G", size.machineSuffix())
	require.NotEmpty(t, size.fingerprint())
}

// Test_VMSizeFromEnv_RejectsInvalidCPUs verifies that an invalid CPU count
// fails naming the variable, instead of silently keeping the default.
func Test_VMSizeFromEnv_RejectsInvalidCPUs(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"not a number", "many"},
		{"zero", "0"},
		{"negative", "-1"},
		{"above the upper bound", "257"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := vmSizeFrom(lookupFrom(map[string]string{EnvVMCPUs: tc.value}))
			require.ErrorContains(t, err, EnvVMCPUs)
		})
	}
}

// Test_VMSizeFromEnv_RejectsInvalidMemory verifies that a value outside
// QEMU's "-m" syntax fails naming the variable, not falling back silently.
func Test_VMSizeFromEnv_RejectsInvalidMemory(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"unit suffix the -m flag rejects", "16GB"},
		{"no unit suffix", "1024"},
		{"zero amount", "0G"},
		{"leading zero", "016M"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := vmSizeFrom(lookupFrom(map[string]string{EnvVMMemory: tc.value}))
			require.ErrorContains(t, err, EnvVMMemory)
		})
	}
}

// Test_VMSize_MachineSuffix verifies that the suffix is empty exactly for
// the default size, so unset overrides keep today's template file names.
func Test_VMSize_MachineSuffix(t *testing.T) {
	cases := []struct {
		name string
		size VMSize
		want string
	}{
		{"default size", DefaultVMSize(), ""},
		{"custom CPUs only", VMSize{CPUs: 4, Memory: defaultVMMemory}, "-c4-m1G"},
		{"custom memory only", VMSize{CPUs: defaultVMCPUs, Memory: "2G"}, "-c2-m2G"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.size.machineSuffix())
		})
	}
}

// Test_MachineArgs_UsesSize verifies that the vCPU and RAM flags come from
// size rather than being hardcoded.
func Test_MachineArgs_UsesSize(t *testing.T) {
	args := machineArgs("vm", VMSize{CPUs: 8, Memory: "16G"})

	require.Equal(t, []string{"-name", "vm", "-smp", "8", "-m", "16G"}, args[:6])
	require.Equal(t, []string{"-smp", "2", "-m", "1G"}, machineArgs("vm", DefaultVMSize())[2:6])
}

// Test_BootedImagePathFor_SeparatesVMSizes verifies that a non-default
// size names a template of its own.
//
// A saved snapshot only loads into the machine shape it was taken on, so
// the default size alone keeps today's file name.
func Test_BootedImagePathFor_SeparatesVMSizes(t *testing.T) {
	custom := VMSize{CPUs: 4, Memory: "8G"}

	require.Equal(t, "/tmp/yanet-test-booted-v3-c4-m8G.qcow2", BootedImagePathFor("/tmp/yanet-test.qcow2", custom))
	require.Equal(t, "/tmp/yanet-test-booted-v3.qcow2", BootedImagePathFor("/tmp/yanet-test.qcow2", DefaultVMSize()))
	require.Equal(t, BootedImagePath("/tmp/yanet-test.qcow2"), BootedImagePathFor("/tmp/yanet-test.qcow2", DefaultVMSize()))
}
