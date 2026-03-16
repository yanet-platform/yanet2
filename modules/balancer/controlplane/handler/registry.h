#pragma once

#include "api/real.h"
#include "api/vs.h"
#include "common/big_array.h"
#include "common/memory.h"
#include <stddef.h>

typedef int (*registry_cmp)(const void *left, const void *right);

struct registry {
	size_t next_stable_index;

	size_t elems_count;

	size_t elem_size;
	struct big_array elems; // array of elems

	struct big_array indices; // array of size_t
};

int
registry_init(
	struct registry *registry,
	struct memory_context *mctx,
	void *elems,
	size_t elem_size,
	size_t elems_count,
	registry_cmp cmp,
	struct registry *prev
);

void
registry_free(struct registry *registry);

ssize_t
registry_lookup(struct registry *registry, void *elem, registry_cmp cmp);

typedef struct registry vs_registry_t;

int
vs_registry_init(
    vs_registry_t *registry,
    struct memory_context *mctx,
    struct vs_identifier *vs,
    size_t vs_count,
    vs_registry_t *prev
);

void
vs_registry_free(
    vs_registry_t *registry
);

ssize_t
vs_registry_lookup(
    vs_registry_t *registry, struct vs_identifier *vs
);

typedef struct registry reals_registry_t;

int
reals_registry_init(
    reals_registry_t *registry,
    struct memory_context *mctx,
    struct real_identifier *reals,
    size_t reals_count,
    reals_registry_t *prev
);

void
reals_registry_free(
    reals_registry_t *registry
);

ssize_t
reals_registry_lookup(
    reals_registry_t *registry, struct real_identifier *real
);