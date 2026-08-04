package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func TestValidSessionName(t *testing.T) {
	for _, name := range []string{"default", "experiment-1", "lab.v2"} {
		if !validSessionName(name) {
			t.Errorf("validSessionName(%q) = false", name)
		}
	}
	for _, name := range []string{"", ".", "..", "../escape", "/tmp/lab", "two words"} {
		if validSessionName(name) {
			t.Errorf("validSessionName(%q) = true", name)
		}
	}
}

func TestExecCommandConsumesSeparator(t *testing.T) {
	command := newApplication().execCommand()
	var got []string
	command.RunE = func(_ *cobra.Command, args []string) error {
		got = args
		return nil
	}
	command.SetArgs([]string{"--", "printf", "--value"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "printf" || got[1] != "--value" {
		t.Fatalf("args = %#v", got)
	}
}

func TestSessionPathsSeparateCheckouts(t *testing.T) {
	first, _, err := sessionPathsForRoot("/tmp/first/yanet2", "default")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := sessionPathsForRoot("/tmp/second/yanet2", "default")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("session paths collide: %q", first)
	}
}

func TestWriteReportCreatesDistinctFiles(t *testing.T) {
	first, err := writeReport(t.TempDir(), []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := writeReport(filepath.Dir(first), []byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("report paths collide: %q", first)
	}
	data, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "second" {
		t.Fatalf("report = %q", data)
	}
}
