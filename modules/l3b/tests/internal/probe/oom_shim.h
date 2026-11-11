#pragma once

// Parse the real allocator before redirecting compiler and inline call sites.
#include "common/memory.h"

struct l3b_probe_state {
	int fail_ip6_decode;
	int in_ip6_decode;
	int ip4_decode_succeeded;
	int ip6_decode_entered;
	int injected_allocations;
	// Decoder entries of the construction in flight; the ordinal is the
	// address family, so the first is IPv4 and the second IPv6.
	int decode_calls;
};

// Only one synchronous call on the current thread can expose this stack state.
extern _Thread_local struct l3b_probe_state *l3b_probe_active;

static inline int
l3b_probe_fail_allocation(void) {
	struct l3b_probe_state *state = l3b_probe_active;
	if (state != NULL && state->fail_ip6_decode && state->in_ip6_decode &&
	    state->injected_allocations == 0) {
		++state->injected_allocations;
		return 1;
	}
	return 0;
}

static inline void *
l3b_probe_balloc(struct memory_context *context, size_t size) {
	if (size != 0 && l3b_probe_fail_allocation()) {
		return NULL;
	}
	return (memory_balloc)(context, size);
}

static inline void *
l3b_probe_brealloc(
	struct memory_context *context,
	void *data,
	size_t old_size,
	size_t new_size
) {
	if (new_size > old_size && l3b_probe_fail_allocation()) {
		return NULL;
	}
	return (memory_brealloc)(context, data, old_size, new_size);
}

#define memory_balloc(context, size) l3b_probe_balloc((context), (size))
#define memory_brealloc(context, data, old_size, new_size)                     \
	l3b_probe_brealloc((context), (data), (old_size), (new_size))
