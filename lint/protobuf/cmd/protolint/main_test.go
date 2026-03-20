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
	if files[0].pkg != "goodpb" {
		t.Errorf("got package %q, want %q", files[0].pkg, "goodpb")
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
			name:    "valid: package matches dir, no go_package",
			dirName: "mypb",
			content: `syntax = "proto3";
package mypb;
`,
			wantOK: true,
		},
		{
			name:    "valid: package matches dir, correct go_package with alias",
			dirName: "mypb",
			content: `syntax = "proto3";
package mypb;
option go_package = "github.com/yanet-platform/yanet2/mypb;mypb";
`,
			wantOK: true,
		},
		{
			name:    "error: package does not match dir",
			dirName: "mypb",
			content: `syntax = "proto3";
package otherpb;
`,
			wantOK: false,
		},
		{
			name:    "error: go_package is multiline (no semicolon at end of line)",
			dirName: "mypb",
			content: `syntax = "proto3";
package mypb;
option go_package = "github.com/yanet-platform/yanet2/mypb;mypb"
`,
			wantOK: false,
		},
		{
			name:    "error: go_package missing alias",
			dirName: "mypb",
			content: `syntax = "proto3";
package mypb;
option go_package = "github.com/yanet-platform/yanet2/mypb";
`,
			wantOK: false,
		},
		{
			name:    "error: go_package alias does not match package",
			dirName: "mypb",
			content: `syntax = "proto3";
package mypb;
option go_package = "github.com/yanet-platform/yanet2/mypb;wrongalias";
`,
			wantOK: false,
		},
		{
			name:    "error: go_package import path does not match file location",
			dirName: "mypb",
			content: `syntax = "proto3";
package mypb;
option go_package = "github.com/yanet-platform/yanet2/wrong/path;mypb";
`,
			wantOK: false,
		},
		{
			name:    "error: package mismatch AND go_package alias mismatch",
			dirName: "mypb",
			content: `syntax = "proto3";
package otherpb;
option go_package = "github.com/yanet-platform/yanet2/mypb;wrongalias";
`,
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, root := writeProto(t, tc.dirName, tc.content)

			pf, err := parseProtoFile(path)
			if err != nil {
				t.Fatalf("parseProtoFile: %v", err)
			}

			got := lintFile(pf, root)
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
		wantPkg       string
		wantGoPkgLine string
		wantFound     bool
	}{
		{
			name:      "package only",
			dirName:   "mypb",
			content:   "syntax = \"proto3\";\npackage mypb;\n",
			wantPkg:   "mypb",
			wantFound: false,
		},
		{
			name:          "package and go_package",
			dirName:       "mypb",
			content:       "syntax = \"proto3\";\npackage mypb;\noption go_package = \"github.com/example/mypb;mypb\";\n",
			wantPkg:       "mypb",
			wantGoPkgLine: `option go_package = "github.com/example/mypb;mypb";`,
			wantFound:     true,
		},
		{
			name:          "go_package without semicolon at end",
			dirName:       "mypb",
			content:       "syntax = \"proto3\";\npackage mypb;\noption go_package = \"github.com/example/mypb;mypb\"\n",
			wantPkg:       "mypb",
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

			if pf.pkg != tc.wantPkg {
				t.Errorf("pkg = %q, want %q", pf.pkg, tc.wantPkg)
			}
			if pf.goPkgFound != tc.wantFound {
				t.Errorf("goPkgFound = %v, want %v", pf.goPkgFound, tc.wantFound)
			}
			if pf.goPkgLine != tc.wantGoPkgLine {
				t.Errorf("goPkgLine = %q, want %q", pf.goPkgLine, tc.wantGoPkgLine)
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
