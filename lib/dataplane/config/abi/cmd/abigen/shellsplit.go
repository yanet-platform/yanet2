package main

import (
	"fmt"
	"strings"
)

func shellSplit(s string) ([]string, error) {
	var tokens []string
	var cur strings.Builder
	inToken := false
	idx := 0
	n := len(s)
	for idx < n {
		c := s[idx]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			if inToken {
				tokens = append(tokens, cur.String())
				cur.Reset()
				inToken = false
			}

			idx++
		case c == '\'':
			inToken = true
			idx++
			start := idx
			for idx < n && s[idx] != '\'' {
				idx++
			}

			if idx >= n {
				return nil, fmt.Errorf("failed to split command: unterminated single quote")
			}

			cur.WriteString(s[start:idx])
			idx++
		case c == '"':
			inToken = true
			idx++
			for idx < n && s[idx] != '"' {
				if s[idx] == '\\' && idx+1 < n && strings.ContainsRune(`"\$`+"`", rune(s[idx+1])) {
					cur.WriteByte(s[idx+1])
					idx += 2
					continue
				}

				cur.WriteByte(s[idx])
				idx++
			}

			if idx >= n {
				return nil, fmt.Errorf("failed to split command: unterminated double quote")
			}

			idx++
		case c == '\\' && idx+1 < n:
			inToken = true
			cur.WriteByte(s[idx+1])
			idx += 2
		default:
			inToken = true
			cur.WriteByte(c)
			idx++
		}
	}

	if inToken {
		tokens = append(tokens, cur.String())
	}

	return tokens, nil
}
