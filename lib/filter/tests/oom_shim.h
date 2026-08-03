#pragma once

// Allocation-failure injector for the filter-compiler OOM sweep test.
//
// Force-included ahead of every shimmed translation unit (see the
// filter_compiler_oom_shim library and the oom_sweep executable in
// meson.build) so every memory_balloc call on the compile path routes
// through a counter that can be made to fail at an exact, armed call
// index.
//
// The real header is included first, so its static inline memory_balloc
// definition is fully parsed before the macro below exists. Dropping this
// include would let the force-included macro rewrite that real definition
// itself into a syntax error, which is why the redefinition stays scoped
// to call sites rather than the definition itself.
#include "common/memory.h"

// Armed by the sweep driver around one filter_init call at a time, so a
// fixture's own setup allocations are never subject to injection.
extern int filter_test_oom_armed;

// -1 disables injection while still counting, which is how the sweep
// driver learns how many memory_balloc calls a clean compile makes.
extern long filter_test_oom_fail_at;
extern long filter_test_oom_calls;

static inline void *
filter_test_balloc(struct memory_context *context, size_t size) {
	if (!filter_test_oom_armed) {
		return (memory_balloc)(context, size);
	}

	if (filter_test_oom_calls++ == filter_test_oom_fail_at) {
		return NULL;
	}

	return (memory_balloc)(context, size);
}

#define memory_balloc(context, size) filter_test_balloc((context), (size))

// The real memory_brealloc body was parsed before this header existed, so it
// still calls the real memory_balloc directly and stays outside the counter
// above. Shimming it here too keeps a growing realloc injectable and counted,
// which is how mem_array_expand_exp reaches the compile path.
static inline void *
filter_test_brealloc(
	struct memory_context *context,
	void *data,
	size_t old_size,
	size_t new_size
) {
	// A zero new_size only frees or no-ops, so it never allocates and is
	// not an injection point.
	if (new_size != 0 && filter_test_oom_armed &&
	    filter_test_oom_calls++ == filter_test_oom_fail_at) {
		return NULL;
	}

	return (memory_brealloc)(context, data, old_size, new_size);
}

#define memory_brealloc(context, data, old_size, new_size)                     \
	filter_test_brealloc((context), (data), (old_size), (new_size))
