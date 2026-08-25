#pragma once

#include <stdbool.h>
#include <stdint.h>

// One-shot cooperative cancellation request shared between a caller and
// the thread executing a long operation on its behalf.
//
// The flag is raised at most once and is never reset, so a token serves
// exactly one operation.
struct cancellation_token {
	uint32_t cancelled;
};

// Safe from any thread, including while the bound thread polls the same
// token. The token must exist.
void
cancellation_token_fire(struct cancellation_token *token);

// Binds a token to the calling thread and returns the binding it
// replaced; NULL unbinds.
//
// Long operations poll the binding of their own thread instead of
// receiving a token through every signature. Restoring the returned
// binding on the way out keeps an operation nested inside another from
// disarming the outer one.
struct cancellation_token *
cancellation_token_bind(struct cancellation_token *token);

// Reports whether the token bound to the calling thread was raised.
//
// A thread with no binding is never cancelled. Giving up on a raise is
// only safe where nothing has been published and no lock is held: the
// shared-memory locks have no owner-death recovery, and a generation the
// workers can already reach cannot be withdrawn.
bool
cancellation_requested(void);
