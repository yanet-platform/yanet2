#pragma once

#include <stdbool.h>

#include "common/tls_stack/stack.h"
#include <stdio.h>
#include <string.h>

struct diag {
	bool has_error;
	const char *error;
};

void
diag_fill(struct diag *diag);

const char *
diag_msg(struct diag *diag);

const char *
diag_take_msg(struct diag *diag);

void
diag_reset(struct diag *diag);

#define NEW_ERROR(...)                                                         \
	do {                                                                   \
		char __buffer[1024];                                           \
		sprintf(__buffer, ##__VA_ARGS__);                              \
		tls_stack_clear();                                             \
		tls_stack_push(__buffer, strlen(__buffer) + 1);                \
	} while (0)

#define PUSH_ERROR(fmt, ...)                                                   \
	do {                                                                   \
		char __buffer[1024];                                           \
		sprintf(__buffer, fmt ": ", ##__VA_ARGS__);                    \
		tls_stack_push(__buffer, strlen(__buffer));                    \
	} while (0)
