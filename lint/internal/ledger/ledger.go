// Package ledger tracks a debt allowlist mapping violation keys to
// mandatory reasons, and reports malformed, reasonless, duplicate, and
// stale rows.
package ledger

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// reasonSeparator marks the boundary between a row's key and its mandatory
// reason, e.g. "loopindex:foo.go:Run:1  # legacy: migrate to idx".
//
// It requires two spaces before the "#" rather than treating the first bare
// "#" as the boundary, so a key that legitimately embeds a "#" — a zapmsg
// key carries the raw log message, and a message like "Worker #3 failed"
// has one — is not truncated mid-key.
const reasonSeparator = "  #"

// Entry is one parsed, well-formed row of the allowlist file.
type Entry struct {
	Line   int
	Reason string
}

// Issue is a malformed or reasonless allowlist row, reported as a linter
// failure in its own right.
type Issue struct {
	Line    int
	Message string
}

// Ledger is a parsed allowlist file: its well-formed entries keyed by
// violation key, and the malformed or reasonless rows found while parsing
// it.
type Ledger struct {
	path    string
	entries map[string]Entry
	issues  []Issue
	matched map[string]bool
}

// Load reads the allowlist file at path and returns its parsed Ledger.
//
// A missing file is not an error — it is treated as an empty ledger.
func Load(path string) (*Ledger, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Ledger{path: path, entries: map[string]Entry{}, matched: map[string]bool{}}, nil
		}
		return nil, fmt.Errorf("failed to open allowlist: %w", err)
	}
	defer file.Close()

	entries := map[string]Entry{}
	var issues []Issue

	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		trimmed := strings.TrimSpace(scanner.Text())
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		key := trimmed
		reason := ""
		if before, after, found := strings.Cut(trimmed, reasonSeparator); found {
			key = strings.TrimSpace(before)
			reason = strings.TrimSpace(after)
		}

		if !strings.Contains(key, ":") {
			issues = append(issues, Issue{
				Line:    lineNumber,
				Message: fmt.Sprintf("malformed entry %q, expected \"<path>:<name>  # <reason>\"", key),
			})
			continue
		}
		if reason == "" {
			issues = append(issues, Issue{
				Line:    lineNumber,
				Message: fmt.Sprintf("entry %s is missing a mandatory reason, add \"# <reason>\"", key),
			})
			continue
		}
		if existing, ok := entries[key]; ok {
			issues = append(issues, Issue{
				Line:    lineNumber,
				Message: fmt.Sprintf("duplicate entry %s, already defined on line %d", key, existing.Line),
			})
			continue
		}

		entries[key] = Entry{Line: lineNumber, Reason: reason}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read allowlist: %w", err)
	}

	return &Ledger{path: path, entries: entries, issues: issues, matched: map[string]bool{}}, nil
}

// Suppresses records key as a live violation and reports whether the
// ledger carries a matching entry for it.
func (m *Ledger) Suppresses(key string) bool {
	if _, ok := m.entries[key]; ok {
		m.matched[key] = true
		return true
	}
	return false
}

// Report prints every allowlist issue and every stale entry to w, and
// returns whether the run must fail.
func (m *Ledger) Report(w io.Writer) bool {
	failed := false

	for _, issue := range m.issues {
		fmt.Fprintf(w, "%s:%d: %s\n", m.path, issue.Line, issue.Message)
		failed = true
	}

	var staleKeys []string
	for key := range m.entries {
		if !m.matched[key] {
			staleKeys = append(staleKeys, key)
		}
	}
	sort.Slice(staleKeys, func(i, j int) bool {
		return m.entries[staleKeys[i]].Line < m.entries[staleKeys[j]].Line
	})
	for _, key := range staleKeys {
		fmt.Fprintf(w, "%s:%d: stale entry %s — no such violation; remove it\n", m.path, m.entries[key].Line, key)
		failed = true
	}

	return failed
}
