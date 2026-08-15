#pragma once

#include <stdbool.h>
#include <stddef.h>

#include "lib/counters/counters.h"

// The longest pattern accepted, rejected rather than truncated above it.
#define COUNTER_PATTERN_MAX 512

// The most patterns in one set that may need the regex engine.
#define COUNTER_PATTERN_MAX_ENGINE 64

// The shape a counter-name pattern reduces to, only the last one an engine.
enum counter_pattern_kind {
	COUNTER_PATTERN_EXACT,
	COUNTER_PATTERN_PREFIX,
	COUNTER_PATTERN_SUFFIX,
	COUNTER_PATTERN_ANY,
	COUNTER_PATTERN_ENGINE,
};

// A compiled counter-name pattern, released with counter_pattern_free.
struct counter_pattern {
	enum counter_pattern_kind kind;
	size_t literal_len;
	char literal[COUNTER_NAME_LEN];
	void *engine;
};

// Compile a whole-name pattern in Rust regex syntax, refusing a bad one.
bool
counter_pattern_compile(
	struct counter_pattern *out,
	const char *pattern,
	char *err,
	size_t err_len
);

// Release whatever the compile allocated. Safe on a zeroed pattern.
void
counter_pattern_free(struct counter_pattern *pattern);

// Report whether name satisfies the pattern, reading COUNTER_NAME_LEN at most.
bool
counter_pattern_match(const struct counter_pattern *pattern, const char *name);

// One request's name filter, empty selecting nothing and match_all everything.
struct counter_pattern_set {
	struct counter_pattern *patterns;
	size_t count;
	bool match_all;
};

// Why a set failed to compile, the client's mistake apart from this side's.
enum counter_pattern_result {
	COUNTER_PATTERN_OK,
	COUNTER_PATTERN_REJECTED,
	COUNTER_PATTERN_NOMEM,
};

// Compile a request's patterns, leaving nothing allocated on failure.
enum counter_pattern_result
counter_pattern_set_compile(
	struct counter_pattern_set *set,
	const char *const *patterns,
	size_t count,
	char *err,
	size_t err_len
);

// Build a set that selects every name, allocating nothing.
void
counter_pattern_set_match_all(struct counter_pattern_set *set);

// Release a compiled set. Safe on a zeroed set.
void
counter_pattern_set_free(struct counter_pattern_set *set);

// Report whether name satisfies the set.
bool
counter_pattern_set_match(
	const struct counter_pattern_set *set, const char *name
);
