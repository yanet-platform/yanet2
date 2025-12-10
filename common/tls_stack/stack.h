#pragma once

#include <stddef.h>

#define TLS_STACK_SIZE (1 << 20)

void
tls_stack_clear();

void
tls_stack_push(const char *data, size_t bytes);

char *
tls_stack_pop(size_t bytes);

size_t
tls_stack_size();

char *
tls_stack_read();