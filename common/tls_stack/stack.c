#include "stack.h"
#include <assert.h>
#include <string.h>
#include <threads.h>

struct tls_stack {
	size_t ptr;
	char data[TLS_STACK_SIZE];
};

static thread_local struct tls_stack stack = {TLS_STACK_SIZE, .data = {0}};

void
tls_stack_clear() {
	tls_stack_pop(tls_stack_size());
}

void
tls_stack_push(const char *data, size_t bytes) {
	assert(bytes <= stack.ptr);
	stack.ptr -= bytes;
	memcpy(&stack.data[stack.ptr], data, bytes);
}

char *
tls_stack_pop(size_t bytes) {
	assert(stack.ptr + bytes <= TLS_STACK_SIZE);
	char *res = &stack.data[stack.ptr];
	stack.ptr += bytes;
	return res;
}

size_t
tls_stack_size() {
	return TLS_STACK_SIZE - stack.ptr;
}

char *
tls_stack_read() {
	return &stack.data[stack.ptr];
}