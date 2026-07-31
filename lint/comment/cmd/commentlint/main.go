// Command commentlint rejects pure-run comment separators in tracked files.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type profile int

const (
	profileNone profile = iota
	profileSlash
	profileHash
	profileMarkup
	profileSQL
	profileINI
)

type finding struct {
	Path string
	Line int
}

type trackedFile struct {
	Path string
	Mode string
	Hash string
}

type shellHeredoc struct {
	Marker    string
	StripTabs bool
}

type shellLexState struct {
	Quote                      byte
	CommandDepth               int
	ResumeDoubleDepths         []int
	InWord                     bool
	WordContinues              bool
	BacktickDepth              int
	BacktickResumeDoubleDepths []int
}

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	findings, err := scan(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, finding := range findings {
		fmt.Printf("%s:%d: pure-run comment separator\n", finding.Path, finding.Line)
	}
	if len(findings) != 0 {
		os.Exit(1)
	}
}

func scan(root string) ([]finding, error) {
	files, err := trackedFiles(root)
	if err != nil {
		return nil, err
	}
	contents, err := stagedContents(root, files)
	if err != nil {
		return nil, err
	}
	var findings []finding
	for _, file := range files {
		if file.Mode != "100644" && file.Mode != "100755" {
			continue
		}
		content := contents[file.Path]
		fileProfile, known := profileFor(file.Path)
		if !known || fileProfile == profileNone {
			continue
		}
		commentLines := comments(file.Path, content, fileProfile)
		for commentIndex, comment := range commentLines {
			if isSeparator(comment.Text) && !isSetextUnderline(commentLines, commentIndex) {
				findings = append(findings, finding{Path: file.Path, Line: comment.Line})
			}
		}
	}
	sort.Slice(findings, func(left, right int) bool {
		if findings[left].Path == findings[right].Path {
			return findings[left].Line < findings[right].Line
		}
		return findings[left].Path < findings[right].Path
	})
	return findings, nil
}

func trackedFiles(root string) ([]trackedFile, error) {
	command := exec.Command("git", "-C", root, "ls-files", "--cached", "--stage", "-z")
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("list tracked files: %w", err)
	}
	return parseTrackedFiles(output), nil
}

func parseTrackedFiles(output []byte) []trackedFile {
	records := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
	if len(records) == 1 && records[0] == "" {
		return nil
	}
	files := make([]trackedFile, 0, len(records))
	for _, record := range records {
		metadata, path, found := strings.Cut(record, "\t")
		fields := strings.Fields(metadata)
		if !found || len(fields) != 3 || fields[2] != "0" {
			continue
		}
		files = append(files, trackedFile{Path: path, Mode: fields[0], Hash: fields[1]})
	}
	return files
}

func stagedContents(root string, files []trackedFile) (map[string][]byte, error) {
	var request strings.Builder
	for _, file := range files {
		if file.Mode == "100644" || file.Mode == "100755" {
			fmt.Fprintln(&request, file.Hash)
		}
	}
	command := exec.Command("git", "-C", root, "cat-file", "--batch")
	command.Stdin = strings.NewReader(request.String())
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("read staged files: %w", err)
	}
	reader := bufio.NewReader(bytes.NewReader(output))
	contents := map[string][]byte{}
	for _, file := range files {
		if file.Mode != "100644" && file.Mode != "100755" {
			continue
		}
		header, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read staged %s header: %w", file.Path, err)
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[0] != file.Hash || fields[1] != "blob" {
			return nil, fmt.Errorf("read staged %s: unexpected object header %q", file.Path, strings.TrimSpace(header))
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("read staged %s size: %w", file.Path, err)
		}
		content := make([]byte, size)
		if _, err := io.ReadFull(reader, content); err != nil {
			return nil, fmt.Errorf("read staged %s content: %w", file.Path, err)
		}
		separator, err := reader.ReadByte()
		if err != nil || separator != '\n' {
			return nil, fmt.Errorf("read staged %s separator", file.Path)
		}
		contents[file.Path] = content
	}
	return contents, nil
}

func profileFor(path string) (profile, bool) {
	base := filepath.Base(path)
	if base == "Makefile" || base == "Justfile" || strings.Contains(base, "Dockerfile") || base == ".gitignore" || base == ".dockerignore" || base == "commit-msg" || base == "pre-commit" || base == "rules" || base == "CODEOWNERS" {
		return profileHash, true
	}
	if base == ".clang-format" || base == ".clang-tidy" || base == ".gitmodules" {
		return profileHash, true
	}
	if base == "AUTHORS" || base == "LICENSE" || base == "changelog" || base == "compat" || base == "control" || base == "format" || base == "options" || base == "meta-data" || strings.Contains(path, "/testdata/fuzz/") {
		return profileNone, true
	}
	switch filepath.Ext(path) {
	case ".c", ".h", ".cc", ".cpp", ".cxx", ".go", ".rs", ".ts", ".tsx", ".js", ".jsx", ".proto", ".css", ".scss", ".java", ".kt", ".mod":
		return profileSlash, true
	case ".py", ".sh", ".bash", ".zsh", ".yaml", ".yml", ".toml", ".ini", ".cfg", ".conf", ".service", ".install", ".env", ".org", ".npmrc", ".build", ".dev":
		if filepath.Ext(path) == ".ini" || filepath.Ext(path) == ".service" || filepath.Ext(path) == ".conf" || filepath.Ext(path) == ".cfg" {
			return profileINI, true
		}
		return profileHash, true
	case ".lock":
		if base == "Cargo.lock" {
			return profileHash, true
		}
		return profileNone, false
	case ".html", ".htm", ".xml", ".svg", ".md", ".markdown":
		return profileMarkup, true
	case ".sql":
		return profileSQL, true
	case ".json", ".sum", ".txt", ".worktreeinclude":
		return profileNone, true
	case "":
	}
	return profileNone, false
}

type comment struct {
	Line int
	Text string
}

func comments(path string, content []byte, fileProfile profile) []comment {
	switch fileProfile {
	case profileSlash:
		return slashCommentsForPath(path, content)
	case profileHash:
		return hashCommentsForPath(path, content)
	case profileINI:
		return iniComments(content)
	case profileMarkup:
		return markupCommentsForPath(path, content)
	case profileSQL:
		return sqlComments(content)
	default:
		return nil
	}
}

func slashComments(content []byte) []comment {
	return slashCommentsForPath("", content)
}

func slashCommentsForPath(path string, content []byte) []comment {
	var comments []comment
	line := 1
	isRust := filepath.Ext(path) == ".rs"
	isJava := filepath.Ext(path) == ".java"
	isTripleQuotedLanguage := isJava || filepath.Ext(path) == ".kt"
	isTemplateLanguage := filepath.Ext(path) == ".ts" || filepath.Ext(path) == ".tsx" || filepath.Ext(path) == ".js" || filepath.Ext(path) == ".jsx"
	for offset := 0; offset < len(content); {
		if content[offset] == '\n' {
			line++
			offset++
			continue
		}
		if content[offset] == '`' && isTemplateLanguage {
			extracted, next := templateLiteralComments(path, content, offset, line)
			comments = append(comments, extracted...)
			line += bytes.Count(content[offset:next], []byte("\n"))
			offset = next
			continue
		}
		if isTripleQuotedLanguage && bytes.HasPrefix(content[offset:], []byte(`"""`)) {
			next := skipTripleQuoted(content, offset, isJava)
			line += bytes.Count(content[offset:next], []byte("\n"))
			offset = next
			continue
		}
		if content[offset] == '"' || content[offset] == '`' || (content[offset] == '\'' && (!isRust || !isRustLifetime(content, offset))) {
			next := skipQuoted(content, offset, content[offset])
			line += bytes.Count(content[offset:next], []byte("\n"))
			offset = next
			continue
		}
		if content[offset] == 'r' || (content[offset] == 'R' && offset+1 < len(content) && content[offset+1] == '"') || (content[offset] == 'b' && offset+1 < len(content) && (content[offset+1] == 'r' || content[offset+1] == 'R')) {
			next := skipRustRaw(content, offset)
			line += bytes.Count(content[offset:next], []byte("\n"))
			offset = next
			continue
		}
		if offset+1 < len(content) && content[offset] == '/' && content[offset+1] == '/' {
			end := bytes.IndexByte(content[offset:], '\n')
			if end < 0 {
				end = len(content) - offset
			}
			text := string(content[offset+2 : offset+end])
			if len(text) > 0 && (text[0] == '!' || text[0] == '/' && (len(text) == 1 || text[1] != '/')) {
				text = text[1:]
			}
			comments = append(comments, comment{Line: line, Text: text})
			offset += end
			continue
		}
		if offset+1 < len(content) && content[offset] == '/' && content[offset+1] == '*' {
			end := blockCommentEnd(content, offset+2, isRust)
			body := content[offset+2 : offset+2+end]
			lines := strings.Split(string(body), "\n")
			for idx, text := range lines {
				text = strings.TrimLeft(text, " \t")
				if len(text) > 1 && text[0] == '*' && (text[1] == ' ' || text[1] == '\t') {
					text = text[1:]
				}
				lines[idx] = text
			}
			comments = append(comments, comment{Line: line, Text: strings.Join(lines, "\n")})
			line += bytes.Count(body, []byte("\n"))
			offset += end + 4
			continue
		}
		offset++
	}
	return comments
}

func templateLiteralComments(path string, content []byte, offset, line int) ([]comment, int) {
	var comments []comment
	templateStart := offset
	for offset++; offset < len(content); offset++ {
		if content[offset] == '\\' {
			offset++
			continue
		}
		if content[offset] == '`' {
			return comments, offset + 1
		}
		if content[offset] != '$' || offset+1 == len(content) || content[offset+1] != '{' {
			continue
		}
		start := offset + 2
		end := templateInterpolationEnd(content, start)
		if end == len(content) {
			return comments, len(content)
		}
		inner := slashCommentsForPath(path, content[start:end])
		baseLine := line + bytes.Count(content[templateStart:start], []byte("\n"))
		for _, current := range inner {
			current.Line += baseLine - 1
			comments = append(comments, current)
		}
		offset = end
	}
	return comments, offset
}

func templateInterpolationEnd(content []byte, offset int) int {
	depth := 1
	canStartRegex := true
	for offset < len(content) {
		switch content[offset] {
		case '\'', '"':
			offset = skipQuoted(content, offset, content[offset])
			canStartRegex = false
			continue
		case '`':
			offset = templateLiteralEnd(content, offset)
			canStartRegex = false
			continue
		case '/':
			if offset+1 < len(content) && content[offset+1] == '/' {
				end := bytes.IndexByte(content[offset:], '\n')
				if end < 0 {
					return len(content)
				}
				offset += end + 1
				canStartRegex = false
				continue
			}
			if offset+1 < len(content) && content[offset+1] == '*' {
				end := bytes.Index(content[offset+2:], []byte("*/"))
				if end < 0 {
					return len(content)
				}
				offset += end + 4
				canStartRegex = false
				continue
			}
			if canStartRegex {
				offset = skipJavaScriptRegex(content, offset)
				canStartRegex = false
				continue
			}
			canStartRegex = true
		case '{':
			depth++
			canStartRegex = true
		case '}':
			depth--
			if depth == 0 {
				return offset
			}
			canStartRegex = false
		case '(', '[', ',', ':', ';', '?', '=', '!', '+', '-', '*', '%', '&', '|', '^', '~', '<', '>':
			canStartRegex = true
		case ')', ']':
			canStartRegex = false
		default:
			if isJavaScriptIdentifierStart(content[offset]) {
				start := offset
				offset++
				for offset < len(content) && isJavaScriptIdentifierPart(content[offset]) {
					offset++
				}
				canStartRegex = isJavaScriptRegexPrefixKeyword(string(content[start:offset])) && (start == 0 || content[start-1] != '.')
				continue
			}
			if content[offset] != ' ' && content[offset] != '\t' && content[offset] != '\n' && content[offset] != '\r' {
				canStartRegex = false
			}
		}
		offset++
	}
	return len(content)
}

func isJavaScriptIdentifierStart(value byte) bool {
	return value == '_' || value == '$' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isJavaScriptIdentifierPart(value byte) bool {
	return isJavaScriptIdentifierStart(value) || value >= '0' && value <= '9'
}

func isJavaScriptRegexPrefixKeyword(identifier string) bool {
	switch identifier {
	case "await", "case", "delete", "do", "else", "in", "instanceof", "new", "of", "return", "throw", "typeof", "void", "yield":
		return true
	default:
		return false
	}
}

func skipJavaScriptRegex(content []byte, offset int) int {
	inCharacterClass := false
	for offset++; offset < len(content); offset++ {
		if content[offset] == '\\' {
			offset++
			continue
		}
		if content[offset] == '[' {
			inCharacterClass = true
			continue
		}
		if content[offset] == ']' {
			inCharacterClass = false
			continue
		}
		if content[offset] == '/' && !inCharacterClass {
			return offset + 1
		}
		if content[offset] == '\n' {
			return len(content)
		}
	}
	return len(content)
}

func templateLiteralEnd(content []byte, offset int) int {
	for offset++; offset < len(content); offset++ {
		if content[offset] == '\\' {
			offset++
			continue
		}
		if content[offset] == '`' {
			return offset + 1
		}
		if content[offset] == '$' && offset+1 < len(content) && content[offset+1] == '{' {
			offset = templateInterpolationEnd(content, offset+2)
		}
	}
	return len(content)
}

func blockCommentEnd(content []byte, offset int, nested bool) int {
	start := offset
	depth := 1
	for offset+1 < len(content) {
		if nested && content[offset] == '/' && content[offset+1] == '*' {
			depth++
			offset += 2
			continue
		}
		if content[offset] == '*' && content[offset+1] == '/' {
			depth--
			if depth == 0 {
				return offset - start
			}
			offset += 2
			continue
		}
		offset++
	}
	return len(content) - start
}

func isRustLifetime(content []byte, offset int) bool {
	idx := offset + 1
	if idx == len(content) || !(content[idx] == '_' || content[idx] >= 'a' && content[idx] <= 'z' || content[idx] >= 'A' && content[idx] <= 'Z') {
		return false
	}
	for idx < len(content) && (content[idx] == '_' || content[idx] >= 'a' && content[idx] <= 'z' || content[idx] >= 'A' && content[idx] <= 'Z' || content[idx] >= '0' && content[idx] <= '9') {
		idx++
	}
	return idx == len(content) || content[idx] != '\''
}

func skipQuoted(content []byte, offset int, quote byte) int {
	offset++
	for offset < len(content) {
		if content[offset] == '\\' && quote != '`' {
			offset += 2
			continue
		}
		if offset < len(content) && content[offset] == quote {
			return offset + 1
		}
		offset++
	}
	return offset
}

func skipTripleQuoted(content []byte, offset int, escapedDelimiters bool) int {
	for offset += 3; offset+2 < len(content); offset++ {
		if !bytes.HasPrefix(content[offset:], []byte(`"""`)) {
			continue
		}
		if !escapedDelimiters || !escapedAt(content, offset) {
			return offset + 3
		}
		offset += 2
	}
	return len(content)
}

func escapedAt(content []byte, offset int) bool {
	escaped := false
	for offset--; offset >= 0 && content[offset] == '\\'; offset-- {
		escaped = !escaped
	}
	return escaped
}

func skipRustRaw(content []byte, offset int) int {
	start := offset
	if content[offset] == 'R' {
		endTag := bytes.IndexByte(content[offset+1:], '(')
		if endTag < 0 {
			return start + 1
		}
		tag := content[offset+2 : offset+1+endTag]
		end := append([]byte(")"), tag...)
		end = append(end, '"')
		match := bytes.Index(content[offset+2+endTag:], end)
		if match < 0 {
			return len(content)
		}
		return offset + 2 + endTag + match + len(end)
	}
	if content[offset] == 'b' {
		offset++
	}
	offset++
	hashes := 0
	for offset < len(content) && content[offset] == '#' {
		hashes++
		offset++
	}
	if offset == len(content) || content[offset] != '"' {
		return start + 1
	}
	end := []byte("\"" + strings.Repeat("#", hashes))
	match := bytes.Index(content[offset+1:], end)
	if match < 0 {
		return len(content)
	}
	return offset + 1 + match + len(end)
}

func hashComments(content []byte) []comment {
	return hashCommentsForPath("", content)
}

func hashCommentsForPath(path string, content []byte) []comment {
	var comments []comment
	lines := strings.Split(string(content), "\n")
	var heredocs []shellHeredoc
	triple := ""
	base := filepath.Base(path)
	hashCommentsStartLines := strings.Contains(base, "Dockerfile") ||
		base == ".gitignore" || base == ".dockerignore"
	isPython := filepath.Ext(path) == ".py"
	isTOML := filepath.Ext(path) == ".toml"
	isShell := isShellPath(path)
	var shellState shellLexState
	yamlParentIndent := -1
	yamlContentIndent := -1
	for idx, line := range lines {
		if yamlParentIndent >= 0 {
			if strings.TrimSpace(line) == "" {
				continue
			}
			indent := leadingSpaces(line)
			if yamlContentIndent < 0 && indent > yamlParentIndent {
				yamlContentIndent = indent
				continue
			}
			if yamlContentIndent >= 0 && indent >= yamlContentIndent {
				continue
			}
			yamlParentIndent = -1
			yamlContentIndent = -1
		}
		if len(heredocs) != 0 {
			candidate := strings.TrimSuffix(line, "\r")
			if heredocs[0].StripTabs {
				candidate = strings.TrimLeft(candidate, "\t")
			}
			if candidate == heredocs[0].Marker {
				heredocs = heredocs[1:]
			}
			continue
		}
		if triple != "" {
			if close := unescapedIndex(line, triple); close >= 0 {
				delimiterLength := len(triple)
				triple = ""
				if position := hashCommentStart(line[close+delimiterLength:]); position >= 0 {
					start := close + delimiterLength + position
					comments = append(comments, comment{Line: idx + 1, Text: line[start+1:]})
				}
			}
			continue
		}
		lineStartShellState := shellState
		commentStart := hashCommentStart(line)
		if hashCommentsStartLines {
			commentStart = lineHashCommentStart(line)
		}
		if isShell {
			commentStart = shellCommentStart(line, &shellState)
		}
		code := line
		if commentStart >= 0 {
			code = line[:commentStart]
		}
		if markers := shellHeredocMarkers(code, isShell, lineStartShellState); len(markers) != 0 {
			if commentStart >= 0 {
				comments = append(comments, comment{Line: idx + 1, Text: line[commentStart+1:]})
			}
			heredocs = append(heredocs, markers...)
			continue
		}
		delimiter := tripleDelimiter(code)
		if (isPython || isTOML) && delimiter != "" && strings.Count(code, delimiter) >= 2 {
			if commentStart >= 0 {
				comments = append(comments, comment{Line: idx + 1, Text: line[commentStart+1:]})
			}
			continue
		}
		if (isPython || isTOML) && delimiter != "" {
			triple = delimiter
			continue
		}
		if isYAML(path) && yamlScalar(line) {
			yamlParentIndent = leadingSpaces(line)
			yamlContentIndent = -1
		}
		if isYAML(path) {
			commentStart = yamlCommentStart(line)
		}
		if commentStart >= 0 {
			comments = append(comments, comment{Line: idx + 1, Text: line[commentStart+1:]})
		}
	}
	return comments
}

func lineHashCommentStart(line string) int {
	for idx := 0; idx < len(line); idx++ {
		if line[idx] == ' ' || line[idx] == '\t' {
			continue
		}
		if line[idx] == '#' {
			return idx
		}
		break
	}
	return -1
}

func unescapedIndex(text, needle string) int {
	for idx := 0; idx+len(needle) <= len(text); idx++ {
		if text[idx:idx+len(needle)] != needle {
			continue
		}
		escaped := false
		for previous := idx - 1; previous >= 0 && text[previous] == '\\'; previous-- {
			escaped = !escaped
		}
		if !escaped {
			return idx
		}
	}
	return -1
}

func tripleDelimiter(code string) string {
	inSingle, inDouble := false, false
	for idx := 0; idx < len(code); idx++ {
		if code[idx] == '\\' {
			idx++
			continue
		}
		if !inDouble && strings.HasPrefix(code[idx:], "'''") && !inSingle {
			return "'''"
		}
		if !inSingle && strings.HasPrefix(code[idx:], `"""`) && !inDouble {
			return `"""`
		}
		if code[idx] == '\'' && !inDouble {
			inSingle = !inSingle
		}
		if code[idx] == '"' && !inSingle {
			inDouble = !inDouble
		}
	}
	return ""
}

func isYAML(path string) bool { return filepath.Ext(path) == ".yaml" || filepath.Ext(path) == ".yml" }

func leadingSpaces(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }

func yamlScalar(line string) bool {
	value := strings.TrimSpace(line)
	if remainder, found := strings.CutPrefix(value, "-"); found {
		value = strings.TrimSpace(remainder)
	}
	if _, remainder, found := strings.Cut(value, ":"); found {
		value = remainder
	}
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">")
}

func yamlCommentStart(line string) int {
	for idx := range line {
		if line[idx] == '#' && (idx == 0 || line[idx-1] == ' ' || line[idx-1] == '\t') {
			return idx
		}
	}
	return -1
}

func isShellPath(path string) bool {
	base := filepath.Base(path)
	return filepath.Ext(path) == ".sh" || filepath.Ext(path) == ".bash" || filepath.Ext(path) == ".zsh" || base == "pre-commit" || base == "commit-msg"
}

func shellHeredocMarkers(line string, isShell bool, initialState shellLexState) []shellHeredoc {
	if !isShell {
		return nil
	}
	var heredocs []shellHeredoc
	inSingle := initialState.Quote == '\''
	inDouble := initialState.Quote == '"'
	commandDepth := initialState.CommandDepth
	resumeDoubleDepths := append([]int(nil), initialState.ResumeDoubleDepths...)
	arithmeticDepth := 0
	for offset := 0; offset+1 < len(line); {
		if line[offset] == '\\' {
			offset += 2
			continue
		}
		if line[offset] == '\'' && !inDouble {
			inSingle = !inSingle
			offset++
			continue
		}
		if line[offset] == '"' && !inSingle {
			inDouble = !inDouble
			offset++
			continue
		}
		if inDouble && line[offset] == '$' && line[offset+1] == '(' {
			inDouble = false
			commandDepth++
			resumeDoubleDepths = append(resumeDoubleDepths, commandDepth)
			offset += 2
			continue
		}
		if !inSingle && !inDouble && commandDepth > 0 && line[offset] == '(' {
			commandDepth++
			offset++
			continue
		}
		if !inSingle && !inDouble && commandDepth > 0 && line[offset] == ')' {
			closingDepth := commandDepth
			commandDepth--
			if len(resumeDoubleDepths) > 0 && resumeDoubleDepths[len(resumeDoubleDepths)-1] == closingDepth {
				inDouble = true
				resumeDoubleDepths = resumeDoubleDepths[:len(resumeDoubleDepths)-1]
			}
			offset++
			continue
		}
		if !inSingle && !inDouble && line[offset] == '(' && line[offset+1] == '(' {
			arithmeticDepth++
			offset += 2
			continue
		}
		if !inSingle && !inDouble && arithmeticDepth > 0 && line[offset] == ')' && line[offset+1] == ')' {
			arithmeticDepth--
			offset += 2
			continue
		}
		if inSingle || inDouble || arithmeticDepth > 0 || line[offset] != '<' || line[offset+1] != '<' || offset+2 < len(line) && line[offset+2] == '<' {
			offset++
			continue
		}
		heredoc, next, found := parseShellHeredoc(line, offset+2)
		if found {
			heredocs = append(heredocs, heredoc)
			offset = next
			continue
		}
		offset += 2
	}
	return heredocs
}

func shellCommentStart(line string, state *shellLexState) int {
	if state.Quote == 0 && !state.WordContinues {
		state.InWord = false
	}
	state.WordContinues = false
	for offset := 0; offset < len(line); offset++ {
		current := line[offset]
		if state.Quote == '\'' {
			if current == '\'' {
				state.Quote = 0
			}
			continue
		}
		if state.Quote == '"' {
			if current == '\\' {
				offset++
				continue
			}
			if current == '"' {
				state.Quote = 0
				continue
			}
			if current == '$' && offset+1 < len(line) && line[offset+1] == '(' {
				state.Quote = 0
				state.CommandDepth++
				state.ResumeDoubleDepths = append(state.ResumeDoubleDepths, state.CommandDepth)
				state.InWord = false
				offset++
				continue
			}
			if current == '`' {
				state.Quote = 0
				state.BacktickDepth++
				state.BacktickResumeDoubleDepths = append(state.BacktickResumeDoubleDepths, state.BacktickDepth)
				state.InWord = false
			}
			continue
		}
		if current == '\\' && offset+1 < len(line) && line[offset+1] == '`' && state.BacktickDepth > 0 {
			if state.BacktickDepth == 1 {
				state.BacktickDepth++
				state.InWord = false
			} else {
				state.BacktickDepth--
				state.InWord = true
			}
			offset++
			continue
		}
		if current == '\\' {
			state.InWord = true
			if offset+1 == len(line) {
				state.WordContinues = true
				continue
			}
			offset++
			continue
		}
		if current == '\'' || current == '"' {
			state.Quote = current
			state.InWord = true
			continue
		}
		if current == '$' && offset+1 < len(line) && line[offset+1] == '(' {
			state.CommandDepth++
			state.InWord = false
			offset++
			continue
		}
		if current == '`' {
			if state.BacktickDepth > 0 {
				closingDepth := state.BacktickDepth
				state.BacktickDepth--
				state.InWord = true
				if len(state.BacktickResumeDoubleDepths) > 0 && state.BacktickResumeDoubleDepths[len(state.BacktickResumeDoubleDepths)-1] == closingDepth {
					state.Quote = '"'
					state.BacktickResumeDoubleDepths = state.BacktickResumeDoubleDepths[:len(state.BacktickResumeDoubleDepths)-1]
				}
			} else {
				state.BacktickDepth = 1
				state.InWord = false
			}
			continue
		}
		if state.CommandDepth > 0 && current == '(' {
			state.CommandDepth++
			continue
		}
		if state.CommandDepth > 0 && current == ')' {
			closingDepth := state.CommandDepth
			state.CommandDepth--
			state.InWord = true
			if len(state.ResumeDoubleDepths) > 0 && state.ResumeDoubleDepths[len(state.ResumeDoubleDepths)-1] == closingDepth {
				state.Quote = '"'
				state.ResumeDoubleDepths = state.ResumeDoubleDepths[:len(state.ResumeDoubleDepths)-1]
			}
			continue
		}
		if current == '#' && !state.InWord {
			state.InWord = false
			state.WordContinues = false
			return offset
		}
		if current == ' ' || current == '\t' || strings.ContainsRune(";|&()<>", rune(current)) {
			state.InWord = false
			continue
		}
		state.InWord = true
	}
	return -1
}

func parseShellHeredoc(line string, offset int) (shellHeredoc, int, bool) {
	for offset < len(line) && (line[offset] == ' ' || line[offset] == '\t') {
		offset++
	}
	stripTabs := offset < len(line) && line[offset] == '-'
	if stripTabs {
		offset++
	}
	for offset < len(line) && (line[offset] == ' ' || line[offset] == '\t') {
		offset++
	}
	start := offset
	var marker strings.Builder
	quote := byte(0)
	for offset < len(line) {
		current := line[offset]
		if quote != 0 {
			if current == quote {
				quote = 0
				offset++
				continue
			}
			if current == '\\' && quote == '"' && offset+1 < len(line) {
				offset++
				current = line[offset]
			}
			marker.WriteByte(current)
			offset++
			continue
		}
		if current == '\'' || current == '"' {
			quote = current
			offset++
			continue
		}
		if current == '\\' && offset+1 < len(line) {
			offset++
			marker.WriteByte(line[offset])
			offset++
			continue
		}
		if current == ' ' || current == '\t' || strings.ContainsRune(";|&<>()", rune(current)) {
			break
		}
		marker.WriteByte(current)
		offset++
	}
	if quote != 0 || offset == start || marker.Len() == 0 {
		return shellHeredoc{}, offset, false
	}
	return shellHeredoc{Marker: marker.String(), StripTabs: stripTabs}, offset, true
}

func hashCommentStart(line string) int {
	inSingle, inDouble := false, false
	for idx := 0; idx < len(line); idx++ {
		if line[idx] == '\\' {
			idx++
			continue
		}
		if line[idx] == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if line[idx] == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if line[idx] == '#' && !inSingle && !inDouble {
			return idx
		}
	}
	return -1
}

func markupComments(content []byte) []comment {
	return markupCommentsForPath("document.md", content)
}

func markupCommentsForPath(path string, content []byte) []comment {
	var comments []comment
	fenceMarker := byte(0)
	fenceLength := 0
	fenceBlockquoteDepth := 0
	fenceListIndent := 0
	inComment := false
	inCDATA := false
	inlineCodeTicks := 0
	listIndent := 0
	commentLine := 0
	var commentText strings.Builder
	line := 1
	for offset := 0; offset < len(content); {
		end := bytes.IndexByte(content[offset:], '\n')
		if end < 0 {
			end = len(content) - offset
		}
		current := content[offset : offset+end]
		lineContentOffset := offset
		isMarkdown := filepath.Ext(path) == ".md" || filepath.Ext(path) == ".markdown"
		blockquoteDepth := 0
		if isMarkdown {
			_, blockquoteDepth = markdownBlockquoteLine(current)
			originalLength := len(current)
			if fenceMarker != 0 {
				fenceContent, inside := markdownFenceContainerLine(current, fenceBlockquoteDepth, fenceListIndent)
				if inside {
					current = fenceContent
				} else {
					fenceMarker, fenceLength = 0, 0
					fenceBlockquoteDepth = 0
					fenceListIndent = 0
					current, listIndent = markdownContainerLine(current, listIndent)
				}
			} else {
				current, listIndent = markdownContainerLine(current, listIndent)
			}
			lineContentOffset += originalLength - len(current)
		}
		marker, length, isFence := markdownFence(current)
		if isMarkdown && fenceMarker == 0 && isFence {
			fenceMarker, fenceLength = marker, length
			fenceBlockquoteDepth = blockquoteDepth
			fenceListIndent = listIndent
		} else if isMarkdown && fenceMarker != 0 && isFence && marker == fenceMarker && length >= fenceLength && fenceCloseWhitespace(current, length) {
			fenceMarker, fenceLength = 0, 0
			fenceBlockquoteDepth = 0
			fenceListIndent = 0
		} else if fenceMarker == 0 && !(isMarkdown && markdownIndentedCode(current)) {
			remaining := current
			for len(remaining) > 0 {
				if inComment {
					close := bytes.Index(remaining, []byte("-->"))
					if close < 0 {
						commentText.Write(remaining)
						commentText.WriteByte('\n')
						break
					}
					commentText.Write(remaining[:close])
					comments = append(comments, comment{Line: commentLine, Text: commentText.String()})
					commentText.Reset()
					inComment = false
					remaining = remaining[close+3:]
					continue
				}
				if inCDATA {
					close := bytes.Index(remaining, []byte("]]>"))
					if close < 0 {
						break
					}
					remaining = remaining[close+3:]
					inCDATA = false
					continue
				}
				if inlineCodeTicks != 0 {
					close := matchingBacktickRun(remaining, inlineCodeTicks)
					if close < 0 {
						break
					}
					remaining = remaining[close+inlineCodeTicks:]
					inlineCodeTicks = 0
					continue
				}
				if bytes.HasPrefix(remaining, []byte("<![CDATA[")) {
					inCDATA = true
					remaining = remaining[len("<![CDATA["):]
					continue
				}
				if isMarkdown && remaining[0] == '`' {
					runLength := backtickRunLength(remaining)
					absoluteOffset := lineContentOffset + len(current) - len(remaining)
					paragraph := markdownParagraphRemainder(content[absoluteOffset+runLength:], listIndent)
					if escapedAt(content, absoluteOffset) || matchingBacktickRun(paragraph, runLength) < 0 {
						remaining = remaining[runLength:]
						continue
					}
					inlineCodeTicks = runLength
					remaining = remaining[runLength:]
					continue
				}
				if !bytes.HasPrefix(remaining, []byte("<!--")) {
					remaining = remaining[1:]
					continue
				}
				remaining = remaining[4:]
				inComment = true
				commentLine = line
			}
		}
		offset += end + 1
		line++
	}
	return comments
}

func markdownParagraphRemainder(content []byte, listIndent int) []byte {
	firstNewline := bytes.IndexByte(content, '\n')
	if firstNewline < 0 {
		return content
	}
	for lineStart := firstNewline + 1; lineStart < len(content); {
		lineEnd := bytes.IndexByte(content[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(content) - lineStart
		}
		line := content[lineStart : lineStart+lineEnd]
		line, listIndent = markdownContainerLine(line, listIndent)
		if len(bytes.TrimSpace(line)) == 0 || markdownCommentBlockStart(line) {
			return content[:lineStart]
		}
		lineStart += lineEnd + 1
	}
	return content
}

func markdownCommentBlockStart(line []byte) bool {
	content := markdownBlockquoteContent(line)
	indentBytes, indentColumns := markdownIndent(content)
	return indentColumns <= 3 && bytes.HasPrefix(content[indentBytes:], []byte("<!--"))
}

func markdownIndentedCode(line []byte) bool {
	content := markdownBlockquoteContent(line)
	_, columns := markdownIndent(content)
	return columns >= 4
}

func markdownContainerLine(line []byte, activeListIndent int) ([]byte, int) {
	content := markdownBlockquoteContent(line)
	if itemContent, contentIndent, found := markdownListItem(content); found {
		return itemContent, contentIndent
	}
	indentBytes, indentColumns := markdownIndent(content)
	if activeListIndent > 0 && indentColumns >= activeListIndent {
		return stripMarkdownIndent(content, activeListIndent), activeListIndent
	}
	if len(bytes.TrimSpace(content)) != 0 && indentBytes == 0 {
		activeListIndent = 0
	}
	return content, activeListIndent
}

func markdownFenceContainerLine(line []byte, blockquoteDepth, listIndent int) ([]byte, bool) {
	content, found := stripMarkdownBlockquotes(line, blockquoteDepth)
	if !found {
		return nil, false
	}
	if listIndent == 0 {
		return content, true
	}
	if len(bytes.TrimSpace(content)) == 0 {
		return content, true
	}
	_, indentColumns := markdownIndent(content)
	if indentColumns < listIndent {
		return nil, false
	}
	return stripMarkdownIndent(content, listIndent), true
}

func stripMarkdownBlockquotes(line []byte, expectedDepth int) ([]byte, bool) {
	for range expectedDepth {
		indentBytes, indentColumns := markdownIndent(line)
		if indentColumns > 3 || indentBytes == len(line) || line[indentBytes] != '>' {
			return nil, false
		}
		line = line[indentBytes+1:]
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			line = line[1:]
		}
	}
	return line, true
}

func markdownListItem(line []byte) ([]byte, int, bool) {
	indentBytes, indentColumns := markdownIndent(line)
	if indentColumns > 3 || indentBytes == len(line) {
		return nil, 0, false
	}
	markerEnd := indentBytes
	if line[markerEnd] == '-' || line[markerEnd] == '+' || line[markerEnd] == '*' {
		markerEnd++
	} else {
		for markerEnd < len(line) && markerEnd-indentBytes < 9 && line[markerEnd] >= '0' && line[markerEnd] <= '9' {
			markerEnd++
		}
		if markerEnd == indentBytes || markerEnd == len(line) || line[markerEnd] != '.' && line[markerEnd] != ')' {
			return nil, 0, false
		}
		markerEnd++
	}
	if markerEnd == len(line) || line[markerEnd] != ' ' && line[markerEnd] != '\t' {
		return nil, 0, false
	}
	contentStart := markerEnd
	spaces := 0
	for contentStart < len(line) && spaces < 5 && (line[contentStart] == ' ' || line[contentStart] == '\t') {
		if line[contentStart] == '\t' {
			spaces += 4 - (indentColumns+markerEnd-indentBytes+spaces)%4
		} else {
			spaces++
		}
		contentStart++
	}
	if spaces == 0 {
		return nil, 0, false
	}
	if spaces > 4 {
		spaces = 1
		contentStart = markerEnd + 1
	}
	contentIndent := indentColumns + markerEnd - indentBytes + spaces
	return line[contentStart:], contentIndent, true
}

func stripMarkdownIndent(line []byte, targetColumns int) []byte {
	columns := 0
	offset := 0
	for offset < len(line) && columns < targetColumns {
		if line[offset] == ' ' {
			columns++
		} else if line[offset] == '\t' {
			columns += 4 - columns%4
		} else {
			break
		}
		offset++
	}
	return line[offset:]
}

func markdownBlockquoteContent(line []byte) []byte {
	content, _ := markdownBlockquoteLine(line)
	return content
}

func markdownBlockquoteLine(line []byte) ([]byte, int) {
	depth := 0
	for {
		indentBytes, indentColumns := markdownIndent(line)
		if indentColumns > 3 || indentBytes == len(line) || line[indentBytes] != '>' {
			return line, depth
		}
		depth++
		line = line[indentBytes+1:]
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			line = line[1:]
		}
	}
}

func markdownIndent(line []byte) (int, int) {
	columns := 0
	offset := 0
	for offset < len(line) {
		switch line[offset] {
		case ' ':
			columns++
		case '\t':
			columns += 4 - columns%4
		default:
			return offset, columns
		}
		offset++
	}
	return offset, columns
}

func backtickRunLength(content []byte) int {
	length := 0
	for length < len(content) && content[length] == '`' {
		length++
	}
	return length
}

func matchingBacktickRun(content []byte, expectedLength int) int {
	for offset := 0; offset < len(content); {
		if content[offset] != '`' {
			offset++
			continue
		}
		length := backtickRunLength(content[offset:])
		if length == expectedLength {
			return offset
		}
		offset += length
	}
	return -1
}

func fenceCloseWhitespace(line []byte, length int) bool {
	indent := len(line) - len(bytes.TrimLeft(line, " "))
	return strings.TrimSpace(string(line[indent+length:])) == ""
}

func markdownFence(line []byte) (byte, int, bool) {
	indent := 0
	for indent < len(line) && line[indent] == ' ' {
		indent++
	}
	if indent >= 4 || indent == len(line) {
		return 0, 0, false
	}
	marker := line[indent]
	if marker != '`' && marker != '~' {
		return 0, 0, false
	}
	length := 0
	for indent+length < len(line) && line[indent+length] == marker {
		length++
	}
	return marker, length, length >= 3
}

func sqlComments(content []byte) []comment {
	var comments []comment
	line := 1
	for offset := 0; offset < len(content); {
		if content[offset] == '\n' {
			line++
			offset++
			continue
		}
		if content[offset] == '\'' || content[offset] == '"' {
			next := skipQuoted(content, offset, content[offset])
			line += bytes.Count(content[offset:next], []byte("\n"))
			offset = next
			continue
		}
		if content[offset] == '$' {
			delimiter, found := sqlDollarDelimiter(content[offset:])
			if found {
				end := bytes.Index(content[offset+len(delimiter):], []byte(delimiter))
				if end >= 0 {
					next := offset + len(delimiter) + end + len(delimiter)
					line += bytes.Count(content[offset:next], []byte("\n"))
					offset = next
					continue
				}
			}
		}
		if offset+1 < len(content) && content[offset] == '/' && content[offset+1] == '*' {
			endLength := blockCommentEnd(content, offset+2, true)
			end := offset + 2 + endLength
			if end+1 >= len(content) || content[end] != '*' || content[end+1] != '/' {
				comments = append(comments, comment{Line: line, Text: string(content[offset+2:])})
				return comments
			}
			body := content[offset+2 : end]
			comments = append(comments, comment{Line: line, Text: string(body)})
			line += bytes.Count(body, []byte("\n"))
			offset = end + 2
			continue
		}
		if offset+1 >= len(content) || content[offset] != '-' || content[offset+1] != '-' {
			offset++
			continue
		}
		end := bytes.IndexByte(content[offset:], '\n')
		if end < 0 {
			end = len(content) - offset
		}
		comments = append(comments, comment{Line: line, Text: string(content[offset+2 : offset+end])})
		offset += end
	}
	return comments
}

func iniComments(content []byte) []comment {
	var comments []comment
	for idx, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if len(trimmed) > 0 && (trimmed[0] == '#' || trimmed[0] == ';') {
			comments = append(comments, comment{Line: idx + 1, Text: trimmed[1:]})
		}
	}
	return comments
}

func sqlDollarDelimiter(content []byte) (string, bool) {
	idx := 1
	for idx < len(content) && (content[idx] == '_' || content[idx] >= 'a' && content[idx] <= 'z' || content[idx] >= 'A' && content[idx] <= 'Z' || content[idx] >= '0' && content[idx] <= '9') {
		idx++
	}
	if idx >= len(content) || content[idx] != '$' {
		return "", false
	}
	return string(content[:idx+1]), true
}

func isSeparator(text string) bool {
	text = normalizedCommentText(text)
	if len(text) < 3 || !strings.ContainsRune("+-=*_~/#", rune(text[0])) {
		return false
	}
	for idx := 1; idx < len(text); idx++ {
		if text[idx] != text[0] {
			return false
		}
	}
	return true
}

func isSetextUnderline(commentLines []comment, commentIndex int) bool {
	if commentIndex == 0 || commentLines[commentIndex-1].Line+1 != commentLines[commentIndex].Line {
		return false
	}
	text := normalizedCommentText(commentLines[commentIndex].Text)
	if text[0] != '-' && text[0] != '=' {
		return false
	}
	previous := normalizedCommentText(commentLines[commentIndex-1].Text)
	return previous != "" && !isSeparator(previous)
}

func normalizedCommentText(text string) string {
	return strings.TrimSpace(text)
}
