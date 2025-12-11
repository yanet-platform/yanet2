#pragma once

#include <stddef.h>

// Size of the thread-local stack buffer (1 MB)
#define TLS_STACK_SIZE (1 << 20)

// Clears all data from the thread-local stack.
// This resets the stack pointer to the initial position, effectively
// discarding all pushed data.
void
tls_stack_clear();

// Pushes data onto the thread-local stack.
// The stack grows downward in memory, so this decreases the stack pointer.
//
// @param data Pointer to the data to push
// @param bytes Number of bytes to push
//
// Note: Asserts if there's insufficient space on the stack.
void
tls_stack_push(const char *data, size_t bytes);

// Pops data from the thread-local stack and returns a pointer to it.
// The stack grows downward, so this increases the stack pointer.
// The returned pointer remains valid until the next push operation.
//
// @param bytes Number of bytes to pop
// @return Pointer to the popped data
//
// Note: Asserts if attempting to pop more bytes than available.
char *
tls_stack_pop(size_t bytes);

// Returns the current size of data on the thread-local stack.
//
// @return Number of bytes currently stored on the stack
size_t
tls_stack_size();

// Returns a pointer to the current top of the thread-local stack.
// This allows reading the stack contents without popping.
//
// @return Pointer to the current stack data
char *
tls_stack_read();