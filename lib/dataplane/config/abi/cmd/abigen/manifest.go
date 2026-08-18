package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

const manifestVersionLine = "yanet-abi-manifest 4"

type entityRow struct {
	// Name is the entity's manifest key, sorted on for determinism.
	//
	// It is "type <kind> <name>" or "fn <name>".
	Name string
	// Line is the entity's full canonical descriptor.
	//
	// It is the text hashed into Hash and printed in the dump.
	Line string
	// Hash is the sha256 of Line.
	//
	// It is the value the loader compares between a plugin and the binary.
	Hash string
}

func entityHash(line string) string {
	sum := sha256.Sum256([]byte(line))
	return hex.EncodeToString(sum[:])
}

func buildEntityRows(types []typeInfo, functions []functionInfo) []entityRow {
	byName := map[string]entityRow{}
	for _, t := range types {
		line := typeLine(t)
		name := "type " + t.Kind + " " + t.Name
		byName[name] = entityRow{Name: name, Line: line, Hash: entityHash(line)}
	}

	for _, fn := range functions {
		line := functionLine(fn)
		name := "fn " + fn.Name
		byName[name] = entityRow{Name: name, Line: line, Hash: entityHash(line)}
	}

	rows := make([]entityRow, 0, len(byName))
	for _, row := range byName {
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

func buildManifestText(rows []entityRow) string {
	var b strings.Builder
	b.WriteString(manifestVersionLine)
	b.WriteByte('\n')
	for _, row := range rows {
		b.WriteString(row.Hash)
		b.WriteByte(' ')
		b.WriteString(row.Line)
		b.WriteByte('\n')
	}

	return b.String()
}

func typeLine(t typeInfo) string {
	if t.Kind == "enum" {
		return fmt.Sprintf("type enum %s size=%d {%s}", t.Name, t.ByteSize, strings.Join(enumConstants(t), ","))
	}

	fields := make([]string, 0, len(t.Members))
	for _, m := range t.Members {
		fields = append(fields, memberField(m))
	}

	return fmt.Sprintf(
		"type %s %s size=%d align=%s {%s}", t.Kind, t.Name, t.ByteSize, alignmentField(t.Alignment), strings.Join(fields, ","),
	)
}

func alignmentField(alignment int64) string {
	if alignment == 0 {
		return "natural"
	}

	return fmt.Sprintf("%d", alignment)
}

func enumConstants(t typeInfo) []string {
	values := make([]string, 0, len(t.Enumerators))
	for _, v := range t.Enumerators {
		values = append(values, fmt.Sprintf("%s=%d", v.Name, v.Value))
	}

	return values
}

func memberField(m typeMember) string {
	if m.BitSize != 0 {
		return fmt.Sprintf("%s:%s@0:%d@%d", m.Name, m.Type, m.BitSize, m.DataBitOffset)
	}

	return fmt.Sprintf("%s:%s@%d", m.Name, m.Type, m.ByteOffset)
}
