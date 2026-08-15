package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIsSeparator verifies only pure separator runs are rejected.
func TestIsSeparator(t *testing.T) {
	tests := []struct {
		text     string
		expected bool
	}{
		{"---", true}, {"=====", true}, {"/////", true}, {"++++", true}, {"***", true},
		{"--- foo ---", false}, {"* ===", false}, {"### something", false}, {"---------  ---------", false},
		{"+-+-+-+-+-+-+-+-", false}, {"+-----+", false}, {"ordinary prose", false},
	}
	for _, test := range tests {
		if actual := isSeparator(test.text); actual != test.expected {
			t.Fatalf("%q: expected %t, got %t", test.text, test.expected, actual)
		}
	}
}

// TestLiteralExtraction verifies comment-looking literal content is ignored.
func TestLiteralExtraction(t *testing.T) {
	comments := slashComments([]byte("const raw = r#\"// ======\"#;\nconst templ = `// ======`;\n// ======\n"))
	if len(comments) != 1 || !isSeparator(comments[0].Text) {
		t.Fatalf("unexpected comments: %#v", comments)
	}
}

// TestBlankDocComments verifies empty line and doc comments are not separators.
func TestBlankDocComments(t *testing.T) {
	comments := slashComments([]byte("//\n///\n//!\n"))
	if len(comments) != 3 {
		t.Fatalf("unexpected comments: %#v", comments)
	}
	for _, comment := range comments {
		if isSeparator(comment.Text) {
			t.Fatalf("blank comment was reported: %#v", comment)
		}
	}
}

// TestCommentProfiles verifies every supported comment profile finds pure runs.
func TestCommentProfiles(t *testing.T) {
	tests := []struct {
		name        string
		fileProfile profile
		source      string
	}{
		{"c", profileSlash, "// =====\n"},
		{"go", profileSlash, "// =====\n"},
		{"go doc", profileSlash, "/// =====\n"},
		{"go doc compact", profileSlash, "///=====\n"},
		{"rust doc", profileSlash, "//! =====\n"},
		{"rust doc compact", profileSlash, "//!=====\n"},
		{"rust", profileSlash, "// =====\n"},
		{"typescript", profileSlash, "// =====\n"},
		{"css", profileSlash, "/* *** */\n"},
		{"proto", profileSlash, "// =====\n"},
		{"hash", profileHash, "# =====\n"},
		{"html", profileMarkup, "<!-- ===== -->\n"},
		{"sql", profileSQL, "-- =====\n"},
		{"preprocessor", profileSlash, "#define VALUE // =====\n"},
	}
	for _, test := range tests {
		comments := comments("test", []byte(test.source), test.fileProfile)
		if len(comments) != 1 || !isSeparator(comments[0].Text) {
			t.Fatalf("%s: unexpected comments: %#v", test.name, comments)
		}
	}
}

// TestLiteralAndDocumentExemptions verifies non-comment pure runs are ignored.
func TestLiteralAndDocumentExemptions(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		fileProfile profile
		source      string
	}{
		{"rust raw", "test.rs", profileSlash, "let value = r#\"// =====\"#;\n"},
		{"typescript template", "test.ts", profileSlash, "const value = `// =====`;\n"},
		{"python triple", "test.py", profileHash, "value = '''\n# =====\n'''\nvalue = '''inline'''\n"},
		{"shell heredoc", "test.sh", profileHash, "cat <<-EOF\n# =====\nEOF\n"},
		{"markdown", "test.md", profileMarkup, "# Heading\n~~~\n// =====\n~~~\n"},
		{"table", "test.yaml", profileHash, "# --------  --------\n"},
		{"encoding", "test.py", profileHash, "# -*- coding: utf-8 -*-\n"},
		{"sql string", "test.sql", profileSQL, "SELECT '-- =====';\n"},
	}
	for _, test := range tests {
		for _, extracted := range comments(test.path, []byte(test.source), test.fileProfile) {
			if isSeparator(extracted.Text) {
				t.Fatalf("%s: false positive: %#v", test.name, extracted)
			}
		}
	}
}

// TestMultilineLiteralLine verifies later diagnostics retain their physical line number.
func TestMultilineLiteralLine(t *testing.T) {
	comments := slashComments([]byte("const value = `first\nsecond`;\n// =====\n"))
	if len(comments) != 1 || comments[0].Line != 3 {
		t.Fatalf("unexpected comments: %#v", comments)
	}
}

// TestRustLifetimes verifies lifetimes do not consume following comments as character literals.
func TestRustLifetimes(t *testing.T) {
	extracted := comments("source.rs", []byte("struct Foo<'a> {}\n// =====\n"), profileSlash)
	if len(extracted) != 1 || !isSeparator(extracted[0].Text) {
		t.Fatalf("unexpected comments: %#v", extracted)
	}
	extracted = comments("source.rs", []byte("let value = '\\n';\nlet raw = r#\"// ===== ignored =====\"#;\n"), profileSlash)
	if len(extracted) != 0 {
		t.Fatalf("rust literals became comments: %#v", extracted)
	}
}

// TestMultilineMarkup verifies HTML comments are assessed as whole payloads.
func TestMultilineMarkup(t *testing.T) {
	comments := markupComments([]byte("<!-- prose\n=====\nprose -->\n"))
	if len(comments) != 1 || comments[0].Line != 1 || isSeparator(comments[0].Text) {
		t.Fatalf("unexpected comments: %#v", comments)
	}
}

// TestYAMLBlockScalar verifies literal scalar contents are not source comments.
func TestYAMLBlockScalar(t *testing.T) {
	comments := hashCommentsForPath("fixture.yaml", []byte("value: |-\n  # =====\n# =====\n"))
	if len(comments) != 1 || comments[0].Line != 3 || !isSeparator(comments[0].Text) {
		t.Fatalf("unexpected YAML comments: %#v", comments)
	}
	comments = hashCommentsForPath("fixture.yaml", []byte("- description: |\n    # =====\n  # =====\n"))
	if len(comments) != 1 || comments[0].Line != 3 || !isSeparator(comments[0].Text) {
		t.Fatalf("unexpected sequence scalar comments: %#v", comments)
	}
}

// TestPythonCommentQuotes verifies comment text cannot open triple-quote state.
func TestPythonCommentQuotes(t *testing.T) {
	comments := hashCommentsForPath("fixture.py", []byte("# \"\"\" prose\n# =====\n"))
	if len(comments) != 2 || !isSeparator(comments[1].Text) {
		t.Fatalf("unexpected Python comments: %#v", comments)
	}
}

// TestHashLanguageStates verifies shell, Python, and TOML state does not cross formats.
func TestHashLanguageStates(t *testing.T) {
	for _, path := range []string{"fixture.py", "fixture.yaml", "fixture.toml"} {
		comments := hashCommentsForPath(path, []byte("flags = 1 << 4\n# =====\n"))
		if len(comments) != 1 || !isSeparator(comments[0].Text) {
			t.Fatalf("%s: %#v", path, comments)
		}
	}
	comments := hashCommentsForPath("fixture.toml", []byte("value = \"\"\"\n# =====\n\"\"\" # =====\n"))
	if len(comments) != 1 || comments[0].Line != 3 || !isSeparator(comments[0].Text) {
		t.Fatalf("unexpected TOML comments: %#v", comments)
	}
}

// TestLineStartHashComments verifies inline hashes remain data.
func TestLineStartHashComments(t *testing.T) {
	for _, path := range []string{"Dockerfile", ".gitignore", ".dockerignore"} {
		comments := hashCommentsForPath(path, []byte("artifact#===\nvalue # =====\n  # =====\n"))
		if len(comments) != 1 || comments[0].Line != 3 || !isSeparator(comments[0].Text) {
			t.Fatalf("%s inline hashes became comments: %#v", path, comments)
		}
	}
}

// TestYAMLCommentPlacement verifies scalar hashes are not YAML comments without separation.
func TestYAMLCommentPlacement(t *testing.T) {
	comments := hashCommentsForPath("fixture.yaml", []byte("url: https://host/#---\nvalue: ok # ===== found =====\n- >+\n  # ===== literal =====\n# ===== later =====\n"))
	if len(comments) != 2 || comments[0].Line != 2 || comments[1].Line != 5 {
		t.Fatalf("unexpected YAML comments: %#v", comments)
	}
}

// TestRustNestedBlockComments verifies nested Rust comments are whole payloads.
func TestRustNestedBlockComments(t *testing.T) {
	comments := comments("fixture.rs", []byte("/* outer\n/* inner */\n=====\n*/\n"), profileSlash)
	if len(comments) != 1 || comments[0].Line != 1 || isSeparator(comments[0].Text) {
		t.Fatalf("unexpected Rust comments: %#v", comments)
	}
}

// TestMarkdownFenceMatching verifies mismatched and indented fences do not suppress comments.
func TestMarkdownFenceMatching(t *testing.T) {
	comments := markupComments([]byte("```\n<!-- ===== literal ===== -->\n~~~\n<!-- ===== still literal ===== -->\n```\n    ~~~\n<!-- ===== found ===== -->\n"))
	if len(comments) != 1 || comments[0].Line != 7 {
		t.Fatalf("unexpected Markdown comments: %#v", comments)
	}
}

// TestMarkdownIndentedFenceClose verifies indented closing fences end literal blocks.
func TestMarkdownIndentedFenceClose(t *testing.T) {
	comments := markupCommentsForPath("document.md", []byte("  ```\n  <!-- ===== -->\n  ```\n<!-- ==== -->\n"))
	if len(comments) != 1 || comments[0].Line != 4 || !isSeparator(comments[0].Text) {
		t.Fatalf("unexpected comments: %#v", comments)
	}
}

// TestSQLDollarQuotes verifies PostgreSQL dollar strings hide only their literal contents.
func TestSQLDollarQuotes(t *testing.T) {
	comments := sqlComments([]byte("SELECT $$ -- ===== $$;\nSELECT $tag$ -- ===== $tag$;\n-- =====\n"))
	if len(comments) != 1 || comments[0].Line != 3 || !isSeparator(comments[0].Text) {
		t.Fatalf("unexpected SQL comments: %#v", comments)
	}
}

// TestSQLCommentDelimiter verifies SQL comments preserve their payload.
func TestSQLCommentDelimiter(t *testing.T) {
	comments := sqlComments([]byte("-- ---\nSELECT '-- ---';\n"))
	if len(comments) != 1 || comments[0].Line != 1 || !isSeparator(comments[0].Text) {
		t.Fatalf("unexpected comments: %#v", comments)
	}
}

// TestSQLBlockComments verifies SQL block comments are extracted as whole payloads.
func TestSQLBlockComments(t *testing.T) {
	comments := comments("fixture.sql", []byte("/* === */\nSELECT '/* ===== */';\nSELECT \"/* ===== */\";\nSELECT $$ /* ===== */ $$;\n/* ===\nLabeled section\n=== */\n"), profileSQL)
	if len(comments) != 2 || comments[0].Line != 1 || !isSeparator(comments[0].Text) || comments[1].Line != 5 || isSeparator(comments[1].Text) {
		t.Fatalf("unexpected SQL block comments: %#v", comments)
	}
}

// TestSQLNestedBlockComments verifies nested SQL comments stay one payload.
func TestSQLNestedBlockComments(t *testing.T) {
	comments := sqlComments([]byte("/* outer\n/* inner */\n-- =====\n*/\n-- =====\n"))
	if len(comments) != 2 || comments[0].Line != 1 || isSeparator(comments[0].Text) || comments[1].Line != 5 || !isSeparator(comments[1].Text) {
		t.Fatalf("unexpected SQL comments: %#v", comments)
	}
}

// TestShellHeredocSyntax verifies shifts stay code while shell words delimit heredocs.
func TestShellHeredocSyntax(t *testing.T) {
	comments := hashCommentsForPath("fixture.sh", []byte("((flags = value << shift))\n# ===== found =====\ncat <<'123'\n# ===== literal =====\n123\n# ===== later =====\n"))
	if len(comments) != 2 || comments[0].Line != 2 || comments[1].Line != 6 {
		t.Fatalf("unexpected shell comments: %#v", comments)
	}
	comments = hashCommentsForPath(".githooks/pre-commit", []byte("cat <<EOF; then\n# ===== literal =====\nEOF\n# ===== found =====\n"))
	if len(comments) != 1 || comments[0].Line != 4 {
		t.Fatalf("unexpected hook comments: %#v", comments)
	}
	comments = hashCommentsForPath("fixture.sh", []byte("cat <<\\EOF\n# =====\nEOF\n# =====\n"))
	if len(comments) != 1 || comments[0].Line != 4 {
		t.Fatalf("unexpected backslash-quoted heredoc comments: %#v", comments)
	}
	comments = hashCommentsForPath("fixture.sh", []byte("cat <<EOF # ===\n# =====\nEOF\n"))
	if len(comments) != 1 || comments[0].Line != 1 || !isSeparator(comments[0].Text) {
		t.Fatalf("unexpected heredoc opener comments: %#v", comments)
	}
	comments = hashCommentsForPath("fixture.sh", []byte("cat <<EOF\n  EOF\n# =====\nEOF\n# =====\n"))
	if len(comments) != 1 || comments[0].Line != 5 || !isSeparator(comments[0].Text) {
		t.Fatalf("indented heredoc delimiter closed the body: %#v", comments)
	}
	comments = hashCommentsForPath("fixture.sh", []byte("cat <<-EOF\n  EOF\n# =====\n\tEOF\n# =====\n"))
	if len(comments) != 1 || comments[0].Line != 5 || !isSeparator(comments[0].Text) {
		t.Fatalf("tab-stripped heredoc delimiter was mishandled: %#v", comments)
	}
	comments = hashCommentsForPath("fixture.sh", []byte("# Example: cat <<EOF\n# =====\n"))
	if len(comments) != 2 || comments[1].Line != 2 || !isSeparator(comments[1].Text) {
		t.Fatalf("comment text opened a heredoc: %#v", comments)
	}
	comments = hashCommentsForPath("fixture.sh", []byte("cat <<FIRST <<SECOND\nFIRST\n# =====\nSECOND\n# =====\n"))
	if len(comments) != 1 || comments[0].Line != 5 || !isSeparator(comments[0].Text) {
		t.Fatalf("multiple heredocs were mishandled: %#v", comments)
	}
	comments = hashCommentsForPath("fixture.sh", []byte("value='\n# =====\n'\n# =====\n"))
	if len(comments) != 1 || comments[0].Line != 4 || !isSeparator(comments[0].Text) {
		t.Fatalf("multiline shell quote exposed literal data: %#v", comments)
	}
	comments = hashCommentsForPath("fixture.sh", []byte("value=\"$(cat <<EOF\n# =====\nEOF\n)\"\n# =====\n"))
	if len(comments) != 1 || comments[0].Line != 5 || !isSeparator(comments[0].Text) {
		t.Fatalf("quoted command-substitution heredoc was missed: %#v", comments)
	}
	comments = hashCommentsForPath("fixture.sh", []byte("value=foo#===\n# =====\n"))
	if len(comments) != 1 || comments[0].Line != 2 || !isSeparator(comments[0].Text) {
		t.Fatalf("shell word data became a comment: %#v", comments)
	}
	comments = hashCommentsForPath("fixture.sh", []byte("value=foo\\ #===\nvalue=foo\\\n#===\n# =====\n"))
	if len(comments) != 1 || comments[0].Line != 4 || !isSeparator(comments[0].Text) {
		t.Fatalf("escaped shell word data became comments: %#v", comments)
	}
	comments = hashCommentsForPath("fixture.sh", []byte("value=$(#===\nprintf ok\n)\n# =====\n"))
	if len(comments) != 2 || comments[0].Line != 1 || comments[1].Line != 4 {
		t.Fatalf("command-substitution comment was missed: %#v", comments)
	}
	comments = hashCommentsForPath("fixture.sh", []byte("value=`#===\nprintf ok\n`\n# =====\n"))
	if len(comments) != 2 || comments[0].Line != 1 || comments[1].Line != 4 {
		t.Fatalf("backtick-substitution comment was missed: %#v", comments)
	}
	comments = hashCommentsForPath("fixture.sh", []byte("(\nvalue=\"$(echo \"$(#===\nprintf ok\n)\")\"\n)\n# =====\n"))
	if len(comments) != 2 || comments[0].Line != 2 || comments[1].Line != 6 {
		t.Fatalf("nested substitution comments were mishandled: %#v", comments)
	}
	comments = hashCommentsForPath("fixture.sh", []byte("value=`echo \\`#===\nprintf ok\n\\``\n# =====\n"))
	if len(comments) != 2 || comments[0].Line != 1 || comments[1].Line != 4 {
		t.Fatalf("nested backtick comments were mishandled: %#v", comments)
	}
}

// TestMarkupPathAwareness verifies fences apply only to Markdown documents.
func TestMarkupPathAwareness(t *testing.T) {
	comments := markupCommentsForPath("document.html", []byte("```\n<!-- ===== found ===== -->\n"))
	if len(comments) != 1 || comments[0].Line != 2 {
		t.Fatalf("unexpected HTML comments: %#v", comments)
	}
	comments = markupCommentsForPath("document.md", []byte("```\n<!-- ===== literal ===== -->\n```not-close\n<!-- ===== literal ===== -->\n```\n    <!-- ===== literal ===== -->\n<!-- ===== found ===== -->\n"))
	if len(comments) != 1 || comments[0].Line != 7 {
		t.Fatalf("unexpected Markdown comments: %#v", comments)
	}
	comments = markupCommentsForPath("document.md", []byte("`<!-- ===== -->`\n<!-- ===== -->\n"))
	if len(comments) != 1 || comments[0].Line != 2 || !isSeparator(comments[0].Text) {
		t.Fatalf("Markdown inline code became a comment: %#v", comments)
	}
	comments = markupCommentsForPath("document.xml", []byte("<![CDATA[\n<!-- ===== -->\n]]>\n<!-- ===== -->\n"))
	if len(comments) != 1 || comments[0].Line != 4 || !isSeparator(comments[0].Text) {
		t.Fatalf("XML CDATA became a comment: %#v", comments)
	}
	comments = markupCommentsForPath("document.md", []byte("Unmatched `\n\n<!-- ===== -->\n"))
	if len(comments) != 1 || comments[0].Line != 3 || !isSeparator(comments[0].Text) {
		t.Fatalf("unmatched backtick hid a comment: %#v", comments)
	}
	comments = markupCommentsForPath("document.md", []byte("Unmatched `\n\n<!-- ===== -->\n`\n"))
	if len(comments) != 1 || comments[0].Line != 3 || !isSeparator(comments[0].Text) {
		t.Fatalf("later paragraph backtick hid a comment: %#v", comments)
	}
	comments = markupCommentsForPath("document.md", []byte("Text `\n<!-- ===== -->\n`\n"))
	if len(comments) != 1 || comments[0].Line != 2 || !isSeparator(comments[0].Text) {
		t.Fatalf("HTML block did not interrupt a code span: %#v", comments)
	}
	comments = markupCommentsForPath("document.md", []byte("\t<!-- ===== -->\n<!-- ===== -->\n"))
	if len(comments) != 1 || comments[0].Line != 2 || !isSeparator(comments[0].Text) {
		t.Fatalf("tab-indented code became a comment: %#v", comments)
	}
	comments = markupCommentsForPath("document.md", []byte("> Text `\n> <!-- ===== -->\n> `\n"))
	if len(comments) != 1 || comments[0].Line != 2 || !isSeparator(comments[0].Text) {
		t.Fatalf("blockquote HTML did not interrupt a code span: %#v", comments)
	}
	comments = markupCommentsForPath("document.md", []byte("- item\n    <!-- ===== -->\n"))
	if len(comments) != 1 || comments[0].Line != 2 || !isSeparator(comments[0].Text) {
		t.Fatalf("list-item HTML became indented code: %#v", comments)
	}
	comments = markupCommentsForPath("document.md", []byte("> ~~~\n> <!-- ===== -->\n> ~~~\n<!-- ===== -->\n"))
	if len(comments) != 1 || comments[0].Line != 4 || !isSeparator(comments[0].Text) {
		t.Fatalf("blockquote fence exposed literal content: %#v", comments)
	}
	comments = markupCommentsForPath("document.md", []byte("> ~~~\n<!-- ===== -->\n~~~\n"))
	if len(comments) != 1 || comments[0].Line != 2 || !isSeparator(comments[0].Text) {
		t.Fatalf("fence survived its blockquote: %#v", comments)
	}
	comments = markupCommentsForPath("document.md", []byte("-     <!-- ===== -->\n<!-- ===== -->\n"))
	if len(comments) != 1 || comments[0].Line != 2 || !isSeparator(comments[0].Text) {
		t.Fatalf("list-item indented code became a comment: %#v", comments)
	}
	comments = markupCommentsForPath("document.md", []byte("- ~~~\n<!-- ===== -->\n~~~\n"))
	if len(comments) != 1 || comments[0].Line != 2 || !isSeparator(comments[0].Text) {
		t.Fatalf("fence survived its list item: %#v", comments)
	}
	comments = markupCommentsForPath("document.md", []byte("> ~~~\n> > <!-- ===== -->\n> ~~~\n<!-- ===== -->\n"))
	if len(comments) != 1 || comments[0].Line != 4 || !isSeparator(comments[0].Text) {
		t.Fatalf("nested blockquote content escaped its fence: %#v", comments)
	}
	comments = markupCommentsForPath("document.md", []byte("> ~~~\n> > ~~~\n> <!-- ===== -->\n> ~~~\n<!-- ===== -->\n"))
	if len(comments) != 1 || comments[0].Line != 5 || !isSeparator(comments[0].Text) {
		t.Fatalf("nested blockquote marker closed its fence: %#v", comments)
	}
	comments = markupCommentsForPath("document.md", []byte("- ~~~\n  - ~~~\n  <!-- ===== -->\n  ~~~\n<!-- ===== -->\n"))
	if len(comments) != 1 || comments[0].Line != 5 || !isSeparator(comments[0].Text) {
		t.Fatalf("nested list marker closed its fence: %#v", comments)
	}
	comments = markupCommentsForPath("document.md", []byte("- ~~~\n\n  <!-- ===== -->\n  ~~~\n<!-- ===== -->\n"))
	if len(comments) != 1 || comments[0].Line != 5 || !isSeparator(comments[0].Text) {
		t.Fatalf("blank list-item line closed its fence: %#v", comments)
	}
	comments = markupCommentsForPath("document.md", []byte("Escaped \\`\n<!-- ===== -->\n"))
	if len(comments) != 1 || comments[0].Line != 2 || !isSeparator(comments[0].Text) {
		t.Fatalf("escaped backtick hid a comment: %#v", comments)
	}
	comments = markupCommentsForPath("document.md", []byte("`code\ninline <!-- ===== -->\n`\n<!-- ===== -->\n"))
	if len(comments) != 1 || comments[0].Line != 4 || !isSeparator(comments[0].Text) {
		t.Fatalf("multiline code span exposed a comment: %#v", comments)
	}
}

// TestCPPAndINIComments verifies raw C++ strings and INI semicolon comments are lexical.
func TestCPPAndINIComments(t *testing.T) {
	comments := comments("fixture.cc", []byte("auto value = R\"tag(a\" // ===== literal =====\n)tag\";\n// ===== found =====\n"), profileSlash)
	if len(comments) != 1 || comments[0].Line != 3 {
		t.Fatalf("unexpected C++ comments: %#v", comments)
	}
	comments = iniComments([]byte("value = \"; ===== literal =====\"\n; ===== found =====\n"))
	if len(comments) != 1 || comments[0].Line != 2 {
		t.Fatalf("unexpected INI comments: %#v", comments)
	}
	comments = iniComments([]byte("Environment=MARKER=#===\nvalue=;===\n  # =====\n; =====\n"))
	if len(comments) != 2 || comments[0].Line != 3 || comments[1].Line != 4 {
		t.Fatalf("INI values became comments: %#v", comments)
	}
}

// TestTripleQuotedSlashLanguages verifies raw strings hide comment markers.
func TestTripleQuotedSlashLanguages(t *testing.T) {
	for _, path := range []string{"fixture.java", "fixture.kt"} {
		comments := comments(path, []byte("val raw = \"\"\"\n\"\n// ===== literal =====\n\"\"\"\n// =====\n"), profileSlash)
		if len(comments) != 1 || comments[0].Line != 5 || !isSeparator(comments[0].Text) {
			t.Fatalf("%s: %#v", path, comments)
		}
	}
	comments := comments("fixture.java", []byte(`String text = """
\"""
// =====
"""
// =====
`), profileSlash)
	if len(comments) != 1 || comments[0].Line != 5 || !isSeparator(comments[0].Text) {
		t.Fatalf("escaped Java text-block delimiter ended the string: %#v", comments)
	}
}

// TestTypeScriptTemplates verifies interpolation comments are code and escaped backticks remain literal.
func TestTypeScriptTemplates(t *testing.T) {
	extracted := comments("fixture.ts", []byte("const value = `${(() => {\n  // ===== hidden =====\n  return \"x\";\n})()}`;\n"), profileSlash)
	if len(extracted) != 1 || extracted[0].Line != 2 {
		t.Fatalf("unexpected interpolation comments: %#v", extracted)
	}
	extracted = comments("fixture.ts", []byte("const value = `\\` // ===== literal =====`;\n"), profileSlash)
	if len(extracted) != 0 {
		t.Fatalf("escaped backtick became comment: %#v", extracted)
	}
}

// TestTemplateInterpolationLexing verifies interpolation braces respect lexical state.
func TestTemplateInterpolationLexing(t *testing.T) {
	tests := []struct {
		name         string
		source       string
		expectedLine int
	}{
		{
			name:         "quoted brace",
			source:       "const value = `${\"}\" // =====\n}`;\n",
			expectedLine: 1,
		},
		{
			name:         "regex brace",
			source:       "const value = `${/}/ // =====\n}`;\n",
			expectedLine: 1,
		},
		{
			name:         "return regex",
			source:       "const value = `${(() => { return /}/; })() // =====\n}`;\n",
			expectedLine: 1,
		},
		{
			name:         "in keyword regex",
			source:       "const value = `${value in /}/ // =====\n}`;\n",
			expectedLine: 1,
		},
		{
			name:         "instanceof keyword regex",
			source:       "const value = `${value instanceof /}/ // =====\n}`;\n",
			expectedLine: 1,
		},
		{
			name:         "of keyword regex",
			source:       "const value = `${value of /}/ // =====\n}`;\n",
			expectedLine: 1,
		},
		{
			name:         "division",
			source:       "const value = `${value / divisor // =====\n}`;\n",
			expectedLine: 1,
		},
		{
			name:         "multiline prefix",
			source:       "const value = `first\nsecond ${() => {\n// =====\n}}`;\n",
			expectedLine: 3,
		},
	}
	for _, test := range tests {
		comments := comments("fixture.ts", []byte(test.source), profileSlash)
		if len(comments) != 1 || comments[0].Line != test.expectedLine || !isSeparator(comments[0].Text) {
			t.Fatalf("%s: %#v", test.name, comments)
		}
	}
}

// TestTripleLiteralLexing verifies ordinary strings cannot open Python or TOML triple state.
func TestTripleLiteralLexing(t *testing.T) {
	for _, path := range []string{"fixture.py", "fixture.toml"} {
		comments := hashCommentsForPath(path, []byte("value = \"'''\"\n# ===== hidden =====\n"))
		if len(comments) != 1 || comments[0].Line != 2 {
			t.Fatalf("%s: %#v", path, comments)
		}
	}
	comments := hashCommentsForPath("fixture.toml", []byte("value = \"\"\"text \\q still literal\nnext\n\"\"\" # ===== found =====\n"))
	if len(comments) != 1 || comments[0].Line != 3 {
		t.Fatalf("unexpected escaped triple comments: %#v", comments)
	}
}

// TestSQLQuotedDollarText verifies ordinary SQL strings cannot open dollar quotes.
func TestSQLQuotedDollarText(t *testing.T) {
	comments := sqlComments([]byte("SELECT '$tag$';\n-- ===== hidden =====\nSELECT '$tag$';\n"))
	if len(comments) != 1 || comments[0].Line != 2 {
		t.Fatalf("unexpected quoted dollar comments: %#v", comments)
	}
}

// TestBitDiagram verifies source ASCII boxes are not pure runs.
func TestBitDiagram(t *testing.T) {
	comments := slashComments([]byte("// +-+-+-+-+-+-+-+-+-+-+-+-+\n"))
	if len(comments) != 1 || isSeparator(comments[0].Text) {
		t.Fatalf("bit diagram was reported: %#v", comments)
	}
}

// TestProfileFor verifies unclassified paths are not scanned.
func TestProfileFor(t *testing.T) {
	fileProfile, known := profileFor("new-language.example")
	if known || fileProfile != profileNone {
		t.Fatalf("unknown path classified as %v, known=%t", fileProfile, known)
	}
}

// TestParseTrackedFiles verifies index metadata and paths are retained.
func TestParseTrackedFiles(t *testing.T) {
	files := parseTrackedFiles([]byte("100644 a 0\tstaged-new.go\x00100755 b 0\ttracked.yaml\x00100644 c 2\tconflict.go\x00"))
	if len(files) != 2 || files[0] != (trackedFile{Path: "staged-new.go", Mode: "100644", Hash: "a"}) || files[1] != (trackedFile{Path: "tracked.yaml", Mode: "100755", Hash: "b"}) {
		t.Fatalf("unexpected tracked files: %#v", files)
	}
}

// TestProfileClassification verifies explicit no-comment and unknown classifications.
func TestProfileClassification(t *testing.T) {
	tests := []struct {
		path          string
		expectedKnown bool
	}{
		{"Makefile", true},
		{"subprojects/dpdk", false},
		{"ignored-untracked.example", false},
		{"Cargo.lock", true},
		{"package.lock", false},
	}
	for _, test := range tests {
		_, known := profileFor(test.path)
		if known != test.expectedKnown {
			t.Fatalf("%s: expected known=%t, got %t", test.path, test.expectedKnown, known)
		}
	}
}

// TestGoCommentCapitalizationScopes verifies Go comment selection.
func TestGoCommentCapitalizationScopes(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		findings int
	}{
		{"function doc", "package p\n\n// lower function documentation.\nfunc Exported() {}\n", 1},
		{"cgo export directive doc", "package p\n\n//export name\n// lower exported documentation.\nfunc Exported() {}\n", 1},
		{"valid cgo export directive alone", "package p\n\nimport \"C\"\n\n//export Exported\nfunc Exported() {}\n", 0},
		{"valid cgo export directive doc", "package p\n\nimport \"C\"\n\n//export Exported\n// lower exported documentation.\nfunc Exported() {}\n", 1},
		{"cgo export directive is line-scoped", "package p\n\nimport \"C\"\n\n//export Exported\n//export wrong.\nfunc Exported() {}\n", 1},
		{"cgo export directive after URL", "package p\n\nimport \"C\"\n\n// https://example.test\n//export Exported\nfunc Exported() {}\n", 0},
		{"pure Go export marker doc", "package p\n\n//export values are cached.\nfunc Exported() {}\n", 1},
		{"mismatched cgo export sentence", "package p\n\nimport \"C\"\n\n//export wrong.\nfunc Exported() {}\n", 1},
		{"mismatched cgo export directive doc", "package p\n\nimport \"C\"\n\n//export wrong\n// lower export documentation.\nfunc Exported() {}\n", 1},
		{"body export marker is prose", "package p\n\nfunc body() {\n\t//export values are cached.\n}\n", 1},
		{"package var doc", "package p\n\n// lower variable documentation.\nvar ExportedVariable = 1\n", 1},
		{"package var value spec doc", "package p\n\nvar (\n\t// lower value documentation.\n\tvalue = 1\n)\n", 1},
		{"function body", "package p\n\nfunc body() {\n\t// lower body sentence.\n}\n", 1},
		{"top-level function literal body", "package p\n\nvar callback = func() {\n\t// lower callback body sentence.\n}\n", 1},
		{"literal-adjacent comment outside body", "package p\n\nvar callback = func() {} // lower callback sentence.\n", 0},
		{"block body comment", "package p\n\nfunc body() {\n\t/*\n\tlower block sentence.\n\t*/\n}\n", 1},
		{"indented block code", "package p\n\nfunc body() {\n/*\n    lower code.\n*/\n}\n", 0},
		{"decorative indented block code", "package p\n\nfunc body() {\n/*\n*     lower code.\n*/\n}\n", 0},
		{"block code then prose", "package p\n\nfunc body() {\n/*\n    lower code.\n*/\n// lower prose follows.\n}\n", 1},
		{"multiline first paragraph", "package p\n\nfunc body() {\n\t// lower continuation\n\t// sentence ends.\n}\n", 1},
		{"physical continuation is not a new opening", "package p\n\nfunc body() {\n\t// Good opening.\n\t// lower later sentence.\n}\n", 0},
		{"later paragraph is ignored", "package p\n\nfunc body() {\n\t/*\n\tGood opening.\n\n\tlower later paragraph.\n\t*/\n}\n", 0},
		{"exact function name", "package p\n\n// helper handles work.\nfunc helper() {}\n", 0},
		{"function name with Unicode punctuation", "package p\n\n// helper—handles work.\nfunc helper() {}\n", 0},
		{"function name with Unicode hyphen", "package p\n\n// helper‑handles work.\nfunc helper() {}\n", 1},
		{"lowercase Unicode opening", "package p\n\n// élision starts here.\nfunc helper() {}\n", 1},
		{"function name with Unicode identifier continuation", "package p\n\n// helperα handles work.\nfunc helper() {}\n", 1},
		{"function name is an exact token", "package p\n\n// helper-only handles work.\nfunc helper() {}\n", 1},
		{"function name compound slash is not exact", "package p\n\n// read/write paths are handled.\nfunc read() {}\n", 1},
		{"function name compound ampersand is not exact", "package p\n\n// foo&bar paths are handled.\nfunc foo() {}\n", 1},
		{"exact method name", "package p\n\ntype thing struct{}\n\n// method handles work.\nfunc (thing) method() {}\n", 0},
		{"exact var name", "package p\n\n// value stores work.\nvar value = 1\n", 0},
		{"exact grouped var name", "package p\n\n// first stores work.\nvar (\n\tfirst = 1\n\tsecond = 2\n)\n", 0},
		{"grouped var name is scoped to its spec", "package p\n\nvar (\n\t// first stores work.\n\tfirst = 1\n\t// first stores work.\n\tsecond = 2\n)\n", 1},
		{"package comment excluded", "// lower package sentence.\npackage p\n", 0},
		{"type comment excluded", "package p\n\n// lower type sentence.\ntype Thing struct{}\n", 0},
		{"const comment excluded", "package p\n\n// lower const sentence.\nconst ThingValue = 1\n", 0},
		{"import comment excluded", "package p\n\n// lower import sentence.\nimport \"fmt\"\n", 0},
		{"local type comment excluded", "package p\n\nfunc body() {\n\t// lower local type sentence.\n\ttype local struct{}\n}\n", 0},
		{"local const comment excluded", "package p\n\nfunc body() {\n\t// lower local const sentence.\n\tconst local = 1\n}\n", 0},
		{"local interface field comment excluded", "package p\n\nfunc body() {\n\ttype local interface {\n\t\t// lower local method sentence.\n\t\tMethod()\n\t}\n}\n", 0},
		{"nested declaration fields excluded", "package p\n\nfunc body() {\n\ttype local struct {\n\t\tNested struct {\n\t\t\t// lower nested field sentence.\n\t\t\tField int\n\t\t}\n\t}\n}\n", 0},
		{"standalone declaration comment excluded", "package p\n\nfunc body() {\n\ttype local struct {\n\t\t// lower standalone sentence.\n\t}\n}\n", 0},
		{"type parameter comment excluded", "package p\n\nfunc body() {\n\ttype local[\n\t\t// lower type parameter sentence.\n\t\tT comparable,\n\t] struct{}\n}\n", 0},
		{"local anonymous struct field comment excluded", "package p\n\nfunc body() {\n\tvar local struct {\n\t\t// lower anonymous field sentence.\n\t\tField int\n\t}\n}\n", 0},
		{"local anonymous interface field comment excluded", "package p\n\nfunc body() {\n\tvar local interface {\n\t\t// lower anonymous method sentence.\n\t\tMethod()\n\t}\n}\n", 0},
		{"local var type delimiter comment excluded", "package p\n\nfunc body() {\n\tvar value /* lower type syntax. */ int\n\t_ = value\n}\n", 0},
		{"local var initializer comment retained", "package p\n\nfunc body() {\n\tvar value /* lower body prose. */ = 1\n\t_ = value\n}\n", 1},
		{"indented body code then prose", "package p\n\nfunc body() {\n\t//    lower preformatted code.\n\t// lower prose follows.\n}\n", 1},
		{"tab-indented body code then prose", "package p\n\nfunc body() {\n\t//\tlower preformatted code.\n\t// lower prose follows.\n}\n", 1},
		{"block tab indentation stays prose", "package p\n\nfunc body() {\n\t/*\n\tlower block prose.\n\t*/\n}\n", 1},
		{"block tab extra indentation is code", "package p\n\nfunc body() {\n\t/*\n\t\tlower preformatted code.\n\t*/\n}\n", 0},
		{"local composite anonymous field comment excluded", "package p\n\nfunc body() {\n\tvar local = struct {\n\t\t// lower composite field sentence.\n\t\tField int\n\t}{}\n}\n", 0},
		{"short composite anonymous field comment excluded", "package p\n\nfunc body() {\n\tlocal := struct {\n\t\t// lower short declaration field sentence.\n\t\tField int\n\t}{}\n\t_ = local\n}\n", 0},
		{"pointer type comment excluded", "package p\n\nfunc body() {\n\tvar value * /* lower pointer type sentence. */ int\n\t_ = value\n}\n", 0},
		{"generic selector type comment excluded", "package p\n\nfunc body() {\n\tvar value pkg /* lower selector type sentence. */ .Type[T]\n\t_ = value\n}\n", 0},
		{"generic index list type comment excluded", "package p\n\nfunc body() {\n\tvar value pkg /* lower index list type sentence. */ .Type[T, U]\n\t_ = value\n}\n", 0},
		{"parenthesized type comment excluded", "package p\n\nfunc body() {\n\tvar value ( /* lower parenthesized type sentence. */ pkg.Type)\n\t_ = value\n}\n", 0},
		{"short generic composite type comment excluded", "package p\n\nfunc body() {\n\tvalue := pkg /* lower composite type sentence. */ .Type[T]{}\n\t_ = value\n}\n", 0},
		{"builtin type argument comment excluded", "package p\n\nfunc body() {\n\tvalue := new(/* lower new type sentence. */ pkg.Type)\n\titems := make([] /* lower make type sentence. */ int, 1)\n\t_, _ = value, items\n}\n", 0},
		{"nested parenthesized builtin type comments excluded", "package p\n\nfunc body() {\n\tvalue := (((new))( /* lower nested new type sentence. */ pkg.Type))\n\titems := ((make))( /* lower nested make type sentence. */ []int, 1)\n\t_, _ = value, items\n}\n", 0},
		{"conversion array type comment excluded", "package p\n\nfunc body(value int) {\n\t_ = [] /* lower conversion type sentence. */ byte(value)\n}\n", 0},
		{"conversion map type comment excluded", "package p\n\nfunc body(value int) {\n\t_ = map[string] /* lower conversion type sentence. */ byte(value)\n}\n", 0},
		{"conversion channel type comment excluded", "package p\n\nfunc body(value int) {\n\t_ = chan /* lower conversion type sentence. */ byte(value)\n}\n", 0},
		{"conversion pointer array type comment excluded", "package p\n\nfunc body(value int) {\n\t_ = (* /* lower conversion type sentence. */ []byte)(value)\n}\n", 0},
		{"conversion pointer map type comment excluded", "package p\n\nfunc body(value int) {\n\t_ = (* /* lower conversion type sentence. */ map[string]byte)(value)\n}\n", 0},
		{"conversion pointer function type comment excluded", "package p\n\nfunc body(value int) {\n\t_ = (* /* lower conversion type sentence. */ func(int) int)(value)\n}\n", 0},
		{"conversion closing delimiter comment excluded", "package p\n\nfunc body(value int) {\n\t_ = []byte /* lower conversion delimiter sentence. */ (value)\n}\n", 0},
		{"composite closing delimiter comment excluded", "package p\n\nfunc body() {\n\t_ = struct{ Field int } /* lower composite delimiter sentence. */ {}\n}\n", 0},
		{"ambiguous pointer conversion remains selected", "package p\n\nfunc body(value int) {\n\t_ = (* /* lower pointer prose. */ function)(value)\n}\n", 1},
		{"dereference prose remains selected", "package p\n\nfunc body(ptr any) {\n\t_ = * /* lower dereference prose. */ ptr\n}\n", 1},
		{"dereference call prose remains selected", "package p\n\nfunc body() {\n\t_ = * /* lower dereference call prose. */ call()\n}\n", 1},
		{"selector call prose remains selected", "package p\n\nfunc body(value any) {\n\tpkg /* lower function call sentence. */ .Call(value)\n}\n", 1},
		{"function literal signature comment excluded", "package p\n\nfunc body() {\n\t_ = func /* lower function signature sentence. */ () {}\n}\n", 0},
		{"shadowed make type comment retained", "package p\n\nfunc body() {\n\tmake := func(value []int, size int) []int { return value[:size] }\n\t_ = make(/* lower shadowed make type sentence. */ []int, 1)\n}\n", 1},
		{"parenthesized shadowed make type comment retained", "package p\n\nfunc body() {\n\tmake := func(value []int, size int) []int { return value[:size] }\n\t_ = ((make))(/* lower shadowed make type sentence. */ []int, 1)\n}\n", 1},
		{"shadowed new type comment retained", "package p\n\nfunc body() {\n\tnew := func(value any) any { return value }\n\t_ = new(/* lower shadowed new type sentence. */ pkg.Type)\n}\n", 1},
		{"package shadowed make type comment retained", "package p\n\nvar make = func(value []int, size int) []int { return value[:size] }\n\nfunc body() {\n\t_ = make(/* lower package shadowed type sentence. */ []int, 1)\n}\n", 1},
		{"package shadowed make does not hide new", "package p\n\nvar make = func(value []int, size int) []int { return value[:size] }\n\nfunc body() {\n\t_ = new(/* lower builtin new type sentence. */ pkg.Type)\n}\n", 0},
		{"package shadowed new does not hide make", "package p\n\nvar new = func(value any) any { return value }\n\nfunc body() {\n\t_ = make([] /* lower builtin make type sentence. */ int, 1)\n}\n", 0},
		{"type assertion comment excluded", "package p\n\nfunc body(value any) {\n\t_, _ = value.(/* lower assertion type sentence. */ pkg.Type)\n}\n", 0},
		{"type assertion closing delimiter comment excluded", "package p\n\nfunc body(value any) {\n\t_, _ = value.(pkg.Type /* lower assertion delimiter sentence. */)\n}\n", 0},
		{"type switch assertion comment excluded", "package p\n\nfunc body(value any) {\n\tswitch value.( /* lower type switch assertion sentence. */ type) {\n\t}\n}\n", 0},
		{"type switch case type comments excluded", "package p\n\nfunc body(value any) {\n\tswitch value.(type) {\n\tcase /* lower first type sentence. */ int, /* lower second type sentence. */ string:\n\t}\n}\n", 0},
		{"expression switch case comments retained", "package p\n\nfunc body(value int) {\n\tswitch value {\n\tcase /* lower expression case sentence. */ 1:\n\t}\n}\n", 1},
		{"function literal parameter type comment excluded", "package p\n\nfunc body() {\n\tcallback := func(value * /* lower parameter type sentence. */ int) {}\n\t_ = callback\n}\n", 0},
		{"nested literal body remains selected", "package p\n\nfunc body() {\n\tcallback := func() {\n\t\t// lower nested body sentence.\n\t}\n\t_ = callback\n}\n", 1},
		{"local var name is allowed exactly", "package p\n\nfunc body() {\n\t// helper handles work.\n\tvar helper = 1\n\t// lower ordinary body prose.\n}\n", 1},
		{"local var name is an exact token", "package p\n\nfunc body() {\n\t// helper-only handles work.\n\tvar helper = 1\n}\n", 1},
		{"ordinary body comment retained around local declarations", "package p\n\nfunc body() {\n\ttype local struct{}\n\t// lower body sentence.\n}\n", 1},
		{"indented line continuation stays in paragraph", "package p\n\nfunc body() {\n\t// lower prose\n\t//     continues here.\n}\n", 1},
		{"outside body excluded", "package p\n\nfunc body() {}\n\n// lower outside sentence.\n", 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			findings, err := goCommentFindings("fixture.go", []byte(test.source))
			require.NoError(t, err)
			if len(findings) != test.findings {
				t.Fatalf("expected %d findings, got %#v", test.findings, findings)
			}
			for _, finding := range findings {
				if finding.Message != "comment sentence starts with lowercase word" {
					t.Fatalf("unexpected finding: %#v", finding)
				}
			}
		})
	}
}

// TestGoCommentCapitalizationExclusions verifies opening exemptions.
func TestGoCommentCapitalizationExclusions(t *testing.T) {
	tests := []struct {
		name    string
		comment string
	}{
		{"go generate directive", "//go:generate lower sentence."},
		{"short lowercase directive", "//go:x lower sentence."},
		{"digit directive", "//go:1x lower sentence."},
		{"punctuation-tail directive", "//go:a? lower sentence."},
		{"nolint directive", "//nolint:reason lower sentence."},
		{"lint directive", "//lint:ignore lower sentence."},
		{"param tag", "// @param lower sentence."},
		{"param tag with direction", "// @param[in] lower sentence."},
		{"return tag", "// @return lower sentence."},
		{"see tag", "// @see lower sentence."},
		{"file tag", "// @file lower sentence."},
		{"todo marker", "// TODO: lower sentence."},
		{"fixme marker", "// FIXME: lower sentence."},
		{"todo owner marker", "// todo(owner): lower sentence."},
		{"fixme owner marker", "// FiXmE(owner): lower sentence."},
		{"URL", "// https://example.test."},
		{"URL with balanced path parentheses", "// https://example.test/path(foo)."},
		{"URL with internal path parentheses", "// https://example.test/a(b)c."},
		{"relative current URL", "// ./v1/items."},
		{"relative parent URL", "// ../guide."},
		{"bracketed IPv6 path URL", "// [::1]/v1/items?full=1."},
		{"email URL", "// <user@example.com>."},
		{"ssh URL", "// ssh://host/path."},
		{"git URL", "// git://host/path."},
		{"grpc URL", "// grpc://host/service."},
		{"unix URL", "// unix://socket/path."},
		{"mailto URL", "// mailto:user@example.com."},
		{"URN URL", "// urn:ietf:rfc:3986."},
		{"about URI", "// about:blank."},
		{"localhost endpoint", "// localhost:8080."},
		{"IPv6 endpoint", "// [::1]:80."},
		{"root relative URL", "// /v1/items."},
		{"localhost path URL", "// localhost/v1/items."},
		{"hostname root URL", "// example.com/."},
		{"hostname port path URL", "// example.com:8080/path."},
		{"hostname query URL", "// example.com?query=1."},
		{"hostname fragment URL", "// example.com#fragment."},
		{"hostname anchor URL", "// example.com#anchor."},
		{"bare hostname URL", "// example.com."},
		{"MAC address", "// 00:11:22:33:44:55."},
		{"IPv4 CIDR", "// 192.0.2.0/24."},
		{"IPv6 CIDR", "// 2001:db8::/32."},
		{"standalone Markdown link", "// [lower label](https://example.test)."},
		{"Markdown fragment link", "// [lower label](#anchor)."},
		{"Markdown one-byte destination", "// [lower label](a)."},
		{"Markdown tilde destination", "// [lower label](~)."},
		{"Markdown slash destination", "// [lower label](/)."},
		{"Markdown empty destination", "// [lower label]()."},
		{"standalone Markdown image", "// ![packet flow](diagram.svg)."},
		{"nested Markdown link", "// [lower [nested]](https://example.test)."},
		{"Markdown link with quoted title", "// [lower label](https://example.test \"Title\")."},
		{"Markdown link with parenthesized title", "// [lower label](https://example.test (Title))."},
		{"Markdown link with angle destination", "// [lower label](<https://example.test>)."},
		{"Markdown angle destination with spaces", "// [lower label](<guide start>)."},
		{"Markdown angle destination with close parenthesis", "// [lower label](<a)b>)."},
		{"Markdown link with escaped title delimiters", "// [lower label](guide (Title \\(nested\\)))."},
		{"Markdown reference definition", "// [docs]: https://example.test."},
		{"relative Markdown reference definition", "// [docs]: /guide/start."},
		{"Markdown file reference title", "// [docs]: diagram.svg \"Title\"."},
		{"Markdown relative reference title", "// [docs]: ../guide \"Title\"."},
		{"Markdown relative reference parenthesized title", "// [docs]: ../guide (Title)."},
		{"opaque news URI", "// news:comp.lang.go."},
		{"valid image reference", "// ![alt][label]."},
		{"numbered heading", "// 123 heading."},
		{"code", "// parse(value)."},
		{"package declaration code", "// package p."},
		{"import declaration code", "// import \"fmt\"."},
		{"function declaration code", "// func helper() {}."},
		{"type declaration code", "// type helper struct{}."},
		{"multiple file declarations", "// package p; import \"fmt\"."},
		{"multiple imports", "// import \"fmt\"; import \"os\"."},
		{"defer call statement", "// defer run()."},
		{"go call statement", "// go run()."},
		{"labeled call statement", "// label: run()."},
		{"increment statement list", "// run(); value++."},
		{"slice expression", "// values[1:2]."},
		{"slice expression statement list", "// run(); values[1:2]."},
		{"input output arrow", "// input -> output."},
		{"input output fat arrow", "// input => output."},
		{"input output left arrow", "// input ← output."},
		{"input output bidirectional arrow", "// input ↔ output."},
		{"pointer arrow", "// ptr->field."},
		{"direct unary pointer arrow", "// *node->next."},
		{"direct unary address arrow", "// &node->next."},
		{"parenthesized selector C arrow", "// (node.field)->next."},
		{"parenthesized call C arrow", "// (getNode())->next."},
		{"parenthesized index C arrow", "// (nodes[0])->next."},
		{"parenthesized struct cast C arrow", "// (struct node *)ptr->next."},
		{"parenthesized const struct cast C arrow", "// (const struct node *)ptr->next."},
		{"parenthesized named cast C arrow", "// (node_t *)ptr->next."},
		{"parenthesized void cast C arrow", "// (void *)ptr->next."},
		{"parenthesized volatile struct cast C arrow", "// (volatile struct node *)ptr->next."},
		{"parenthesized volatile const cast C arrow", "// (volatile const node_t *)ptr->next."},
		{"parenthesized unsigned long cast C arrow", "// (unsigned long *)ptr->next."},
		{"parenthesized pointer qualified cast C arrow", "// (node_t * const)ptr->next."},
		{"parenthesized typedef cast C arrow", "// (node_t)ptr->next."},
		{"parenthesized binary C arrow", "// (ptr + 1)->next."},
		{"parenthesized conditional C arrow", "// (cond ? left : right)->next."},
		{"parenthesized comma C arrow", "// (a, b)->next."},
		{"parenthesized function pointer cast C arrow", "// (node_t (*)(void))ptr->next."},
		{"parenthesized array pointer cast C arrow", "// (node_t (*)[4])ptr->next."},
		{"direct unary not arrow", "// !node->ready."},
		{"direct unary complement arrow", "// ~node->flags."},
		{"prefix increment arrow", "// ++node->next."},
		{"prefix decrement arrow", "// --node->next."},
		{"postfix increment arrow", "// node->next++."},
		{"postfix decrement arrow", "// node->next--."},
		{"sizeof arrow", "// sizeof node->next."},
		{"pointer indexed arrow", "// ptr->field[index]."},
		{"input filter output arrow chain", "// input -> filter -> output."},
		{"pointer field next arrow chain", "// ptr->field->next."},
		{"mixed arrow chain", "// input => filter -> output."},
		{"select send clause with body", "// case ch <- value: run()."},
		{"select receive assignment with body", "// case value := <-ch: run()."},
		{"select send composite incomplete header", "// case ch <- value{field: 1}."},
		{"select send composite with body", "// case ch <- value{field: 1}: run()."},
		{"valid rune x", "// 'x'."},
		{"valid rune question", "// '?'."},
		{"valid rune escape", "// '\\n'."},
		{"valid rune Unicode", "// '世'."},
		{"indexing code", "// items[index]."},
		{"composite code", "// value{field: 1}."},
		{"selector code", "// object.Field."},
		{"parenthesized addition code", "// value + (other)."},
		{"parenthesized comparison code", "// left == (right)."},
		{"parenthesized assignment code", "// value = (other)."},
		{"string literal code", "// \"value\"."},
		{"parenthesized expression code", "// (value + other)."},
		{"nested parenthesized string code", "// ((\"value\"))."},
		{"statement list code", "// foo(); bar()."},
		{"select send clause", "// case ch <- value."},
		{"select receive assignment clause", "// case value := <-ch."},
		{"select receive-only clause", "// case <-ch."},
		{"if statement code", "// if ready { run() }."},
		{"labeled for code", "// label: for {}."},
		{"nested labeled block code", "// outer: inner: for {}."},
		{"backtick", "// `code`."},
		{"string punctuation code", "// \"value?\"."},
		{"rune punctuation code", "// '?'."},
		{"raw string punctuation code", "// `value?`."},
		{"diagram", "// +---+."},
		{"assignment", "// value = other."},
		{"indexed vector heading", "// size-2 vector: [0] = packets, [1] = bytes."},
		{"comparison", "// left == right."},
		{"arithmetic", "// total + increment."},
		{"tight assignment", "// value=other."},
		{"tight comparison", "// left==right."},
		{"tight arithmetic", "// count+1."},
		{"increment", "// count++."},
		{"decrement", "// count--."},
		{"spaced increment", "// count ++."},
		{"spaced decrement", "// count --."},
		{"subtraction", "// total - decrement."},
		{"spaced subtraction", "// value - other."},
		{"multiplication", "// total * factor."},
		{"division", "// total / factor."},
		{"remainder", "// total % factor."},
		{"bitwise and", "// flags & mask."},
		{"bitwise or", "// flags | mask."},
		{"bitwise xor", "// flags ^ mask."},
		{"shift left", "// value << bits."},
		{"shift right", "// value >> bits."},
		{"and not", "// flags &^ mask."},
		{"not equal", "// left != right."},
		{"less than", "// left < right."},
		{"less or equal", "// left <= right."},
		{"greater than", "// left > right."},
		{"greater or equal", "// left >= right."},
		{"logical and", "// left && right."},
		{"logical or", "// left || right."},
		{"send", "// value <- channel."},
		{"add assignment", "// value += delta."},
		{"subtract assignment", "// value -= delta."},
		{"multiply assignment", "// value *= factor."},
		{"divide assignment", "// value /= factor."},
		{"remainder assignment", "// value %= factor."},
		{"and assignment", "// flags &= mask."},
		{"or assignment", "// flags |= mask."},
		{"xor assignment", "// flags ^= mask."},
		{"shift left assignment", "// flags <<= 1."},
		{"shift right assignment", "// flags >>= 1."},
		{"and not assignment", "// flags &^= mask."},
		{"negative assignment", "// value = -1."},
		{"not assignment", "// ok = !ready."},
		{"address assignment", "// ptr = &value."},
		{"complement assignment", "// flags = ^mask."},
		{"receive assignment", "// result = <-channel."},
		{"return statement", "// return value."},
		{"labeled goto statement", "// label: goto retry."},
		{"labeled return statement", "// label: return value."},
		{"labeled break with explicit semicolon", "// break retryLoop;."},
		{"labeled continue with explicit semicolon", "// continue retryLoop;."},
		{"default clause with whitespace", "// default : run()."},
		{"case clause with tab", "// case\tready: run()."},
		{"struct field snippet", "// field int."},
		{"slice member snippet", "// field []customType."},
		{"map member snippet", "// field map[string]customType."},
		{"pointer member snippet", "// field *customType."},
		{"sized array member snippet", "// field [size]customType."},
		{"generic member snippet", "// field customType[T]."},
		{"parenthesized member snippet", "// field (customType)."},
		{"function field snippet", "// field func()."},
		{"interface method snippet", "// method(value int) error."},
		{"interface method with lowercase types", "// method(value customType) anotherType."},
		{"any member snippet", "// field any."},
		{"exported named member snippet", "// field CustomType."},
		{"predeclared true", "// true."},
		{"predeclared false", "// false."},
		{"predeclared nil", "// nil."},
		{"labeled break with target", "// outer: break inner."},
		{"labeled continue with target", "// outer: continue inner."},
		{"else block statement", "// else { run() }."},
		{"local declaration statement", "// var value int."},
		{"slice type expression", "// []int."},
		{"array type expression", "// [4]byte."},
		{"channel type expression", "// chan int."},
		{"receive channel type expression", "// <-chan int."},
		{"map type expression", "// map[string]int."},
		{"function type expression", "// func(string) error."},
		{"spaced not assignment", "// ok = ! ready."},
		{"spaced not condition", "// if ! ready { run() }."},
		{"spaced not for condition", "// for ! ready { run() }."},
		{"spaced not return value", "// return ! ready."},
		{"spaced not switch condition", "// switch ! ready { case true: run() }."},
		{"spaced not case condition", "// case ! ready: run()."},
		{"valid hostname endpoint", "// service.example:8443."},
		{"single-label hostname endpoint", "// router:8080."},
		{"tight double not assignment", "// value = !! ready."},
		{"spaced double not assignment", "// value = ! ! ready."},
		{"spaced double not expression", "// ! ! ready."},
		{"short fragment", "// field: value"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := "package p\n\nfunc body() {\n\t" + test.comment + "\n}\n"
			findings, err := goCommentFindings("fixture.go", []byte(source))
			require.NoError(t, err)
			if len(findings) != 0 {
				t.Fatalf("unexpected findings: %#v", findings)
			}
		})
	}
}

// TestGoCommentCapitalizationOperatorBoundaries verifies operator boundaries.
func TestGoCommentCapitalizationOperatorBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		comment  string
		findings int
	}{
		{"wrong prose opening", "// wrong.", 1},
		{"dash prose opening", "// - lower prose.", 1},
		{"plus prose opening", "// + lower prose.", 1},
		{"single-word dash list opening", "// - lower.", 1},
		{"single-word plus list opening", "// + lower.", 1},
		{"list heading is prose", "// - lower heading.", 1},
		{"words around operator", "// value is other.", 1},
		{"hyphenated words", "// lower-case prose.", 1},
		{"double hyphenated words", "// lower--case prose.", 1},
		{"input/output prose", "// input/output files are available.", 1},
		{"read/write prose", "// read/write access is available.", 1},
		{"read&write prose", "// read&write access is available.", 1},
		{"spaced division prose", "// input / output modes are available.", 1},
		{"spaced bitwise prose", "// read & write access is available.", 1},
		{"spaced subtraction prose", "// lower - case spelling is rejected.", 1},
		{"spaced comparison prose", "// lower < upper bounds are rejected.", 1},
		{"spaced division uppercase", "// Input / Output modes are available.", 0},
		{"bracket prose is not endpoint", "// [lower prose]:80.", 1},
		{"quoted operator mention", "// lower prose mentions `:=`.", 1},
		{"quoted arrow mention", "// lower prose mentions `->`.", 1},
		{"quoted fat arrow mention", "// lower prose mentions `=>`.", 1},
		{"smart quoted arrow mention", "// lower prose mentions “->”.", 1},
		{"ipv4/ipv6 prose", "// ipv4/ipv6 traffic is supported.", 1},
		{"ipv4-only prose", "// ipv4-only traffic is supported.", 1},
		{"layer-2 prose", "// layer-2 traffic is supported.", 1},
		{"parenthesized prose", "// lower case (see below).", 1},
		{"loose parenthesized prose", "// note (sic).", 1},
		{"leading quote prose", "// \"lower values\" are rejected.", 1},
		{"leading parenthesis prose", "// (lower values) are rejected.", 1},
		{"inline backtick prose", "// lower values use `foo` here.", 1},
		{"loose index prose", "// items [index].", 1},
		{"slice prose", "// values in range [1:2].", 1},
		{"loose composite prose", "// value {field: 1}.", 1},
		{"ordinary colon prose", "// lower values: see below.", 1},
		{"short colon prose", "// lower: prose.", 1},
		{"tight colon prose", "// lower:values.", 1},
		{"ordinary todo word", "// todolist entries are checked.", 1},
		{"abbreviation eg prose", "// lower values, e.g. defaults, are rejected.", 1},
		{"abbreviation ie prose", "// lower values, i.e. defaults, are rejected.", 1},
		{"semicolon prose", "// funcA allows every matching UDP IPv4 packet; funcB then drops them all.", 1},
		{"defer prose", "// defer to later.", 1},
		{"go prose after keyword", "// go figure.", 1},
		{"labeled call prose", "// label: lower prose.", 1},
		{"arrow prose suffix", "// input -> output modes are supported.", 1},
		{"fat arrow prose suffix", "// input => output modes are supported.", 1},
		{"pointer arrow prose suffix", "// ptr->field values are copied.", 1},
		{"direct unary pointer arrow", "// *node->next.", 0},
		{"direct unary address arrow", "// &node->next.", 0},
		{"direct unary not arrow", "// !node->ready.", 0},
		{"direct unary complement arrow", "// ~node->flags.", 0},
		{"prefix increment arrow", "// ++node->next.", 0},
		{"prefix decrement arrow", "// --node->next.", 0},
		{"postfix increment arrow", "// node->next++.", 0},
		{"postfix decrement arrow", "// node->next--.", 0},
		{"sizeof arrow", "// sizeof node->next.", 0},
		{"direct unary arrow prose suffix", "// !node->ready values are checked.", 1},
		{"direct complement arrow prose suffix", "// ~node->flags values are checked.", 1},
		{"sizeof prose", "// sizeof values are measured.", 1},
		{"arrow chain prose suffix", "// input -> filter -> output modes are supported.", 1},
		{"indexed C arrow chain", "// nodes[index]->next.", 0},
		{"parenthesized pointer C arrow", "// (*node)->next.", 0},
		{"parenthesized C arrow", "// (node)->next.", 0},
		{"parenthesized selector C arrow", "// (node.field)->next.", 0},
		{"parenthesized call C arrow", "// (getNode())->next.", 0},
		{"parenthesized index C arrow", "// (nodes[0])->next.", 0},
		{"parenthesized struct cast C arrow", "// (struct node *)ptr->next.", 0},
		{"parenthesized const struct cast C arrow", "// (const struct node *)ptr->next.", 0},
		{"parenthesized named cast C arrow", "// (node_t *)ptr->next.", 0},
		{"parenthesized void cast C arrow", "// (void *)ptr->next.", 0},
		{"parenthesized volatile struct cast C arrow", "// (volatile struct node *)ptr->next.", 0},
		{"parenthesized volatile const cast C arrow", "// (volatile const node_t *)ptr->next.", 0},
		{"parenthesized unsigned long cast C arrow", "// (unsigned long *)ptr->next.", 0},
		{"parenthesized pointer qualified cast C arrow", "// (node_t * const)ptr->next.", 0},
		{"parenthesized typedef cast C arrow", "// (node_t)ptr->next.", 0},
		{"parenthesized binary C arrow", "// (ptr + 1)->next.", 0},
		{"parenthesized conditional C arrow", "// (cond ? left : right)->next.", 0},
		{"parenthesized comma C arrow", "// (a, b)->next.", 0},
		{"parenthesized function pointer cast C arrow", "// (node_t (*)(void))ptr->next.", 0},
		{"parenthesized array pointer cast C arrow", "// (node_t (*)[4])ptr->next.", 0},
		{"opaque words C cast shape", "// (node_t prose_word)ptr->next.", 0},
		{"opaque void C cast shape", "// (void int)ptr->next.", 0},
		{"opaque unsigned C cast shape", "// (unsigned float)ptr->next.", 0},
		{"empty true conditional remains prose", "// (cond ? : right)->next.", 1},
		{"missing false conditional remains prose", "// (cond ? left)->next.", 1},
		{"incomplete binary C arrow remains prose", "// (ptr +)->next.", 1},
		{"C cast words remain prose", "// lower (node_t *) prose.", 1},
		{"prose C arrow prefix remains prose", "// lower (ptr prose)->next.", 1},
		{"Unicode arrow diagram", "// input → output.", 0},
		{"Unicode heavy arrow diagram", "// input ➔ output.", 0},
		{"Unicode dingbat arrow diagram", "// input ➡ output.", 0},
		{"Unicode supplemental arrow diagram", "// input ⬀ output.", 0},
		{"Unicode dingbat right arrow diagram", "// input ⮕ output.", 0},
		{"Unicode open arrow diagram", "// input ⇿ output.", 0},
		{"Unicode supplemental arrows C diagram", "// input 🡆 output.", 0},
		{"Unicode left arrow diagram", "// input ← output.", 0},
		{"Unicode bidirectional arrow diagram", "// input ↔ output.", 0},
		{"Unicode star is prose", "// lower values ⭐ higher values are mapped.", 1},
		{"Unicode half-black symbol is prose", "// lower values ⬒ higher values are mapped.", 1},
		{"Unicode arrow prose suffix", "// lower input → output modes.", 1},
		{"Unicode arrow prose words", "// lower values ← higher values are mapped.", 1},
		{"underscored literal assignment prefix", "// layer_index=99 should fail.", 1},
		{"bare hostname then lower prose", "// example.com lower prose follows.", 1},
		{"bare hostname then upper prose", "// example.com Upper prose follows.", 0},
		{"empty email host is prose", "// lower@.", 1},
		{"localhost email host is URL", "// lower@localhost.", 0},
		{"MAC then lower prose", "// 00:11:22:33:44:55 lower prose follows.", 1},
		{"MAC then upper prose", "// 00:11:22:33:44:55 Upper prose follows.", 0},
		{"malformed MAC then lower prose", "// 00:11:22:33:44:5x. lower prose follows.", 1},
		{"invalid Markdown inline destination", "// [lower prose](choose this path).", 1},
		{"invalid Markdown nested parenthesized title", "// [lower prose](guide (Title (nested))).", 1},
		{"invalid Markdown angle destination", "// [lower prose](<a>b>).", 1},
		{"Markdown at destination", "// [lower label](@).", 0},
		{"empty go directive name", "//go: lower prose.", 1},
		{"empty lint directive name", "//lint: lower prose.", 1},
		{"punctuation go directive name", "//go:? lower prose.", 1},
		{"punctuation lint directive name", "//lint:- lower prose.", 1},
		{"uppercase go directive name", "//go:Xx lower prose.", 1},
		{"escaped backtick prose", "// \\`lower\\`.", 1},
		{"escaped backtick uppercase", "// \\`Upper\\`.", 0},
		{"invalid rune word", "// 'lower'.", 1},
		{"invalid rune question word", "// 'lower?'.", 1},
		{"invalid image definition", "// ![lower prose]: diagram.svg.", 1},
		{"go prose", "// go figure.", 1},
		{"break prose", "// break here.", 1},
		{"continue prose", "// continue reading.", 1},
		{"return prose", "// return to sender.", 1},
		{"labeled branch prose", "// label: break.", 1},
		{"input/output sentence", "// input/output.", 1},
		{"read/write sentence", "// read/write.", 1},
		{"read&write sentence", "// read&write.", 1},
		{"hyphenated sentence", "// lower-case.", 1},
		{"ipv4/ipv6 sentence", "// ipv4/ipv6.", 1},
		{"ipv4-only sentence", "// ipv4-only.", 1},
		{"layer-2 sentence", "// layer-2.", 1},
		{"leading punctuation chain", "// (\"lower values\") are rejected.", 1},
		{"nested leading punctuation", "// ((lower values)) are rejected.", 1},
		{"single quote prose", "// 'lower values' are rejected.", 1},
		{"smart quote prose", "// “lower values” are rejected.", 1},
		{"smart quote terminal", "// “lower prose.”", 1},
		{"smart quote punctuation prose", "// “lower values?” are rejected.", 1},
		{"markdown emphasis prose", "// **lower values** are rejected.", 1},
		{"markdown emphasis terminal", "// **lower prose.**", 1},
		{"strikethrough terminal", "// ~~lower prose.~~", 1},
		{"strikethrough uppercase terminal", "// ~~Upper prose.~~", 0},
		{"markdown single emphasis prose", "// *lower values* are rejected.", 1},
		{"parenthesis terminal", "// (lower prose.)", 1},
		{"inline code punctuation prose", "// lower values use `foo?` here.", 1},
		{"first prose sentence wins", "// Upper opening. lower later.", 0},
		{"abbreviation at opening", "// e.g. lower values are rejected.", 1},
		{"abbreviation at opening upper", "// i.e. lower values are rejected.", 1},
		{"selector abbreviation eg", "// object.e.g. lower prose follows.", 1},
		{"selector abbreviation ie", "// object.i.e. lower prose follows.", 1},
		{"selector abbreviation eg then upper", "// object.e.g. Upper prose follows.", 0},
		{"selector abbreviation ie then upper", "// object.i.e. Upper prose follows.", 0},
		{"numbered heading then prose", "// 123 heading. lower prose follows.", 1},
		{"ordered list prose", "// 1) lower prose follows.", 1},
		{"ordered dot list prose", "// 1. lower prose follows.", 1},
		{"diagram then prose", "// +---+. lower prose follows.", 1},
		{"heading then prose", "// # Heading\n// lower prose follows.", 1},
		{"code line then prose", "// value := other\n// lower prose follows.", 1},
		{"inline pointer continuation", "// FromC converts a *C.value and\n// releases it.", 0},
		{"inline arrow continuation", "// dp_worker->device_id feeds arrays that\n// remain valid.", 1},
		{"diagram line then prose", "// +---+\n// lower prose follows.", 1},
		{"punctuation fragment then prose", "// ... lower prose follows.", 1},
		{"punctuation fragment then upper prose", "// ... Upper prose follows.", 0},
		{"selector code then upper prose", "// a.b. Upper prose follows.", 0},
		{"call code then lower prose", "// parse(value). lower prose follows.", 1},
		{"backtick code then lower prose", "// `code`. lower prose follows.", 1},
		{"backtick span then prose", "// `field` stores the value.", 1},
		{"assignment code then lower prose", "// value = other. lower prose follows.", 1},
		{"URL then lower prose", "// https://example.test. lower prose follows.", 1},
		{"URL line then lower prose", "// https://example.test\n// lower prose follows.", 1},
		{"URL prefix then lower prose", "// https://example.test lower prose follows.", 1},
		{"URL prefix then uppercase prose", "// https://example.test Upper prose follows.", 0},
		{"relative current URL then lower prose", "// ./v1/items lower prose follows.", 1},
		{"relative current URL then upper prose", "// ./v1/items Upper prose follows.", 0},
		{"relative parent URL then lower prose", "// ../guide lower prose follows.", 1},
		{"relative parent URL then upper prose", "// ../guide Upper prose follows.", 0},
		{"bracketed IPv6 path then lower prose", "// [::1]/v1/items lower prose follows.", 1},
		{"bracketed IPv6 path then upper prose", "// [::1]/v1/items Upper prose follows.", 0},
		{"IPv4 prefix then lower prose", "// 192.0.2.1 lower prose follows.", 1},
		{"IPv4 prefix then uppercase prose", "// 192.0.2.1 Upper prose follows.", 0},
		{"IPv6 prefix then lower prose", "// [::1] lower prose follows.", 1},
		{"IPv6 prefix then uppercase prose", "// [::1] Upper prose follows.", 0},
		{"IPv4 CIDR prefix then lower prose", "// 192.0.2.0/24 lower prose follows.", 1},
		{"IPv4 CIDR prefix then upper prose", "// 192.0.2.0/24 Upper prose follows.", 0},
		{"IPv6 CIDR prefix then lower prose", "// 2001:db8::/32 lower prose follows.", 1},
		{"IPv6 CIDR prefix then upper prose", "// 2001:db8::/32 Upper prose follows.", 0},
		{"URL call then lower prose", "// https://example.test parse(value). lower prose follows.", 1},
		{"URL call then uppercase prose", "// https://example.test parse(value). Upper prose follows.", 0},
		{"URL parentheses then lower prose", "// https://example.test/path(foo) lower prose follows.", 1},
		{"URL parentheses then upper prose", "// https://example.test/path(foo) Upper prose follows.", 0},
		{"URL internal parentheses then lower prose", "// https://example.test/a(b)c lower prose follows.", 1},
		{"URL internal parentheses then upper prose", "// https://example.test/a(b)c Upper prose follows.", 0},
		{"angle URL", "// <https://example.test>.", 0},
		{"parenthesized URL", "// (https://example.test).", 0},
		{"nested wrapped URL", "// (<https://example.test>).", 0},
		{"smart quoted URL", "// “https://example.test”.", 0},
		{"domain path URL", "// example.com/path.", 0},
		{"domain path then lower prose", "// example.com/path lower prose follows.", 1},
		{"domain path then upper prose", "// example.com/path Upper prose follows.", 0},
		{"root relative path then lower prose", "// /v1/items lower prose follows.", 1},
		{"root relative path then upper prose", "// /v1/items Upper prose follows.", 0},
		{"hostname root then lower prose", "// example.com/ lower prose follows.", 1},
		{"hostname root then upper prose", "// example.com/ Upper prose follows.", 0},
		{"hostname port path then lower prose", "// example.com:8080/path lower prose follows.", 1},
		{"hostname port path then upper prose", "// example.com:8080/path Upper prose follows.", 0},
		{"hostname query then lower prose", "// example.com?query=1 lower prose follows.", 1},
		{"hostname query then upper prose", "// example.com?query=1 Upper prose follows.", 0},
		{"hostname anchor then lower prose", "// example.com#anchor lower prose follows.", 1},
		{"hostname anchor then upper prose", "// example.com#anchor Upper prose follows.", 0},
		{"invalid domain path prose", "// lower-case/prose.", 1},
		{"uppercase WWW URL", "// WWW.EXAMPLE.COM/path.", 0},
		{"raw IPv6 address", "// [::1].", 0},
		{"TODO line then lower prose", "// TODO(owner): pending\n// lower prose follows.", 1},
		{"directive line then lower prose", "//go:generate tool\n// lower prose follows.", 1},
		{"spaced go prefix is prose", "// go: choose the lower path.", 1},
		{"spaced lint prefix is prose", "// lint: choose the lower path.", 1},
		{"Markdown reference then lower prose", "// [docs]: https://example.test\n// lower prose follows.", 1},
		{"Markdown reference with title then lower prose", "// [docs]: <https://example.test> \"title\"\n// lower prose follows.", 1},
		{"malformed Markdown reference prose", "// [lower prose]: choose this path.", 1},
		{"call code then upper prose", "// parse(value). Upper prose follows.", 0},
		{"assignment code then upper prose", "// value = other. Upper prose follows.", 0},
		{"operator after prose", "// lower prose describes x + y.", 1},
		{"assignment after prose", "// lower prose: value := other.", 1},
		{"semicolon assignment uppercase suffix", "// value = other; Upper prose follows.", 0},
		{"semicolon assignment lowercase suffix", "// value = other; lower prose follows.", 1},
		{"semicolon call uppercase suffix", "// parse(value); Upper prose follows.", 0},
		{"semicolon call lowercase suffix", "// parse(value); lower prose follows.", 1},
		{"identifier prose with buffers", "// inputValue and outputValue need 2 buffers.", 1},
		{"identifier prose with buffers uppercase", "// InputValue and OutputValue need 2 buffers.", 0},
		{"identifier prose with counts", "// packetCount and byteCount stay below 10.", 1},
		{"identifier prose with counts uppercase", "// PacketCount and ByteCount stay below 10.", 0},
		{"call code then capitalized prose", "// parse(value). Lower prose follows.", 0},
		{"multi-statement return and break", "// return value; break.", 0},
		{"default clause code", "// default: run().", 0},
		{"default clause with whitespace", "// default : run().", 0},
		{"case clause with tab", "// case\tready: run().", 0},
		{"default colon prose", "// default: lower prose.", 1},
		{"select case prose", "// case lower prose.", 1},
		{"goto statement", "// goto retry.", 0},
		{"labeled goto statement", "// label: goto retry.", 0},
		{"labeled return statement", "// label: return value.", 0},
		{"fallthrough statement", "// fallthrough.", 0},
		{"struct field snippet", "// field int.", 0},
		{"predeclared true", "// true.", 0},
		{"predeclared false", "// false.", 0},
		{"predeclared nil", "// nil.", 0},
		{"emphasis-shaped multiplication code", "// *pointer * factor.", 0},
		{"prose exclamation", "// lower! More detail.", 1},
		{"leading backtick line then prose", "// `field`\n// lower prose follows.", 1},
		{"setext heading then prose", "// lower heading\n// ========\n// lower prose follows.", 1},
		{"backtick fence then prose", "// ```\n// lower code\n// ```\n// lower prose follows.", 1},
		{"indented backticks do not fence", "//     ```\n// lower prose follows.", 1},
		{"tab-indented backticks do not fence", "//\t```\n// lower prose follows.", 1},
		{"decorated block fence then prose", "/*\n * ```\n * lower code.\n * ```\n * lower prose follows.\n */", 1},
		{"tilde fence then prose", "// ~~~\n// lower code\n// ~~~\n// lower prose follows.", 1},
		{"tilde fence info run then prose", "// ~~~~ info ~~~~\n// lower code\n// ~~~~\n// lower prose follows.", 1},
		{"tilde fence info run without close", "// ~~~~ info ~~~~\n// lower code\n// lower prose follows.", 0},
		{"Unicode diagram then prose", "// ┌──┐\n// └──┘\n// lower prose follows.", 1},
		{"URL line then prose", "// https://example.test\n// lower prose follows.", 1},
		{"standalone link then prose", "// [lower label](https://example.test)\n// lower prose follows.", 1},
		{"inline backtick fence span then prose", "// ```code``` lower prose follows.", 1},
		{"backtick in fence info then prose", "// ```go`x\n// lower prose follows.\n// ```", 1},
		{"four-open three-close fence", "// ````\n// lower code\n// ```\n// lower prose follows.", 0},
		{"four-open four-close fence", "// ````\n// lower code\n// ````\n// lower prose follows.", 1},
		{"fence blank payload then lower prose", "// ```\n// code\n//\n// more code\n// ```\n// lower prose follows.", 1},
		{"fence blank payload then upper prose", "// ```\n// code\n//\n// more code\n// ```\n// Upper prose follows.", 0},
		{"mismatched fence glyph", "// ```\n// lower code\n// ~~~\n// lower prose follows.", 0},
		{"one-character Setext underline", "// lower heading\n// -\n// lower prose follows.", 1},
		{"four-column Setext underline is code", "// lower prose.\n//    ---", 1},
		{"three-column Setext underline is heading", "// lower prose.\n//   ---", 0},
		{"multiline composite code then prose", "// Config{\n// Field: value,\n// }\n// lower prose follows.", 1},
		{"multiline call then lower prose", "// parse(\n// value,\n// )\n// lower prose follows.", 1},
		{"multiline call then upper prose", "// parse(\n// value,\n// )\n// Upper prose follows.", 0},
		{"multiline assignment then lower prose", "// value :=\n// other\n// lower prose follows.", 1},
		{"multiline assignment then upper prose", "// value :=\n// other\n// Upper prose follows.", 0},
		{"multiline call same-line lower remainder", "// parse(\n// value,\n// ) lower prose follows.", 1},
		{"multiline call same-line upper remainder", "// parse(\n// value,\n// ) Upper prose follows.", 0},
		{"multiline assignment same-line lower remainder", "// value :=\n// other; lower prose follows.", 1},
		{"multiline assignment same-line upper remainder", "// value :=\n// other; Upper prose follows.", 0},
		{"multiline chained call then lower prose", "// foo(\n// value).bar().\n// lower prose follows.", 1},
		{"multiline chained call then upper prose", "// foo(\n// value).bar().\n// Upper prose follows.", 0},
		{"multiline selector chain", "// parse(\n// value).\n// lower.", 0},
		{"multiline selector chain uppercase", "// parse(\n// value).\n// Upper.", 0},
		{"multiline selector chain prose", "// parse(\n// value).\n// lower prose follows.", 1},
		{"multiline call statement list", "// foo(\n// value); bar().", 0},
		{"ordinary prose continuation", "// lower opening\n// continuation words.", 1},
		{"else block statement", "// else { run() }.", 0},
		{"else prose", "// else lower prose.", 1},
		{"lower named member ambiguity", "// field customType.", 1},
		{"ambiguous method prose", "// method handles value.", 1},
		{"ambiguous two-word prose", "// lower values.", 1},
		{"dotted numeric then lower prose", "// 1.2. lower prose follows.", 1},
		{"dotted numeric then upper prose", "// 1.2. Upper prose follows.", 0},
		{"strikethrough prose", "// ~~lower prose~~.", 1},
		{"strikethrough uppercase prose", "// ~~Upper prose~~.", 0},
		{"malformed bracket prose", "// [lower [nested] prose.", 1},
		{"labeled continue prose", "// label: continue.", 1},
		{"symbol diagram sentence then prose", "// ┌──┐. lower prose follows.", 1},
		{"ASCII string then uppercase prose", "// \"lower?\" Upper prose follows.", 0},
		{"ASCII string then lowercase prose", "// \"lower?\" lower prose follows.", 1},
		{"ASCII rune then uppercase prose", "// 'lower?' Upper prose follows.", 1},
		{"ASCII raw string then uppercase prose", "// `lower?` Upper prose follows.", 0},
		{"unmatched leading backtick", "// `lower prose follows.", 1},
		{"cgo export boundary prose", "//exported name.", 1},
		{"spaced export prose", "// export name.", 1},
		{"Unicode doc comment", "/// lower prose.", 1},
		{"Unicode bang doc comment", "//! lower prose.", 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := "package p\n\nfunc body() {\n\t" + test.comment + "\n}\n"
			findings, err := goCommentFindings("fixture.go", []byte(source))
			require.NoError(t, err)
			if len(findings) != test.findings {
				t.Fatalf("expected %d findings, got %#v", test.findings, findings)
			}
		})
	}
}

// TestGoCommentCapitalizationArrowShapes verifies structural arrow boundaries.
func TestGoCommentCapitalizationArrowShapes(t *testing.T) {
	tests := []struct {
		name     string
		comment  string
		findings int
	}{
		{"opaque grouped words", "// (node_t prose_word)ptr->next.", 0},
		{"opaque grouped specifiers", "// (void int)ptr->next.", 0},
		{"opaque grouped pointer", "// (static *)ptr->next.", 0},
		{"grouped call", "// (call(lower prose))->next.", 0},
		{"grouped assignment", "// (1 = lower)->next.", 0},
		{"grouped repeated increment", "// (value++++)->next.", 0},
		{"grouped compound assignment", "// ((value += other))->next.", 0},
		{"grouped numeric suffix", "// (1UL)->next.", 0},
		{"grouped compound literal", "// ((struct pair){left: right})->next.", 0},
		{"nested arrow operand", "// ((left -> right))->next.", 0},
		{"nested arrow chain", "// ptr->field->next.", 0},
		{"nested arrow group", "// (left->right).", 0},
		{"nested arrow call", "// call(left->right).", 0},
		{"grouped address arrow", "// (&node)->next.", 0},
		{"grouped not arrow", "// (!node)->next.", 0},
		{"grouped complement arrow", "// (~node)->next.", 0},
		{"grouped subtraction arrow", "// (-node)->next.", 0},
		{"cast unary arrow", "// (node_t *)*ptr->next.", 0},
		{"spaced cast unary arrow", "// (node_t *) *ptr->next.", 0},
		{"arrow assignment right", "// ptr->field = value.", 0},
		{"arrow assignment left", "// result = ptr->field.", 0},
		{"arrow arithmetic context", "// ptr->field + offset.", 0},
		{"arrow comma context", "// ptr->field, other->next.", 0},
		{"arrow conditional context", "// cond ? ptr->field : other->next.", 0},
		{"arrow logical context", "// ptr && ptr->field.", 0},
		{"arrow comparison context", "// ptr->field == value.", 0},
		{"arrow add assignment context", "// ptr->field += value.", 0},
		{"assignment add arrow context", "// result += ptr->field.", 0},
		{"arrow shift assignment context", "// ptr->field <<= shift.", 0},
		{"receive arrow context", "// ch <- ptr->field.", 0},
		{"ellipsis sequence", "// ptr->field ... output.", 0},
		{"receive prefix", "// <-ch -> output.", 0},
		{"receive arrow right operand", "// ptr-> <-ch.", 0},
		{"nested conditional arrow context", "// cond1 ? cond2 ? ptr->field : other : fallback.", 0},
		{"square wrappers", "// [input] -> [output].", 0},
		{"angle wrappers", "// <input> -> <output>.", 0},
		{"angle nested arrow", "// <left->right>.", 0},
		{"angle nested arrow context", "// <left->right> -> output.", 0},
		{"shift angle wrappers", "// <<input>> -> output.", 0},
		{"triple angle wrappers", "// <<<input>>> -> output.", 0},
		{"quadruple angle wrappers", "// <<<<input>>>> -> output.", 0},
		{"angle comparison content", "// <fn(value > limit)> -> output.", 0},
		{"angle shift content", "// <<value >> shift>> -> output.", 0},
		{"angle fat arrow", "// <left=>right> => output.", 0},
		{"angle asymmetric run", "// <<<input>> -> output.", 1},
		{"angle opening direction", "// <input< -> output.", 1},
		{"angle opening direction run", "// <<<input<<< -> output.", 1},
		{"angle empty direction", "// <>input<> -> output.", 1},
		{"spaced call suffix", "// call (arg)->field.", 0},
		{"spaced cast suffix", "// (node_t *) (ptr)->next.", 0},
		{"spaced group prose", "// lower (ptr prose)->next.", 1},
		{"empty call suffix", "// call()->field.", 0},
		{"empty spaced call suffix", "// call ()->field.", 0},
		{"empty call after operator", "// ptr->field + run().", 0},
		{"empty composite suffix", "// (struct node){}->next.", 0},
		{"empty standalone group", "// () -> output.", 1},
		{"empty standalone composite", "// {} -> output.", 1},
		{"empty standalone bracket", "// [] -> output.", 1},
		{"slice omitted upper", "// items[:]->next.", 0},
		{"slice omitted final bound", "// items[lo:]->next.", 0},
		{"slice omitted lower bound", "// items[:hi]->next.", 0},
		{"slice omitted lower and full max", "// items[:hi:max]->next.", 0},
		{"slice full", "// items[lo:hi:max]->next.", 0},
		{"slice missing middle bound", "// items[lo::max]->next.", 1},
		{"slice missing final bound", "// items[lo:hi:]->next.", 1},
		{"slice prose", "// items[lower prose: values copied]->next.", 1},
		{"type assertion suffix", "// value.(Type)->next.", 0},
		{"pointer type assertion suffix", "// value.(*Type)->next.", 0},
		{"interface type assertion suffix", "// value.(interface{})->next.", 0},
		{"slice type assertion suffix", "// value.([]T)->next.", 0},
		{"map type assertion suffix", "// value.(map[string]T)->next.", 0},
		{"channel type assertion suffix", "// value.(chan T)->next.", 0},
		{"function type assertion suffix", "// value.(func() T)->next.", 0},
		{"struct type assertion suffix", "// value.(struct{})->next.", 0},
		{"type keyword assertion suffix", "// value.(type)->next.", 0},
		{"qualified type assertion suffix", "// value.(pkg.Type)->next.", 0},
		{"generic type assertion suffix", "// value.(pkg.Type[T])->next.", 0},
		{"prose type assertion suffix", "// value.(lower prose)->next.", 1},
		{"empty type assertion", "// value.()->next.", 1},
		{"invalid type assertion", "// value.(*)->next.", 1},
		{"map literal arrow operand", "// map[string]int{}->next.", 0},
		{"struct literal arrow operand", "// struct{}{}->next.", 0},
		{"function literal arrow operand", "// func(){} -> output.", 0},
		{"map type arrow operand", "// map[string]int -> output.", 0},
		{"channel type arrow operand", "// chan T -> output.", 0},
		{"function type arrow operand", "// func(T) U -> output.", 0},
		{"struct type arrow operand", "// struct{field T} -> output.", 0},
		{"slice literal arrow operand", "// []T{} -> output.", 0},
		{"binary grouped operand", "// (ptr + 1)->next.", 0},
		{"conditional grouped operand", "// (cond ? left : right)->next.", 0},
		{"comma grouped operand", "// (a, b)->next.", 0},
		{"prose arrow suffix", "// ptr->field values are copied.", 1},
		{"prose arrow prefix", "// lower prose before (ptr + 1)->next.", 1},
		{"prose arrow mention", "// lower prose mentions ->.", 1},
		{"incomplete arrow", "// input ->.", 1},
		{"incomplete compound assignment", "// ptr->field +=.", 1},
		{"unmatched grouped operand", "// (ptr + 1->next.", 1},
		{"incomplete binary group", "// (ptr +)->next.", 1},
		{"incomplete conditional group", "// (cond ? left)->next.", 1},
		{"nested incomplete arrow", "// (left->).", 1},
		{"nested arrow prose suffix", "// (left->right values).", 1},
		{"comma arrow prose suffix", "// ptr->field, values are copied.", 1},
		{"square arrow prose suffix", "// [input] -> [output] modes are supported.", 1},
		{"nested conditional missing outer colon", "// cond1 ? cond2 ? ptr->field : other.", 1},
		{"spaced parenthesized prose prefix", "// (lower prose) continues->output.", 1},
		{"spaced bracket prose prefix", "// [lower label] continues->output.", 1},
		{"tight parenthesized shape", "// (lower prose)continues->output.", 0},
		{"tight bracket shape", "// [lower label]continues->output.", 0},
		{"quoted arrow mention", "// lower prose mentions `->`.", 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := "package p\n\nfunc body() {\n\t" + test.comment + "\n}\n"
			findings, err := goCommentFindings("fixture.go", []byte(source))
			require.NoError(t, err)
			require.Len(t, findings, test.findings)
		})
	}
}

// TestGoCommentCapitalizationSpacedCalls verifies complete spaced calls.
func TestGoCommentCapitalizationSpacedCalls(t *testing.T) {
	tests := []struct {
		name     string
		comment  string
		findings int
	}{
		{"builtin call", "// append (items, value).", 0},
		{"integer conversion", "// int (value).", 0},
		{"slice conversion", "// []byte (value).", 0},
		{"selector call", "// slices.Append (items, value).", 0},
		{"generic call", "// append[T] (value).", 0},
		{"parenthesized callee", "// (append) (items, value).", 0},
		{"parenthesized helper callee", "// (helper) (value).", 0},
		{"factory call callee", "// factory () (value).", 0},
		{"function literal callee", "// func (int) { } (value).", 0},
		{"empty call", "// append ().", 0},
		{"empty helper call", "// helper ().", 0},
		{"ambiguous builtin copy", "// copy (carefully).", 1},
		{"ambiguous builtin close", "// close (carefully).", 1},
		{"multiple helper arguments", "// helper (first, second).", 0},
		{"binary helper argument", "// helper (value + other).", 0},
		{"variadic helper argument", "// helper (values...).", 0},
		{"integer helper argument", "// helper (42).", 0},
		{"string helper argument", "// helper (\"value\").", 0},
		{"boolean helper argument", "// helper (true).", 0},
		{"trailing comma helper argument", "// helper (value,).", 0},
		{"blank helper argument", "// helper (_).", 0},
		{"iota helper argument", "// helper (iota).", 0},
		{"channel conversion", "// chan int (value).", 0},
		{"function conversion", "// func() int (value).", 0},
		{"struct conversion", "// struct{} (value).", 0},
		{"interface conversion", "// interface{} (value).", 0},
		{"prose parenthesis", "// lower prose (see below).", 1},
		{"ambiguous helper call", "// helper (value).", 1},
		{"ambiguous lower call", "// lower (values).", 1},
		{"call prose suffix", "// append (items, value) copies data.", 1},
		{"incomplete call", "// append (items, value.", 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := "package p\n\nfunc body() {\n\t" + test.comment + "\n}\n"
			findings, err := goCommentFindings("fixture.go", []byte(source))
			require.NoError(t, err)
			require.Len(t, findings, test.findings)
		})
	}
}

// TestGoCommentMarkdownDestinationGrammar verifies angle destination boundaries.
func TestGoCommentMarkdownDestinationGrammar(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		valid bool
	}{
		{name: "angle spaces", text: "<guide start>", valid: true},
		{name: "angle escaped space", text: "<guide\\ start>", valid: true},
		{name: "angle newline", text: "<guide\nstart>", valid: false},
		{name: "angle escaped newline", text: "<guide\\\nstart>", valid: false},
		{name: "angle internal close", text: "<a)b>", valid: true},
		{name: "bare balanced parentheses", text: "guide(foo)", valid: true},
		{name: "bare escaped space", text: "guide\\ start", valid: false},
		{name: "bare escaped punctuation", text: "guide\\(start", valid: true},
		{name: "bare unescaped space", text: "guide start", valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.valid, goCommentLooksLikeMarkdownDestination(test.text))
		})
	}
}

// TestGoCommentCapitalizationReportsSentenceLine verifies line attribution.
func TestGoCommentCapitalizationReportsSentenceLine(t *testing.T) {
	source := "package p\n\nfunc body() {\n\t/* parse(value).\n\t   lower prose follows.\n\t*/\n}\n"
	findings, err := goCommentFindings("fixture.go", []byte(source))
	require.NoError(t, err)
	if len(findings) != 1 || findings[0].Line != 5 {
		t.Fatalf("expected line 5 finding, got %#v", findings)
	}
}

// TestGoCommentCapitalizationPreservesPhysicalLines verifies physical lines.
func TestGoCommentCapitalizationPreservesPhysicalLines(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		expected int
	}{
		{
			name:     "block comment after blank line",
			source:   "package p\n\nfunc body() {\n\t/*\n\n\t lower block prose.\n\t*/\n}\n",
			expected: 6,
		},
		{
			name:     "line comment after blank payload",
			source:   "package p\n\nfunc body() {\n\t//\n\t// lower line prose.\n}\n",
			expected: 5,
		},
		{
			name:     "leading inline code on prior line",
			source:   "package p\n\nfunc body() {\n\t// `field`\n\t// lower line prose.\n}\n",
			expected: 5,
		},
		{
			name:     "URL on prior line",
			source:   "package p\n\nfunc body() {\n\t// https://example.test\n\t// lower line prose.\n}\n",
			expected: 5,
		},
		{
			name:     "indented URL continuation remains prose",
			source:   "package p\nfunc body() {\n\t// https://example.test\n\t//     lower prose.\n}\n",
			expected: 4,
		},
		{
			name:     "indented punctuation continuation remains prose",
			source:   "package p\nfunc body() {\n\t// +---+.\n\t//     lower prose.\n}\n",
			expected: 4,
		},
		{
			name:     "setext heading on prior lines",
			source:   "package p\n\nfunc body() {\n\t// lower heading\n\t// ========\n\t// lower line prose.\n}\n",
			expected: 6,
		},
		{
			name:     "multiline composite code on prior lines",
			source:   "package p\n\nfunc body() {\n\t// Config{\n\t// Field: value,\n\t// }\n\t// lower line prose.\n}\n",
			expected: 7,
		},
		{
			name:     "multiline call code on prior lines",
			source:   "package p\n\nfunc body() {\n\t// parse(\n\t// value,\n\t// )\n\t// lower line prose.\n}\n",
			expected: 7,
		},
		{
			name:     "multiline assignment code on prior lines",
			source:   "package p\n\nfunc body() {\n\t// value :=\n\t// other\n\t// lower line prose.\n}\n",
			expected: 6,
		},
		{
			name:     "multiline call keeps closing-line remainder",
			source:   "package p\n\nfunc body() {\n\t// parse(\n\t// value,\n\t// ) lower line prose.\n}\n",
			expected: 6,
		},
		{
			name:     "multiline assignment keeps same-line remainder",
			source:   "package p\n\nfunc body() {\n\t// value :=\n\t// other; lower line prose.\n}\n",
			expected: 5,
		},
		{
			name:     "angle destination cannot cross a line",
			source:   "package p\nfunc body() {\n// [lower label](<guide\n// start>).\n}\n",
			expected: 3,
		},
		{
			name:     "escaped angle destination cannot cross a line",
			source:   "package p\nfunc body() {\n// [lower label](<guide\\\n// start>).\n}\n",
			expected: 3,
		},
		{
			name:     "reference angle destination cannot cross a line",
			source:   "package p\nfunc body() {\n// [lower]: <guide\n// start>.\n}\n",
			expected: 3,
		},
		{
			name:     "spaced inline angle destination cannot cross a line",
			source:   "package p\nfunc body() {\n// [lower]( <guide\n// start>).\n}\n",
			expected: 3,
		},
		{
			name:     "nested angle destination cannot cross a line",
			source:   "package p\nfunc body() {\n// [lower [nested]](<guide\n// start>).\n}\n",
			expected: 3,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			findings, err := goCommentFindings("fixture.go", []byte(test.source))
			require.NoError(t, err)
			require.Len(t, findings, 1)
			require.Equal(t, test.expected, findings[0].Line)
		})
	}
}

// TestGoCommentMarkdownLinkSoftbreaks verifies label and title softbreaks.
func TestGoCommentMarkdownLinkSoftbreaks(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name:   "label softbreak",
			source: "package p\nfunc body() {\n// [lower <label\n// continues>](guide).\n}\n",
		},
		{
			name:   "quoted title softbreak",
			source: "package p\nfunc body() {\n// [lower](guide \"title <part\n// continues>\").\n}\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			findings, err := goCommentFindings("fixture.go", []byte(test.source))
			require.NoError(t, err)
			require.Empty(t, findings)
		})
	}
}

// TestGoCommentCapitalizationCgoPreambles verifies cgo preambles are excluded.
func TestGoCommentCapitalizationCgoPreambles(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{"single import declaration", "package p\n\n/*\nlower cgo preamble.\n*/\nimport \"C\"\n"},
		{"grouped C-only import", "package p\n\nimport (\n\t/*\nlower grouped cgo preamble.\n\t*/\n\t\"C\"\n)\n"},
		{"grouped multi-spec import", "package p\n\nimport (\n\t\"fmt\"\n\t// lower grouped cgo preamble.\n\t\"C\"\n)\n"},
		{"multiple import declarations", "package p\n\n/*\nlower first cgo preamble.\n*/\nimport \"C\"\n\nimport (\n\t/*\nlower second cgo preamble.\n\t*/\n\t\"C\"\n)\n"},
		{"ordinary import preamble", "package p\n\n/*\nlower ordinary import preamble.\n*/\nimport \"fmt\"\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			findings, err := goCommentFindings("fixture.go", []byte(test.source))
			require.NoError(t, err)
			if len(findings) != 0 {
				t.Fatalf("unexpected findings: %#v", findings)
			}
		})
	}
}

// TestGoCommentCapitalizationParseErrors verifies malformed Go.
func TestGoCommentCapitalizationParseErrors(t *testing.T) {
	_, err := goCommentFindings("broken.go", []byte("package p\nfunc broken(\n"))
	if err == nil || !strings.Contains(err.Error(), "parse Go file broken.go") {
		t.Fatalf("expected contextual parse error, got %v", err)
	}
}

// TestGoCommentCapitalizationSortsWithSeparators verifies diagnostic ordering.
func TestGoCommentCapitalizationSortsWithSeparators(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	writeTestFile(t, root, "source.go", "package p\nfunc body() {\n\t// lower body sentence.\n\n\t// =====\n}\n")
	runGit(t, root, "add", "source.go")

	findings, err := scanFixture(t, root)
	require.NoError(t, err)
	if len(findings) != 2 {
		t.Fatalf("unexpected findings: %#v", findings)
	}
	if findings[0].Line != 3 || findings[0].Message != "comment sentence starts with lowercase word" {
		t.Fatalf("unexpected capitalization finding: %#v", findings[0])
	}
	if findings[1].Line != 5 || findings[1].Message != "" {
		t.Fatalf("unexpected separator finding: %#v", findings[1])
	}
}

// TestScanUsesStagedGoContents verifies capitalization uses the staged blob.
func TestScanUsesStagedGoContents(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	writeTestFile(t, root, "source.go", "package p\nfunc body() {\n\t// lower body sentence.\n}\n")
	runGit(t, root, "add", "source.go")
	writeTestFile(t, root, "source.go", "package p\nfunc body() {\n\t// Upper body sentence.\n}\n")

	findings, err := scanFixture(t, root)
	require.NoError(t, err)
	if len(findings) != 1 || findings[0].Line != 3 || findings[0].Message != "comment sentence starts with lowercase word" {
		t.Fatalf("unexpected findings: %#v", findings)
	}
}

// TestScanUsesTrackedIndex verifies tracked and staged files are scanned while untracked files are ignored.
func TestScanUsesTrackedIndex(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	writeTestFile(t, root, "tracked.c", "// =====\n")
	writeTestFile(t, root, "staged.go", "// =====\npackage p\n")
	writeTestFile(t, root, "untracked.c", "// =====\n")
	runGit(t, root, "add", "tracked.c", "staged.go")
	writeTestFile(t, root, "tracked.c", "// ordinary comment\n")

	findings, err := scanFixture(t, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 || findings[0].Path != "staged.go" || findings[1].Path != "tracked.c" {
		t.Fatalf("unexpected findings: %#v", findings)
	}
	runGit(t, root, "add", "tracked.c")
	writeTestFile(t, root, "tracked.c", "// =====\n")
	if err := os.Remove(filepath.Join(root, "staged.go")); err != nil {
		t.Fatal(err)
	}
	findings, err = scanFixture(t, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Path != "staged.go" {
		t.Fatalf("working-tree divergence affected staged findings: %#v", findings)
	}
	runGit(t, root, "rm", "--cached", "-f", "tracked.c")
	findings, err = scanFixture(t, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Path != "staged.go" {
		t.Fatalf("staged deletion was scanned: %#v", findings)
	}
}

// TestScanRejectsSQLBlockComment verifies tracked SQL block separators fail linting.
func TestScanRejectsSQLBlockComment(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	writeTestFile(t, root, "source.sql", "/* === */\n")
	runGit(t, root, "add", "source.sql")

	findings, err := scanFixture(t, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Path != "source.sql" || findings[0].Line != 1 {
		t.Fatalf("unexpected findings: %#v", findings)
	}
}

// TestScanAllowsSetextHeadings verifies adjacent comment headings are allowed.
func TestScanAllowsSetextHeadings(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	writeTestFile(t, root, "source.go", "// Three-phase protocol\n// ====================\n/// Title\n/// ====================\n//! Title\n//! --------------------\n// * ===\npackage p\n")
	writeTestFile(t, root, "config.yaml", "# Three-phase protocol\n# --------------------\n")
	writeTestFile(t, root, "source.c", "/**\n * Three-phase protocol\n * ====================\n */\n")
	runGit(t, root, "add", "source.go", "config.yaml", "source.c")

	findings, err := scanFixture(t, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("unexpected findings: %#v", findings)
	}
}

// TestScanRejectsInvalidSetextUnderlines verifies only adjacent prose permits them.
func TestScanRejectsInvalidSetextUnderlines(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	writeTestFile(t, root, "source.go", "// ====================\n// Three-phase protocol\n\n// --------------------\n// ====================\n// --- foo ---\n\n/// ====================\n//! --------------------\n///====================\n//!--------------------\npackage p\n")
	runGit(t, root, "add", "source.go")

	findings, err := scanFixture(t, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 7 || findings[0].Line != 1 || findings[1].Line != 4 || findings[2].Line != 5 || findings[3].Line != 8 || findings[4].Line != 9 || findings[5].Line != 10 || findings[6].Line != 11 {
		t.Fatalf("unexpected findings: %#v", findings)
	}
}

// TestScanAllowsLabeledBlockComment verifies mixed block bodies are one payload.
func TestScanAllowsLabeledBlockComment(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	writeTestFile(t, root, "source.c", "/* ====\n * Labeled section\n * ==== */\n")
	runGit(t, root, "add", "source.c")

	findings, err := scanFixture(t, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("unexpected findings: %#v", findings)
	}
}

// TestScanAllowsBlankDocComments verifies empty comment forms do not fail linting.
func TestScanAllowsBlankDocComments(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	writeTestFile(t, root, "source.go", "//\n///\n//!\npackage p\n")
	runGit(t, root, "add", "source.go")

	findings, err := scanFixture(t, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("unexpected findings: %#v", findings)
	}
}

// TestScanSkipsUnclassifiedTrackedText verifies unknown files do not fail linting.
func TestScanSkipsUnclassifiedTrackedText(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	writeTestFile(t, root, "unknown.example", "// =====\n")
	runGit(t, root, "add", "unknown.example")

	findings, err := scanFixture(t, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("unexpected findings: %#v", findings)
	}
}

// TestScanSkipsGitlinksAndSymlinks verifies non-regular index entries are not read as text.
func TestScanSkipsGitlinksAndSymlinks(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	writeTestFile(t, root, "clean.c", "// ordinary comment\n")
	writeTestFile(t, root, "untracked-target.c", "// =====\n")
	if err := os.Symlink("untracked-target.c", filepath.Join(root, "link.go")); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "clean.c", "link.go")
	tree := runGitOutput(t, root, "mktree")
	runGit(t, root, "update-index", "--add", "--cacheinfo", "160000,"+tree+",submodule.go")
	findings, err := scanFixture(t, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("unexpected findings: %#v", findings)
	}
}

// TestScanStillChecksIgnoredGoSeparators verifies scope filtering is Go-only.
func TestScanStillChecksIgnoredGoSeparators(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	writeTestFile(t, root, "_separator.go", "// =====\npackage p\nfunc make(value any) any { return value }\n")
	writeTestFile(t, root, "body.go", "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n")
	runGit(t, root, "add", "_separator.go", "body.go")
	findings, err := scanFixture(t, root)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	require.Equal(t, "_separator.go", findings[0].Path)
	require.Empty(t, findings[0].Message)
}

// TestGitCommandsIgnoreCallerRepositoryEnvironment verifies fixtures and scans
// use their explicit repository root.
func TestGitCommandsIgnoreCallerRepositoryEnvironment(t *testing.T) {
	cleanEnvironment := sanitizedGitEnvironment(t)
	callerRoot := t.TempDir()
	runGit(t, callerRoot, "init")
	writeTestFile(t, callerRoot, "caller.go", "// ordinary comment\npackage p\n")
	runGit(t, callerRoot, "add", "caller.go")
	callerIndex := runGitOutputWithEnvironment(t, cleanEnvironment, callerRoot, "ls-files", "--cached", "--stage")
	callerBare := runGitOutputWithEnvironment(t, cleanEnvironment, callerRoot, "config", "--bool", "core.bare")

	fixtureRoot := t.TempDir()
	t.Setenv("GIT_DIR", filepath.Join(callerRoot, ".git"))
	t.Setenv("GIT_WORK_TREE", fixtureRoot)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(callerRoot, ".git", "index"))
	t.Setenv("GIT_COMMON_DIR", filepath.Join(callerRoot, ".git"))
	runGit(t, fixtureRoot, "init")
	writeTestFile(t, fixtureRoot, "fixture.go", "// =====\npackage p\n")
	runGit(t, fixtureRoot, "add", "fixture.go")

	findings, err := scanWithGitCommand(fixtureRoot, sanitizedGitCommand(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Path != "fixture.go" || findings[0].Line != 1 {
		t.Fatalf("unexpected findings: %#v", findings)
	}
	if actual := runGitOutputWithEnvironment(t, cleanEnvironment, callerRoot, "config", "--bool", "core.bare"); actual != callerBare {
		t.Fatalf("caller core.bare changed: expected %q, got %q", callerBare, actual)
	}
	if actual := runGitOutputWithEnvironment(t, cleanEnvironment, callerRoot, "ls-files", "--cached", "--stage"); actual != callerIndex {
		t.Fatalf("caller index changed: expected %q, got %q", callerIndex, actual)
	}
}

// TestScanUsesAlternateIndex verifies production scans the index selected by
// its Git environment.
func TestScanUsesAlternateIndex(t *testing.T) {
	clearGitLocalEnvironment(t)
	cleanEnvironment := sanitizedGitEnvironment(t)
	root := t.TempDir()
	runGit(t, root, "init")
	writeTestFile(t, root, "source.go", "// ordinary comment\npackage p\n")
	runGit(t, root, "add", "source.go")

	alternateIndex := filepath.Join(t.TempDir(), "index")
	writeTestFile(t, root, "source.go", "// =====\npackage p\n")
	gitEnvironment := append(cleanEnvironment, "GIT_INDEX_FILE="+alternateIndex)
	runGitWithEnvironment(t, gitEnvironment, root, "read-tree", "--empty")
	runGitWithEnvironment(t, gitEnvironment, root, "add", "source.go")
	if persistentContent := runGitOutputWithEnvironment(t, cleanEnvironment, root, "show", ":source.go"); persistentContent != "// ordinary comment\npackage p" {
		t.Fatalf("unexpected persistent index content: %q", persistentContent)
	}
	t.Setenv("GIT_INDEX_FILE", alternateIndex)

	findings, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Path != "source.go" || findings[0].Line != 1 {
		t.Fatalf("unexpected findings: %#v", findings)
	}
}

// TestScanResolvesPackageShadows verifies cross-file builtin shadows.
func TestScanResolvesPackageBuiltinShadows(t *testing.T) {
	tests := []struct {
		name       string
		first      string
		firstPath  string
		second     string
		secondPath string
		findings   int
	}{
		{
			name:     "package declaration",
			first:    "package p\nfunc make(value any) any { return value }\n",
			second:   "package p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n",
			findings: 1,
		},
		{
			name:     "package new declaration",
			first:    "package p\nfunc new(value any) any { return value }\n",
			second:   "package p\nfunc body(value any) { _ = new(/* lower package shadow. */ pkg.Type); _ = value }\n",
			findings: 1,
		},
		{
			name:     "receiver method does not shadow",
			first:    "package p\ntype receiver struct{}\nfunc (receiver) make(value any) any { return value }\n",
			second:   "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings: 0,
		},
		{
			name:     "dot import keeps builtins",
			first:    "package p\nimport . \"example.test/pkg\"\n",
			second:   "package p\nfunc body(value any) { _ = new(/* lower builtin with dot import. */ pkg.Type) }\n",
			findings: 0,
		},
		{
			name:     "package type make declaration",
			first:    "package p\ntype make func(any) any\n",
			second:   "package p\nfunc body(value any) { _ = make(/* lower package type shadow. */ []int, 1); _ = value }\n",
			findings: 1,
		},
		{
			name:     "package type new declaration",
			first:    "package p\ntype new func(any) any\n",
			second:   "package p\nfunc body(value any) { _ = new(/* lower package type shadow. */ pkg.Type); _ = value }\n",
			findings: 1,
		},
		{
			name:      "underscore file does not shadow",
			firstPath: "_shadow.go",
			first:     "package p\nfunc make(value any) any { return value }\n",
			second:    "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:  0,
		},
		{
			name:      "dot file does not shadow",
			firstPath: ".shadow.go",
			first:     "package p\nfunc make(value any) any { return value }\n",
			second:    "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:  0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			runGit(t, root, "init")
			firstPath, secondPath := test.firstPath, test.secondPath
			if firstPath == "" {
				firstPath = "a.go"
			}
			if secondPath == "" {
				secondPath = "b.go"
			}
			writeTestFile(t, root, firstPath, test.first)
			writeTestFile(t, root, secondPath, test.second)
			runGit(t, root, "add", firstPath, secondPath)
			findings, err := scanFixture(t, root)
			require.NoError(t, err)
			require.Len(t, findings, test.findings)
			if test.findings != 0 {
				require.Equal(t, secondPath, findings[0].Path)
			}
		})
	}
}

// TestScanRespectsPackageBuildVariants verifies compatible shadow declarations.
func TestScanRespectsPackageBuildVariants(t *testing.T) {
	tests := []struct {
		name       string
		first      string
		firstPath  string
		second     string
		secondPath string
		findings   int
	}{
		{
			name:       "same go build constraint",
			firstPath:  "a.go",
			first:      "//go:build linux\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b.go",
			second:     "//go:build linux\n\npackage p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n",
			findings:   1,
		},
		{
			name:       "exclusive go build constraints",
			firstPath:  "a.go",
			first:      "//go:build windows\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b.go",
			second:     "//go:build linux\n\npackage p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "exclusive plus build constraints",
			firstPath:  "a.go",
			first:      "// +build windows\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b.go",
			second:     "// +build linux\n\npackage p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "matching modern and legacy constraints",
			firstPath:  "a.go",
			first:      "//go:build windows\n// +build windows\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_linux.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "tab-separated go build constraint",
			firstPath:  "a.go",
			first:      "//go:build\tlinux\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_windows.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "no-space legacy build constraint",
			firstPath:  "a.go",
			first:      "//+build linux\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_windows.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "implicit cgo requirement",
			firstPath:  "a.go",
			first:      "package p\nimport \"C\"\nfunc make(value any) any { return value }\n",
			secondPath: "b.go",
			second:     "//go:build !cgo\n\npackage p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "filename constraints",
			firstPath:  "a_windows.go",
			first:      "package p\nfunc make(value any) any { return value }\n",
			secondPath: "b_linux.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "nonterminal filename component is unconstrained",
			firstPath:  "a_linux_extra.go",
			first:      "package p\nfunc make(value any) any { return value }\n",
			secondPath: "b_windows.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n",
			findings:   1,
		},
		{
			name:       "android includes linux filename",
			firstPath:  "a_linux.go",
			first:      "package p\nfunc make(value any) any { return value }\n",
			secondPath: "b_android.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n",
			findings:   1,
		},
		{
			name:       "invalid android ppc64 target pair",
			firstPath:  "a_android_ppc64.go",
			first:      "package p\nfunc make(value any) any { return value }\n",
			secondPath: "b.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "unsupported cgo js wasm target",
			firstPath:  "a_js_wasm.go",
			first:      "package p\nimport \"C\"\nfunc make(value any) any { return value }\n",
			secondPath: "b_js_wasm.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "illumos includes solaris filename",
			firstPath:  "a_solaris.go",
			first:      "package p\nfunc make(value any) any { return value }\n",
			secondPath: "b_illumos.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n",
			findings:   1,
		},
		{
			name:       "ios includes darwin filename",
			firstPath:  "a_darwin.go",
			first:      "package p\nfunc make(value any) any { return value }\n",
			secondPath: "b_ios.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n",
			findings:   1,
		},
		{
			name:       "unix build tag includes linux",
			firstPath:  "a.go",
			first:      "//go:build unix\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b.go",
			second:     "//go:build linux\n\npackage p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n",
			findings:   1,
		},
		{
			name:       "dotted release tag",
			firstPath:  "a.go",
			first:      "//go:build go1.24\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b.go",
			second:     "//go:build go1.24\n\npackage p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n",
			findings:   1,
		},
		{
			name:       "exclusive dotted release tags",
			firstPath:  "a.go",
			first:      "//go:build go1.24\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b.go",
			second:     "//go:build !go1.24\n\npackage p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "impossible compiler tags",
			firstPath:  "a.go",
			first:      "//go:build !gc && !gccgo\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "impossible release tag ordering",
			firstPath:  "a.go",
			first:      "//go:build go1.24 && !go1.23\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "dotted experiment tag",
			firstPath:  "a.go",
			first:      "//go:build goexperiment.boringcrypto\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b.go",
			second:     "//go:build goexperiment.boringcrypto\n\npackage p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n",
			findings:   1,
		},
		{
			name:       "block comment does not activate build tag",
			firstPath:  "a.go",
			first:      "/*\n//go:build windows\n*/\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_linux.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n",
			findings:   1,
		},
		{
			name:       "closed block before package stops header scan",
			firstPath:  "a.go",
			first:      "/* header */ package p\n// +build windows\nfunc make(value any) any { return value }\n",
			secondPath: "b_linux.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n",
			findings:   1,
		},
		{
			name:       "BOM build header is active",
			firstPath:  "a.go",
			first:      "\ufeff//go:build windows\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_linux.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "impossible amd64 feature levels",
			firstPath:  "a_amd64.go",
			first:      "//go:build amd64.v3\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_amd64.go",
			second:     "//go:build !amd64.v2\n\npackage p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "impossible amd64 v4 feature levels",
			firstPath:  "a_amd64.go",
			first:      "//go:build amd64.v4\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_amd64.go",
			second:     "//go:build !amd64.v3\n\npackage p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "compatible amd64 feature levels",
			firstPath:  "a_amd64.go",
			first:      "//go:build amd64.v2\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_amd64.go",
			second:     "//go:build amd64.v1\n\npackage p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n",
			findings:   1,
		},
		{
			name:       "compatible arm64 feature levels",
			firstPath:  "a_arm64.go",
			first:      "//go:build arm64.v8.0\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_arm64.go",
			second:     "//go:build arm64.v8.1\n\npackage p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n",
			findings:   1,
		},
		{
			name:       "incompatible arm64 cross-major feature levels",
			firstPath:  "a_arm64.go",
			first:      "//go:build arm64.v9.1\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_arm64.go",
			second:     "//go:build !arm64.v8.6\n\npackage p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "compatible arm64 cross-major feature levels",
			firstPath:  "a_arm64.go",
			first:      "//go:build arm64.v9.0\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_arm64.go",
			second:     "//go:build !arm64.v8.6\n\npackage p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n",
			findings:   1,
		},
		{
			name:       "noncanonical release tag remains variable",
			firstPath:  "a.go",
			first:      "//go:build go1.025\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b.go",
			second:     "//go:build go1.025\n\npackage p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n",
			findings:   1,
		},
		{
			name:       "canonical long future release tag is fixed",
			firstPath:  "a.go",
			first:      "//go:build go1.999999999999999999999999999999\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b.go",
			second:     "//go:build go1.999999999999999999999999999999\n\npackage p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "foreign amd64 feature is fixed false",
			firstPath:  "a_arm64.go",
			first:      "//go:build amd64.v3\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_arm64.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "mipsle feature family is target-specific",
			firstPath:  "a_mipsle.go",
			first:      "//go:build mips.hardfloat\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_mipsle.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "mipsle feature family remains compatible",
			firstPath:  "a_mipsle.go",
			first:      "//go:build mipsle.hardfloat\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_mipsle.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n",
			findings:   1,
		},
		{
			name:       "mips64le feature family is target-specific",
			firstPath:  "a_mips64le.go",
			first:      "//go:build mips64.hardfloat\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_mips64le.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "mips64le feature family remains compatible",
			firstPath:  "a_mips64le.go",
			first:      "//go:build mips64le.hardfloat\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_mips64le.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n",
			findings:   1,
		},
		{
			name:       "ppc64le feature family is target-specific",
			firstPath:  "a_ppc64le.go",
			first:      "//go:build ppc64.power8\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_ppc64le.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "ppc64le feature family remains compatible",
			firstPath:  "a_ppc64le.go",
			first:      "//go:build ppc64le.power8\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_ppc64le.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n",
			findings:   1,
		},
		{
			name:       "multiple leading block comments retain header",
			firstPath:  "a.go",
			first:      "/* first */ /* second */\n//go:build windows\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_linux.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "block and line comments retain header",
			firstPath:  "a.go",
			first:      "/* first */ // second\n//go:build windows\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_linux.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "block explanatory comment retains legacy header",
			firstPath:  "a.go",
			first:      "// +build windows\n/* explanatory header */\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_linux.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "block internal blank does not end legacy header",
			firstPath:  "a.go",
			first:      "// +build windows\n/* explanatory\n\nheader */\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_linux.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n",
			findings:   1,
		},
		{
			name:       "legacy constraint without blank is unconditional",
			firstPath:  "a.go",
			first:      "// +build windows\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_linux.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n",
			findings:   1,
		},
		{
			name:       "legacy constraint survives explanatory header",
			firstPath:  "a.go",
			first:      "// +build windows\n// explanatory header\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_linux.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			runGit(t, root, "init")
			writeTestFile(t, root, test.firstPath, test.first)
			writeTestFile(t, root, test.secondPath, test.second)
			runGit(t, root, "add", test.firstPath, test.secondPath)
			findings, err := scanFixture(t, root)
			require.NoError(t, err)
			require.Len(t, findings, test.findings)
		})
	}
}

// TestGoCommentBuildFeatureVariants verifies pinned architecture feature tags.
func TestGoCommentBuildFeatureVariants(t *testing.T) {
	amd64 := goCommentBuildFeatureVariants(goCommentBuildTarget{GOARCH: "amd64"})
	require.Len(t, amd64, 4)
	require.True(t, amd64[3]["amd64.v3"])
	require.True(t, amd64[3]["amd64.v4"])

	arm64 := goCommentBuildFeatureVariants(goCommentBuildTarget{GOARCH: "arm64"})
	require.Len(t, arm64, 16)
	require.True(t, arm64[11]["arm64.v9.1"])
	require.True(t, arm64[11]["arm64.v8.6"])
	require.False(t, arm64[10]["arm64.v8.6"])
	require.True(t, arm64[1]["arm64.v8.1"])
	arm64Known := goCommentBuildKnownTags("linux", "arm64", "gc", false)
	require.False(t, arm64Known["amd64.v3"])
	require.False(t, arm64Known["mipsle.hardfloat"])
	require.False(t, arm64Known["ppc64le.power8"])
	amd64Features := goCommentBuildFeatureVariants(goCommentBuildTarget{GOARCH: "amd64"})
	require.True(t, amd64Features[len(amd64Features)-1]["amd64.v1"])
	require.False(t, arm64Known["mips.hardfloat"])
}

// TestScanRejectsMalformedBuildMetadata reports contextual header errors.
func TestScanRejectsMalformedBuildMetadata(t *testing.T) {
	tests := []struct {
		name   string
		first  string
		second string
		want   string
	}{
		{
			name:   "malformed modern expression",
			first:  "//go:build (\n\npackage p\nfunc make(value any) any { return value }\n",
			second: "package p\nfunc body() {}\n",
			want:   "a.go:1",
		},
		{
			name:   "duplicate modern directives",
			first:  "//go:build linux\n//go:build windows\n\npackage p\nfunc make(value any) any { return value }\n",
			second: "package p\nfunc body() {}\n",
			want:   "duplicate //go:build",
		},
		{
			name:   "mismatched modern and legacy directives",
			first:  "//go:build linux\n// +build windows\n\npackage p\nfunc make(value any) any { return value }\n",
			second: "package p\nfunc body() {}\n",
			want:   "mismatched",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			runGit(t, root, "init")
			writeTestFile(t, root, "a.go", test.first)
			writeTestFile(t, root, "b.go", test.second)
			runGit(t, root, "add", "a.go", "b.go")
			_, err := scanFixture(t, root)
			require.Error(t, err)
			require.ErrorContains(t, err, test.want)
		})
	}
}

// TestScanChecksLargePairedBuildConstraints proves large headers exactly.
func TestScanChecksLargePairedBuildConstraints(t *testing.T) {
	tags := make([]string, 13)
	for index := range tags {
		tags[index] = fmt.Sprintf("t%d", index)
	}
	modern := strings.Join(tags, " && ")
	tests := []struct {
		name      string
		legacy    string
		wantError bool
	}{
		{
			name:      "large mismatch",
			legacy:    strings.Join(append(append([]string(nil), tags[:12]...), "other"), ","),
			wantError: true,
		},
		{
			name:   "large equivalent",
			legacy: strings.Join(tags, ","),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			runGit(t, root, "init")
			first := "//go:build " + modern + "\n// +build " + test.legacy + "\n\npackage p\nfunc make(value any) any { return value }\n"
			second := "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n"
			writeTestFile(t, root, "a.go", first)
			writeTestFile(t, root, "b.go", second)
			runGit(t, root, "add", "a.go", "b.go")
			_, err := scanFixture(t, root)
			if test.wantError {
				require.Error(t, err)
				require.ErrorContains(t, err, "mismatched")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestScanBuildConstraintCutoffPreservesProofs checks bounded compatibility.
func TestScanBuildConstraintCutoffPreservesProofs(t *testing.T) {
	unknownTags := strings.Join([]string{
		"custom0", "custom1", "custom2", "custom3", "custom4", "custom5", "custom6", "custom7",
	}, " && ")
	tests := []struct {
		name       string
		first      string
		firstPath  string
		second     string
		secondPath string
		findings   int
	}{
		{
			name:       "filename contradiction survives cutoff",
			firstPath:  "a_windows.go",
			first:      "//go:build " + unknownTags + "\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b_linux.go",
			second:     "//go:build " + unknownTags + "\n\npackage p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "release contradiction survives cutoff",
			firstPath:  "a.go",
			first:      "//go:build go1.24 && " + unknownTags + "\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b.go",
			second:     "//go:build !go1.24 && " + unknownTags + "\n\npackage p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "compiler contradiction survives cutoff",
			firstPath:  "a.go",
			first:      "//go:build gc && " + unknownTags + "\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b.go",
			second:     "//go:build gccgo && " + unknownTags + "\n\npackage p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "cgo contradiction survives cutoff",
			firstPath:  "a.go",
			first:      "//go:build cgo && " + unknownTags + "\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b.go",
			second:     "//go:build !cgo && " + unknownTags + "\n\npackage p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "repeated unknown contradiction survives cutoff",
			firstPath:  "a.go",
			first:      "//go:build t1 && !t1 && " + unknownTags + "\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "De Morgan unknown contradiction survives cutoff",
			firstPath:  "a.go",
			first:      "//go:build (t1 || t2) && !t1 && !t2 && " + unknownTags + "\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b.go",
			second:     "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n",
			findings:   0,
		},
		{
			name:       "satisfiable unknown tags survive cutoff",
			firstPath:  "a.go",
			first:      "//go:build " + unknownTags + "\n\npackage p\nfunc make(value any) any { return value }\n",
			secondPath: "b.go",
			second:     "//go:build " + unknownTags + "\n\npackage p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n",
			findings:   1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			runGit(t, root, "init")
			writeTestFile(t, root, test.firstPath, test.first)
			writeTestFile(t, root, test.secondPath, test.second)
			runGit(t, root, "add", test.firstPath, test.secondPath)
			findings, err := scanFixture(t, root)
			require.NoError(t, err)
			require.Len(t, findings, test.findings)
		})
	}
}

// TestScanBuildConstraintCutoffIsDeterministic checks stable SAT variable order.
func TestScanBuildConstraintCutoffIsDeterministic(t *testing.T) {
	unknownTags := strings.Join([]string{
		"t0", "t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8", "t9", "t10", "t11",
	}, " && ")
	root := t.TempDir()
	runGit(t, root, "init")
	writeTestFile(t, root, "a.go", "//go:build t0 && !t0 && "+unknownTags+"\n\npackage p\nfunc make(value any) any { return value }\n")
	writeTestFile(t, root, "b.go", "package p\nfunc body(value any) { _ = make(/* lower builtin type. */ []int, 1); _ = value }\n")
	runGit(t, root, "add", "a.go", "b.go")
	for range 10 {
		findings, err := scanFixture(t, root)
		require.NoError(t, err)
		require.Empty(t, findings)
	}
	satisfiableRoot := t.TempDir()
	runGit(t, satisfiableRoot, "init")
	writeTestFile(t, satisfiableRoot, "a.go", "//go:build "+unknownTags+"\n\npackage p\nfunc make(value any) any { return value }\n")
	writeTestFile(t, satisfiableRoot, "b.go", "package p\nfunc body(value any) { _ = make(/* lower package shadow. */ []int, 1); _ = value }\n")
	runGit(t, satisfiableRoot, "add", "a.go", "b.go")
	findings, err := scanFixture(t, satisfiableRoot)
	require.NoError(t, err)
	require.Len(t, findings, 1)
}

func writeTestFile(t *testing.T, root, path, content string) {
	t.Helper()
	fullPath := filepath.Join(root, path)
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func scanFixture(t *testing.T, root string) ([]finding, error) {
	t.Helper()
	return scanWithGitCommand(root, sanitizedGitCommand(t))
}

func runGit(t *testing.T, root string, arguments ...string) {
	t.Helper()
	command := sanitizedGitCommand(t)(root, arguments...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
}

func runGitOutput(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	command := sanitizedGitCommand(t)(root, arguments...)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", arguments, err)
	}
	return string(output[:len(output)-1])
}

func runGitWithEnvironment(t *testing.T, environment []string, root string, arguments ...string) {
	t.Helper()
	command := newGitCommand(root, arguments...)
	command.Env = environment
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
}

func runGitOutputWithEnvironment(t *testing.T, environment []string, root string, arguments ...string) string {
	t.Helper()
	command := newGitCommand(root, arguments...)
	command.Env = environment
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", arguments, err)
	}
	return string(output[:len(output)-1])
}

func sanitizedGitCommand(t *testing.T) gitCommand {
	t.Helper()
	environment := sanitizedGitEnvironment(t)
	return func(root string, arguments ...string) *exec.Cmd {
		command := newGitCommand(root, arguments...)
		command.Env = environment
		return command
	}
}

func sanitizedGitEnvironment(t *testing.T) []string {
	t.Helper()
	localVariables := gitLocalEnvironmentVariables(t)
	environment := make([]string, 0, len(os.Environ()))
	for _, variable := range os.Environ() {
		name, _, found := strings.Cut(variable, "=")
		if found && !isGitLocalEnvironmentVariable(name, localVariables) {
			environment = append(environment, variable)
		}
	}
	return environment
}

func clearGitLocalEnvironment(t *testing.T) {
	t.Helper()
	localVariables := gitLocalEnvironmentVariables(t)
	type environmentValue struct {
		value   string
		present bool
	}
	previous := map[string]environmentValue{}
	for _, variable := range os.Environ() {
		name, _, found := strings.Cut(variable, "=")
		if !found || !isGitLocalEnvironmentVariable(name, localVariables) {
			continue
		}
		value, present := os.LookupEnv(name)
		previous[name] = environmentValue{value: value, present: present}
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for name, previousValue := range previous {
			var err error
			if previousValue.present {
				err = os.Setenv(name, previousValue.value)
			} else {
				err = os.Unsetenv(name)
			}
			if err != nil {
				t.Error(err)
			}
		}
	})
}

func gitLocalEnvironmentVariables(t *testing.T) map[string]struct{} {
	t.Helper()
	command := exec.Command("git", "rev-parse", "--local-env-vars")
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	localVariables := map[string]struct{}{}
	for name := range strings.FieldsSeq(string(output)) {
		localVariables[name] = struct{}{}
	}
	return localVariables
}

func isGitLocalEnvironmentVariable(name string, localVariables map[string]struct{}) bool {
	if _, found := localVariables[name]; found {
		return true
	}
	return strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_")
}
