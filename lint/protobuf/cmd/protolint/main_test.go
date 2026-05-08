package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsExcluded(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		excludes []string
		want     bool
	}{
		{
			name:     "not excluded",
			path:     "modules/foo/bar.proto",
			excludes: []string{"subprojects"},
			want:     false,
		},
		{
			name:     "directly excluded",
			path:     "subprojects/foo/bar.proto",
			excludes: []string{"subprojects"},
			want:     true,
		},
		{
			name:     "excluded by one of multiple",
			path:     "vendor/foo/bar.proto",
			excludes: []string{"subprojects", "vendor"},
			want:     true,
		},
		{
			name:     "empty excludes",
			path:     "modules/foo/bar.proto",
			excludes: nil,
			want:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isExcluded(tc.path, tc.excludes)
			if got != tc.want {
				t.Errorf("isExcluded(%q, %v) = %v, want %v", tc.path, tc.excludes, got, tc.want)
			}
		})
	}
}

func TestCollectProtoFilesExclude(t *testing.T) {
	root := t.TempDir()

	// Create two directories: one included, one excluded.
	includedDir := filepath.Join(root, "goodpb")
	excludedDir := filepath.Join(root, "subprojects", "vendored")
	for _, d := range []string{includedDir, excludedDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	writeFile := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	writeFile(filepath.Join(includedDir, "good.proto"), "syntax = \"proto3\";\npackage goodpb;\n")
	writeFile(filepath.Join(excludedDir, "bad.proto"), "syntax = \"proto3\";\npackage vendored;\n")

	excludes := []string{filepath.Join(root, "subprojects")}
	files, err := collectProtoFiles(root, excludes)
	if err != nil {
		t.Fatalf("collectProtoFiles: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("got %d files, want 1: %v", len(files), files)
	}
	if files[0].GoPkgFound {
		t.Errorf("expected no go_package in good.proto")
	}
}

// writeProto creates a temporary .proto file with the given content inside a
// subdirectory named dirName under a root temp dir. It returns the file path
// and the root directory.
func writeProto(t *testing.T, dirName, content string) (filePath, root string) {
	t.Helper()

	root = t.TempDir()
	dir := filepath.Join(root, dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	filePath = filepath.Join(dir, "test.proto")
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write proto: %v", err)
	}

	return filePath, root
}

func TestLintFile(t *testing.T) {
	tests := []struct {
		name    string
		dirName string
		content string
		wantOK  bool
	}{
		{
			name:    "valid: no go_package — nothing to check",
			dirName: "mypb",
			content: `syntax = "proto3";
package mypb;
`,
			wantOK: true,
		},
		{
			name:    "valid: correct go_package with alias and version",
			dirName: "mypb/v1",
			content: `syntax = "proto3";
package modules.acl.controlplane.aclpb.v1;
option go_package = "github.com/example/mypb/v1;mypb";
`,
			wantOK: true,
		},
		{
			name:    "valid: v2 version",
			dirName: "mypb/v2",
			content: `syntax = "proto3";
package mypb;
option go_package = "github.com/example/mypb/v2;mypb";
`,
			wantOK: true,
		},
		{
			name:    "valid: v0 version",
			dirName: "mypb/v0",
			content: `syntax = "proto3";
package mypb;
option go_package = "github.com/example/mypb/v0;mypb";
`,
			wantOK: true,
		},
		{
			name:    "valid: v1beta version",
			dirName: "mypb/v1beta",
			content: `syntax = "proto3";
package mypb;
option go_package = "github.com/example/mypb/v1beta;mypb";
`,
			wantOK: true,
		},
		{
			name:    "valid: v12 version",
			dirName: "mypb/v12",
			content: `syntax = "proto3";
package mypb;
option go_package = "github.com/example/mypb/v12;mypb";
`,
			wantOK: true,
		},
		{
			name:    "error: go_package is multiline (no semicolon at end of line)",
			dirName: "mypb/v1",
			content: `syntax = "proto3";
package mypb;
option go_package = "github.com/example/mypb/v1;mypb"
`,
			wantOK: false,
		},
		{
			name:    "error: go_package missing alias",
			dirName: "mypb/v1",
			content: `syntax = "proto3";
package mypb;
option go_package = "github.com/example/mypb/v1";
`,
			wantOK: false,
		},
		{
			name:    "error: go_package alias does not match penultimate segment",
			dirName: "mypb/v1",
			content: `syntax = "proto3";
package mypb;
option go_package = "github.com/example/mypb/v1;wrongalias";
`,
			wantOK: false,
		},
		{
			name:    "error: last segment is not a version",
			dirName: "mypb",
			content: `syntax = "proto3";
package mypb;
option go_package = "github.com/example/mypb;mypb";
`,
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, _ := writeProto(t, tc.dirName, tc.content)

			pf, err := parseProtoFile(path)
			if err != nil {
				t.Fatalf("parseProtoFile: %v", err)
			}

			got := lintFile(pf)
			if got != tc.wantOK {
				t.Errorf("lintFile() = %v, want %v", got, tc.wantOK)
			}
		})
	}
}

func TestParseProtoFile(t *testing.T) {
	tests := []struct {
		name          string
		dirName       string
		content       string
		wantGoPkgLine string
		wantFound     bool
	}{
		{
			name:      "no go_package",
			dirName:   "mypb",
			content:   "syntax = \"proto3\";\npackage mypb;\n",
			wantFound: false,
		},
		{
			name:          "with go_package",
			dirName:       "mypb",
			content:       "syntax = \"proto3\";\npackage mypb;\noption go_package = \"github.com/example/mypb;mypb\";\n",
			wantGoPkgLine: `option go_package = "github.com/example/mypb;mypb";`,
			wantFound:     true,
		},
		{
			name:          "go_package without semicolon at end",
			dirName:       "mypb",
			content:       "syntax = \"proto3\";\npackage mypb;\noption go_package = \"github.com/example/mypb;mypb\"\n",
			wantGoPkgLine: `option go_package = "github.com/example/mypb;mypb"`,
			wantFound:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, _ := writeProto(t, tc.dirName, tc.content)

			pf, err := parseProtoFile(path)
			if err != nil {
				t.Fatalf("parseProtoFile: %v", err)
			}

			if pf.GoPkgFound != tc.wantFound {
				t.Errorf("GoPkgFound = %v, want %v", pf.GoPkgFound, tc.wantFound)
			}
			if pf.GoPkgLine != tc.wantGoPkgLine {
				t.Errorf("GoPkgLine = %q, want %q", pf.GoPkgLine, tc.wantGoPkgLine)
			}
		})
	}
}

func TestParseGoPkg(t *testing.T) {
	tests := []struct {
		name           string
		line           string
		wantImportPath string
		wantAlias      string
		wantOK         bool
	}{
		{
			name:           "with alias",
			line:           `option go_package = "github.com/example/mypb;mypb";`,
			wantImportPath: "github.com/example/mypb",
			wantAlias:      "mypb",
			wantOK:         true,
		},
		{
			name:           "without alias",
			line:           `option go_package = "github.com/example/mypb";`,
			wantImportPath: "github.com/example/mypb",
			wantAlias:      "",
			wantOK:         true,
		},
		{
			name:   "no quotes",
			line:   `option go_package = github.com/example/mypb;`,
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			importPath, alias, ok := parseGoPkg(tc.line)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if importPath != tc.wantImportPath {
				t.Errorf("importPath = %q, want %q", importPath, tc.wantImportPath)
			}
			if alias != tc.wantAlias {
				t.Errorf("alias = %q, want %q", alias, tc.wantAlias)
			}
		})
	}
}

func TestIsSemver(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"v1", true},
		{"v2", true},
		{"v12", true},
		{"v100", true},
		{"v0", true},
		{"v1beta", true},
		{"v1.0", true},
		{"v", false},
		{"1", false},
		{"", false},
		{"mypb", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := isSemver(tc.input)
			if got != tc.want {
				t.Errorf("isSemver(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestPenultimateSegment(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"github.com/org/repo/aclpb/v1", "aclpb"},
		{"github.com/org/repo/mypb/v2", "mypb"},
		{"github.com/org/repo", "org"},
		{"single", ""},
		{"a/b", "a"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := penultimateSegment(tc.input)
			if got != tc.want {
				t.Errorf("penultimateSegment(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
