#pragma once

#include "api/real.h"
#include "api/vs.h"
#include "common/big_array.h"
#include "common/memory.h"
#include <stddef.h>

typedef int (*registry_cmp)(const void *left, const void *right);

struct service_registry {
	size_t next_stable_index;

	size_t elems_count;

	size_t elem_size;
	struct big_array elems; // array of elems

	struct big_array indices; // array of size_t
};

int
service_registry_init(
	struct service_registry *registry,
	struct memory_context *mctx,
	void *elems,
	size_t elem_size,
	size_t elems_count,
	registry_cmp cmp,
	struct service_registry *prev
);

void
service_registry_free(struct service_registry *registry);

ssize_t
service_registry_lookup(
	struct service_registry *registry, const void *elem, registry_cmp cmp
);

typedef struct service_registry vs_registry_t;

int
vs_registry_init(
	vs_registry_t *registry,
	struct memory_context *mctx,
	struct vs_identifier *vs,
	size_t vs_count,
	vs_registry_t *prev
);

void
vs_registry_free(vs_registry_t *registry);

ssize_t
vs_registry_lookup(vs_registry_t *registry, const struct vs_identifier *vs);

typedef struct service_registry reals_registry_t;

int
reals_registry_init(
	reals_registry_t *registry,
	struct memory_context *mctx,
	struct real_identifier *reals,
	size_t reals_count,
	reals_registry_t *prev
);

void
reals_registry_free(reals_registry_t *registry);

ssize_t
reals_registry_lookup(
	reals_registry_t *registry, const struct real_identifier *real
);