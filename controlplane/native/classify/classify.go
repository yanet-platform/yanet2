//go:build linux && (amd64 || arm64)

// Package classify compiles the attributes of a ruleset into the
// classifier tables the dataplane reads, as lib/classify does, without
// cgo.
//
// Only the artefacts a consumer embeds in its configuration go to shared
// memory, in C's structures and under C's memory-context names, so C's
// lookups read them and C's frees release them. The groups of equal
// rules, the region refinement and the stage a join consumes live in the
// Go heap; C keeps them in shared memory, the stage until its caller
// frees it. Classes are numbered as C numbers them, so every key maps to
// C's class; the bytes of the tables need not match C's.
//
// A compile refuses input C cannot take before it allocates anything; a
// commit that runs out of shared memory frees what it took and leaves its
// artefact zeroed, as C's error paths do.
package classify

import "errors"

// ErrInvalidRule reports a ruleset the compile cannot take.
var ErrInvalidRule = errors.New("invalid classifier rule")

// InvalidGroup marks a rule outside the projection a ruleset spells
// (FILTER_GROUP_INVALID).
const InvalidGroup = ^uint32(0)
