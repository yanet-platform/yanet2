#include "cancellation.h"

#include <stddef.h>

static __thread struct cancellation_token *bound_token = NULL;

// Relaxed ordering suffices here and on the matching load: the flag is a
// monotonic one-shot signal that publishes no data of its own.
void
cancellation_token_fire(struct cancellation_token *token) {
	__atomic_store_n(&token->cancelled, 1, __ATOMIC_RELAXED);
}

struct cancellation_token *
cancellation_token_bind(struct cancellation_token *token) {
	struct cancellation_token *previous = bound_token;
	bound_token = token;
	return previous;
}

bool
cancellation_requested(void) {
	return bound_token != NULL &&
	       __atomic_load_n(&bound_token->cancelled, __ATOMIC_RELAXED);
}
