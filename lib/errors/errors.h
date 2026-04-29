#pragma once

#include <stdarg.h>

// Error type.
//
// Functions that can fail and that wishes to set an extended error message
// should accept `yanet_error **` as a last out-parameter.
//
// If such function does not fail, it should leave the error pointer unchanged.
struct yanet_error;

typedef struct yanet_error yanet_error;

// Frees the whole chain.
//
// No-op on NULL and on the out-of-memory singleton.
void
yanet_error_free(yanet_error *err);

// Returns the message of this frame (not the whole chain).
const char *
yanet_error_message(const yanet_error *err);

// Returns the wrapped cause, or NULL if `err` is a leaf.
const yanet_error *
yanet_error_cause(const yanet_error *err);

// Formats the chain as a heap-allocated C string.
char *
yanet_error_format(const yanet_error *err);

// Adds an error frame to an error chain in-place.
//
// This is the preferred helper for functions that report errors via a
// `yanet_error **` out-parameter.
//
// Behavior:
// - If `err == NULL`, this function does nothing.
// - If `err != NULL` and `*err == NULL`, a new leaf error is created.
// - If `err != NULL` and `*err != NULL`, the existing error is wrapped with
//   a new frame that prepends additional context.
//
// This makes the function suitable both for:
// - creating a root error at the point where a failure happens, and
// - adding context while propagating an existing error upward.
//
// Notes:
// - Ownership of the resulting chain remains with the caller.
// - Callers should normally pass a pointer to a variable initialized to NULL
//   for a single logical operation.
// - Reusing a non-NULL error across unrelated operations will extend the
//   existing chain instead of replacing it.
void
yanet_error_add(yanet_error **err, const char *fmt, ...)
	__attribute__((format(printf, 2, 3)));

// Frees the current error chain stored in `*err` and resets it to NULL.
//
// Behavior:
// - If `err == NULL`, this function does nothing.
// - If `err != NULL` and `*err == NULL`, this function does nothing.
// - Otherwise, the whole error chain is freed and `*err` is set to NULL.
//
// Use this before reusing the same `yanet_error *` variable for a new,
// unrelated operation.
void
yanet_error_reset(yanet_error **err);
