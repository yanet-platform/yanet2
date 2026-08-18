package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTransformCompileArgsStripsJoinedPathRemapFlags pins =-joined stripping.
func TestTransformCompileArgsStripsJoinedPathRemapFlags(t *testing.T) {
	tokens := []string{
		"cc",
		"-ffile-prefix-map=/build=/src",
		"-fdebug-prefix-map=/build=/src",
		"-fmacro-prefix-map=/build=/src",
		"-fdebug-compilation-dir=/build",
		"-Ilib", "-c", "a.c",
	}

	_, args, err := transformCompileArgs(tokens, "", true)
	require.NoError(t, err)
	for _, dropped := range tokens[1:5] {
		require.NotContains(t, args, dropped)
	}

	require.Contains(t, args, "-Ilib")
}

// TestTransformCompileArgsStripsSeparatedPathRemapFlags pins separated strip.
func TestTransformCompileArgsStripsSeparatedPathRemapFlags(t *testing.T) {
	tokens := []string{
		"cc",
		"-ffile-prefix-map", "/build=/src",
		"-fdebug-prefix-map", "/build=/src",
		"-fmacro-prefix-map", "/build=/src",
		"-fdebug-compilation-dir", "/build",
		"-Ilib", "-c", "a.c",
	}

	_, args, err := transformCompileArgs(tokens, "", true)
	require.NoError(t, err)
	for _, dropped := range []string{
		"-ffile-prefix-map", "-fdebug-prefix-map", "-fmacro-prefix-map", "-fdebug-compilation-dir", "/build=/src", "/build",
	} {
		require.NotContains(t, args, dropped)
	}

	require.Contains(t, args, "-Ilib")
}

// TestTransformCompileArgsKeepsSeparatedPreprocessorFlags pins -I/-D/-U argument survival.
func TestTransformCompileArgsKeepsSeparatedPreprocessorFlags(t *testing.T) {
	tokens := []string{
		"cc",
		"-I", "/sdk/include",
		"-D", "FEATURE=1",
		"-U", "FEATURE",
		"-c", "a.c",
	}

	_, args, err := transformCompileArgs(tokens, "", true)
	require.NoError(t, err)
	require.Contains(t, args, "-I")
	require.Contains(t, args, "/sdk/include")
	require.Contains(t, args, "-D")
	require.Contains(t, args, "FEATURE=1")
	require.Contains(t, args, "-U")
	require.Contains(t, args, "FEATURE")
}

// TestLinkDriverFlagsCarriesTargetAndSysroot pins the cross-compile driver flags.
func TestLinkDriverFlagsCarriesTargetAndSysroot(t *testing.T) {
	_, args, err := transformCompileArgs(
		[]string{"clang", "-target", "aarch64-linux-gnu", "--sysroot", "/opt/sysroot", "-Ilib", "-c", "a.c"}, "", true,
	)
	require.NoError(t, err)
	require.Equal(t, []string{"-target", "aarch64-linux-gnu", "--sysroot", "/opt/sysroot"}, linkDriverFlags(args))
}

// TestLinkDriverFlagsCarriesJoinedSpellings pins =-joined driver flags.
func TestLinkDriverFlagsCarriesJoinedSpellings(t *testing.T) {
	_, args, err := transformCompileArgs(
		[]string{
			"clang", "--target=aarch64-linux-gnu", "--sysroot=/opt/sysroot",
			"-isysroot=/opt/sdk", "-arch=arm64", "-Ilib", "-c", "a.c",
		}, "", true,
	)
	require.NoError(t, err)
	require.Equal(
		t,
		[]string{"--target=aarch64-linux-gnu", "--sysroot=/opt/sysroot", "-isysroot=/opt/sdk", "-arch=arm64"},
		linkDriverFlags(args),
	)
}

// TestLinkDriverFlagsCarriesGccToolchain pins both --gcc-toolchain spellings.
func TestLinkDriverFlagsCarriesGccToolchain(t *testing.T) {
	_, args, err := transformCompileArgs(
		[]string{"clang", "-gcc-toolchain", "/opt/gcc", "-Ilib", "-c", "a.c"}, "", true,
	)
	require.NoError(t, err)
	require.Equal(t, []string{"-gcc-toolchain", "/opt/gcc"}, linkDriverFlags(args))

	_, args, err = transformCompileArgs(
		[]string{"clang", "--gcc-toolchain=/opt/gcc", "-Ilib", "-c", "a.c"}, "", true,
	)
	require.NoError(t, err)
	require.Equal(t, []string{"--gcc-toolchain=/opt/gcc"}, linkDriverFlags(args))
}

// TestLinkDriverFlagsEmptyWithoutDriverFlags pins the native-build case.
func TestLinkDriverFlagsEmptyWithoutDriverFlags(t *testing.T) {
	_, args, err := transformCompileArgs([]string{"cc", "-Ilib", "-c", "a.c"}, "", true)
	require.NoError(t, err)
	require.Empty(t, linkDriverFlags(args))
}
