package lab_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLabRecipePreservesArguments(t *testing.T) {
	just, err := exec.LookPath("just")
	if err != nil {
		t.Skip("just is not installed")
	}

	binDirectory := t.TempDir()
	fakeGo := filepath.Join(binDirectory, "go")
	if err := os.WriteFile(fakeGo, []byte("#!/bin/sh\nprintf '<%s>\\n' \"$@\"\n"), 0o755); err != nil {
		t.Fatalf("write fake go: %v", err)
	}

	args := []string{
		"--justfile", "../Justfile", "lab",
		"exec", "--", "printf", "value with spaces; echo HOST_INJECTION",
		"$HOME", "single'quote",
	}
	command := exec.Command(just, args...)
	command.Env = append(os.Environ(), "PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run lab recipe: %v\n%s", err, output)
	}

	want := strings.Join([]string{
		"<run>",
		"<./cmd/yanet-lab>",
		"<exec>",
		"<-->",
		"<printf>",
		"<value with spaces; echo HOST_INJECTION>",
		"<$HOME>",
		"<single'quote>",
		"",
	}, "\n")
	if string(output) != want {
		t.Fatalf("output = %q, want %q", output, want)
	}
}
