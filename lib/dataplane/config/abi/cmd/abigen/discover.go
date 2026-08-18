package main

import (
	"fmt"
	"path"
	"sort"
)

var defaultTUPatterns = []string{
	"modules/*/dataplane/*.c",
	"devices/*/dataplane/*.c",
	"dataplane/*.c",
	"dataplane/drivers/*.c",
	"lib/controlplane/agent/agent.c",
	"lib/controlplane/config/*.c",
	"lib/counters/counters.c",
	"lib/counters/histogram.c",
	"lib/dataplane/config/*.c",
	"lib/dataplane/module/*.c",
	"lib/dataplane/packet/*.c",
	"lib/dataplane/pipeline/*.c",
	"lib/dataplane/time/*.c",
	"lib/dataplane/worker/*.c",
	"lib/errors/*.c",
	"lib/filter/compiler/*.c",
	"lib/fwstate/*.c",
	"lib/logging/*.c",
	"lib/utils/packet.c",
}

type tuCandidate struct {
	// Rel is the TU's path relative to repoRoot, matched against patterns.
	Rel string
	// Entry is the compile database entry returned once Rel matches.
	Entry compileEntry
}

func discoverTUs(db compileDB, repoRoot string, patterns []string) ([]compileEntry, error) {
	candidates := make([]tuCandidate, 0, len(db))
	for _, entry := range db {
		rel, ok := repoRelative(resolveTUPath(entry), repoRoot)
		if !ok {
			continue
		}

		candidates = append(candidates, tuCandidate{Rel: rel, Entry: entry})
	}

	matched := map[string]compileEntry{}
	for _, pattern := range patterns {
		matchCount := 0
		for _, c := range candidates {
			ok, err := path.Match(pattern, c.Rel)
			if err != nil {
				return nil, fmt.Errorf("failed to evaluate TU pattern %q: %w", pattern, err)
			}

			if ok {
				matched[c.Rel] = c.Entry
				matchCount++
			}
		}

		if matchCount == 0 {
			return nil, fmt.Errorf("TU pattern %q matched no entries in the compile database", pattern)
		}
	}

	rels := make([]string, 0, len(matched))
	for rel := range matched {
		rels = append(rels, rel)
	}

	sort.Strings(rels)
	entries := make([]compileEntry, 0, len(rels))
	for _, rel := range rels {
		entries = append(entries, matched[rel])
	}

	return entries, nil
}
