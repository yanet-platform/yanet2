package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

// TestScanUsesTrackedIndex verifies tracked and staged files are scanned while untracked files are ignored.
func TestScanUsesTrackedIndex(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	writeTestFile(t, root, "tracked.c", "// =====\n")
	writeTestFile(t, root, "staged.go", "// =====\n")
	writeTestFile(t, root, "untracked.c", "// =====\n")
	runGit(t, root, "add", "tracked.c", "staged.go")
	writeTestFile(t, root, "tracked.c", "// ordinary comment\n")

	findings, err := scan(root)
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
	findings, err = scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Path != "staged.go" {
		t.Fatalf("working-tree divergence affected staged findings: %#v", findings)
	}
	runGit(t, root, "rm", "--cached", "-f", "tracked.c")
	findings, err = scan(root)
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

	findings, err := scan(root)
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
	writeTestFile(t, root, "source.go", "// Three-phase protocol\n// ====================\n/// Title\n/// ====================\n//! Title\n//! --------------------\n// * ===\n")
	writeTestFile(t, root, "config.yaml", "# Three-phase protocol\n# --------------------\n")
	writeTestFile(t, root, "source.c", "/**\n * Three-phase protocol\n * ====================\n */\n")
	runGit(t, root, "add", "source.go", "config.yaml", "source.c")

	findings, err := scan(root)
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
	writeTestFile(t, root, "source.go", "// ====================\n// Three-phase protocol\n\n// --------------------\n// ====================\n// --- foo ---\n\n/// ====================\n//! --------------------\n///====================\n//!--------------------\n")
	runGit(t, root, "add", "source.go")

	findings, err := scan(root)
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

	findings, err := scan(root)
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
	writeTestFile(t, root, "source.go", "//\n///\n//!\n")
	runGit(t, root, "add", "source.go")

	findings, err := scan(root)
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

	findings, err := scan(root)
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
	if err := os.Symlink("untracked-target.c", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "clean.c", "link")
	tree := runGitOutput(t, root, "mktree")
	runGit(t, root, "update-index", "--add", "--cacheinfo", "160000,"+tree+",submodule")
	findings, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("unexpected findings: %#v", findings)
	}
}

// TestGitCommandsIgnoreCallerRepositoryEnvironment verifies fixtures and scans
// use their explicit repository root.
func TestGitCommandsIgnoreCallerRepositoryEnvironment(t *testing.T) {
	cleanEnvironment := sanitizedGitEnvironment(t)
	callerRoot := t.TempDir()
	runGit(t, callerRoot, "init")
	writeTestFile(t, callerRoot, "caller.go", "// ordinary comment\n")
	runGit(t, callerRoot, "add", "caller.go")
	callerIndex := runGitOutputWithEnvironment(t, cleanEnvironment, callerRoot, "ls-files", "--cached", "--stage")
	callerBare := runGitOutputWithEnvironment(t, cleanEnvironment, callerRoot, "config", "--bool", "core.bare")

	fixtureRoot := t.TempDir()
	t.Setenv("GIT_DIR", filepath.Join(callerRoot, ".git"))
	t.Setenv("GIT_WORK_TREE", fixtureRoot)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(callerRoot, ".git", "index"))
	t.Setenv("GIT_COMMON_DIR", filepath.Join(callerRoot, ".git"))
	runGit(t, fixtureRoot, "init")
	writeTestFile(t, fixtureRoot, "fixture.go", "// =====\n")
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
	writeTestFile(t, root, "source.go", "// ordinary comment\n")
	runGit(t, root, "add", "source.go")

	alternateIndex := filepath.Join(t.TempDir(), "index")
	writeTestFile(t, root, "source.go", "// =====\n")
	gitEnvironment := append(cleanEnvironment, "GIT_INDEX_FILE="+alternateIndex)
	runGitWithEnvironment(t, gitEnvironment, root, "read-tree", "--empty")
	runGitWithEnvironment(t, gitEnvironment, root, "add", "source.go")
	if persistentContent := runGitOutputWithEnvironment(t, cleanEnvironment, root, "show", ":source.go"); persistentContent != "// ordinary comment" {
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

func writeTestFile(t *testing.T, root, path, content string) {
	t.Helper()
	fullPath := filepath.Join(root, path)
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
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
