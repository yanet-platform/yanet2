#include "registry.h"
#include "common/big_array.h"
#include "common/memory.h"
#include "controlplane/diag/diag.h"
#include <assert.h>
#include <netinet/in.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>

static int
vs_identifier_cmp(const void *left, const void *right);

static int
real_identifier_cmp(const void *left, const void *right);

int
service_registry_init(
	struct service_registry *registry,
	struct memory_context *mctx,
	void *elems,
	size_t elem_size,
	size_t elems_count,
	registry_cmp cmp,
	struct service_registry *prev
) {
	if (prev != NULL && prev->elem_size != elem_size) {
		NEW_ERROR("internal error: incompatible registry: "
			  "prev->elem_size != elem_size");
		return -1;
	}

	registry->elem_size = elem_size;
	registry->elems_count = elems_count;

	if (big_array_init(&registry->elems, elem_size * elems_count, mctx) !=
	    0) {
		NEW_ERROR("no memory");
		return -1;
	}

	if (big_array_init(
		    &registry->indices, elems_count * sizeof(size_t), mctx
	    ) != 0) {
		big_array_free(&registry->elems);
		NEW_ERROR("no memory");
		return -1;
	}

	registry->next_stable_index =
		prev != NULL ? prev->next_stable_index : 0;

	qsort(elems, elems_count, elem_size, cmp);

	uint8_t *elems_bytes = (uint8_t *)elems;
	for (size_t idx = 0; idx < elems_count; ++idx) {
		void *elem_ptr = elems_bytes + idx * elem_size;

		ssize_t stable_idx =
			prev != NULL
				? service_registry_lookup(prev, elem_ptr, cmp)
				: -1;
		if (stable_idx == -1) {
			stable_idx = (ssize_t)registry->next_stable_index++;
		}

		size_t stable_idx_u = (size_t)stable_idx;
		memcpy(big_array_get(&registry->elems, idx * elem_size),
		       elem_ptr,
		       elem_size);
		memcpy(big_array_get(&registry->indices, idx * sizeof(size_t)),
		       &stable_idx_u,
		       sizeof(size_t));
	}

	return 0;
}

void
service_registry_free(struct service_registry *registry) {
	big_array_free(&registry->elems);
	big_array_free(&registry->indices);
	memset(registry, 0, sizeof(*registry));
}

ssize_t
service_registry_lookup(
	struct service_registry *registry, const void *elem, registry_cmp cmp
) {
	ssize_t left = -1;
	ssize_t right = (ssize_t)registry->elems_count;
	while (left + 1 < right) {
		ssize_t idx = (left + right) / 2;

		void *elem_at_idx = big_array_get(
			&registry->elems, (size_t)idx * registry->elem_size
		);
		int cmp_res = cmp(elem, elem_at_idx);
		if (cmp_res == 0) {
			size_t stable_idx_u = *(size_t *)big_array_get(
				&registry->indices, (size_t)idx * sizeof(size_t)
			);
			return (ssize_t)stable_idx_u;
		} else if (cmp_res < 0) {
			right = idx;
		} else {
			left = idx;
		}
	}
	return -1;
}

static int
cmp_u8(uint8_t a, uint8_t b) {
	return (a > b) - (a < b);
}

static int
cmp_u16(uint16_t a, uint16_t b) {
	return (a > b) - (a < b);
}

static int
vs_identifier_cmp(const void *left, const void *right) {
	const struct vs_identifier *a = (const struct vs_identifier *)left;
	const struct vs_identifier *b = (const struct vs_identifier *)right;

	int c = cmp_u8(a->ip_proto, b->ip_proto);
	if (c != 0) {
		return c;
	}

	if (a->ip_proto == IPPROTO_IP) {
		c = memcmp(a->addr.v4.bytes, b->addr.v4.bytes, NET4_LEN);
	} else if (a->ip_proto == IPPROTO_IPV6) {
		c = memcmp(a->addr.v6.bytes, b->addr.v6.bytes, NET6_LEN);
	} else {
		/* Fallback for unexpected protocol values */
		c = memcmp(&a->addr, &b->addr, sizeof(a->addr));
	}
	if (c != 0) {
		return c;
	}

	c = cmp_u16(a->port, b->port);
	if (c != 0) {
		return c;
	}

	return cmp_u8(a->transport_proto, b->transport_proto);
}

static int
real_identifier_cmp(const void *left, const void *right) {
	const struct real_identifier *a = (const struct real_identifier *)left;
	const struct real_identifier *b = (const struct real_identifier *)right;

	int c = vs_identifier_cmp(&a->vs_identifier, &b->vs_identifier);
	if (c != 0) {
		return c;
	}

	c = cmp_u8(a->relative.ip_proto, b->relative.ip_proto);
	if (c != 0) {
		return c;
	}

	if (a->relative.ip_proto == IPPROTO_IP) {
		c =
			memcmp(a->relative.addr.v4.bytes,
			       b->relative.addr.v4.bytes,
			       NET4_LEN);
	} else if (a->relative.ip_proto == IPPROTO_IPV6) {
		c =
			memcmp(a->relative.addr.v6.bytes,
			       b->relative.addr.v6.bytes,
			       NET6_LEN);
	} else {
		c =
			memcmp(&a->relative.addr,
			       &b->relative.addr,
			       sizeof(a->relative.addr));
	}
	if (c != 0) {
		return c;
	}

	return cmp_u16(a->relative.port, b->relative.port);
}

int
vs_registry_init(
	vs_registry_t *registry,
	struct memory_context *mctx,
	struct vs_identifier *vs,
	size_t vs_count,
	vs_registry_t *prev
) {
	return service_registry_init(
		registry,
		mctx,
		vs,
		sizeof(struct vs_identifier),
		vs_count,
		vs_identifier_cmp,
		prev
	);
}

void
vs_registry_free(vs_registry_t *registry) {
	service_registry_free(registry);
}

ssize_t
vs_registry_lookup(vs_registry_t *registry, const struct vs_identifier *vs) {
	return service_registry_lookup(registry, vs, vs_identifier_cmp);
}

int
reals_registry_init(
	reals_registry_t *registry,
	struct memory_context *mctx,
	struct real_identifier *reals,
	size_t reals_count,
	reals_registry_t *prev
) {
	return service_registry_init(
		registry,
		mctx,
		reals,
		sizeof(struct real_identifier),
		reals_count,
		real_identifier_cmp,
		prev
	);
}

void
reals_registry_free(reals_registry_t *registry) {
	service_registry_free(registry);
}

ssize_t
reals_registry_lookup(
	reals_registry_t *registry, const struct real_identifier *real
) {
	return service_registry_lookup(registry, real, real_identifier_cmp);
}