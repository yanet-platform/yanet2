package main

import "testing"

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
