#pragma once

#include <stdbool.h>

#include "common/tls_stack/stack.h"
#include <stdio.h>
#include <string.h>

// Diagnostic structure for error handling.
// Stores error state and message for an operation.
struct diag {
	bool has_error;      // Whether an error has occurred
	const char *error;   // Heap-allocated error message, or NULL
};

// Fills the diagnostic structure with error information from the thread-local stack.
// Allocates memory for the error message and copies it from the TLS stack.
//
// @param diag Pointer to the diagnostic structure to fill
//
// Note: If malloc fails, sets has_error=true but leaves error=NULL.
//       The caller should check errno for ENOMEM in diag_msg().
void
diag_fill(struct diag *diag);

// Returns the error message from the diagnostic structure.
// Does not modify the diagnostic state.
//
// @param diag Pointer to the diagnostic structure
// @return Error message string, or NULL if no error.
//         Sets errno=ENOMEM and returns NULL if malloc failed during diag_fill.
const char *
diag_msg(struct diag *diag);

// Takes ownership of the error message from the diagnostic structure.
// Returns the error message and clears the diagnostic state.
// The caller is responsible for freeing the returned string.
//
// @param diag Pointer to the diagnostic structure
// @return Heap-allocated error message that must be freed by caller,
//         or NULL if no error. Sets errno=ENOMEM if malloc failed.
const char *
diag_take_msg(struct diag *diag);

// Resets the diagnostic structure, freeing any allocated error message.
//
// @param diag Pointer to the diagnostic structure to reset
void
diag_reset(struct diag *diag);

// Creates a new error by clearing the TLS stack and pushing a formatted message.
// This starts a new error chain.
//
// Usage: NEW_ERROR("Failed to open file: %s", filename);
#define NEW_ERROR(...)                                                         \
	do {                                                                   \
		char __buffer[1024];                                           \
		sprintf(__buffer, ##__VA_ARGS__);                              \
		tls_stack_clear();                                             \
		tls_stack_push(__buffer, strlen(__buffer) + 1);                \
	} while (0)

// Pushes additional context onto an existing error chain.
// Adds a formatted message with ": " separator to the TLS stack.
//
// Usage: PUSH_ERROR("In function %s", __func__);
//
// Example error chain:
//   NEW_ERROR("File not found");
//   PUSH_ERROR("Failed to load config");
//   PUSH_ERROR("Initialization failed");
// Results in: "Initialization failed: Failed to load config: File not found"
#define PUSH_ERROR(fmt, ...)                                                   \
	do {                                                                   \
		char __buffer[1024];                                           \
		sprintf(__buffer, fmt ": ", ##__VA_ARGS__);                    \
		tls_stack_push(__buffer, strlen(__buffer));                    \
	} while (0)
