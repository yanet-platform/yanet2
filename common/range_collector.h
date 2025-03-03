#pragma once

#include <stdint.h>

#include "common/exp_array.h"
#include "common/memory.h"

#include "key.h"
#include "lpm.h"
#include "radix.h"

struct range_collector {
	struct memory_context *memory_context;

	struct radix radix;
	uint8_t *masks;
	uint64_t mask_count;
	uint32_t count;
};

static inline int
range_collector_init(
	struct range_collector *collector, struct memory_context *memory_context
) {
	collector->memory_context = memory_context;

	if (radix_init(&collector->radix, collector->memory_context))
		return -1;
	collector->masks = NULL;
	collector->mask_count = 0;

	return 0;
}

static inline void
range_collector_free(struct range_collector *collector, uint8_t key_size) {
	memory_bfree(
		collector->memory_context,
		ADDR_OF(collector, collector->masks),
		collector->mask_count * key_size
	);
	radix_free(&collector->radix);
}

static inline int
range_collector_add_mask(
	struct range_collector *collector,
	uint8_t key_size,
	uint32_t *mask_index
) {
	uint8_t *masks = ADDR_OF(collector, collector->masks);

	if (mem_array_expand_exp(
		    collector->memory_context,
		    (void **)&masks,
		    sizeof(*masks) * key_size,
		    &collector->mask_count
	    )) {
		return -1;
	}

	memset(masks + (collector->mask_count - 1) * key_size, 0, key_size);

	collector->masks = OFFSET_OF(collector, masks);

	*mask_index = collector->mask_count - 1;
	return 0;
}

static inline void
range_collector_set_mask(
	struct range_collector *collector,
	uint8_t key_size,
	uint32_t mask_index,
	uint8_t prefix
) {
	uint32_t pos = mask_index * key_size + prefix / 8;
	ADDR_OF(collector, collector->masks)[pos] |= 0x80 >> (prefix % 8);
}

static int
range_collector_add(
	struct range_collector *collector,
	uint8_t key_size,
	const uint8_t *value,
	const uint8_t prefix
) {
	if (!prefix)
		return 0;

	uint32_t mask_index = radix_lookup(&collector->radix, key_size, value);
	if (mask_index == RADIX_VALUE_INVALID) {
		if (range_collector_add_mask(
			    collector, key_size, &mask_index
		    )) {
			return -1;
		}

		if (radix_insert(
			    &collector->radix, key_size, value, mask_index
		    )) {
			/*
			 * Mask added above leaked but this should not be
			 * an issue as the collector assumed to be freed
			 * after an error.
			 */
			return -1;
		}
	}

	range_collector_set_mask(collector, key_size, mask_index, prefix - 1);

	return 0;
}

struct range_collector_ctx {
	struct range_collector *collector;
	struct lpm *lpm;

	uint32_t max_value;
	uint32_t stack_depth;

	uint32_t *values;
	uint8_t *to;

	uint8_t *pos;
};

struct range_collector_stack_item {
	uint32_t *value;
	uint8_t *to;
};

static inline struct range_collector_stack_item
range_collector_stack_last(struct range_collector_ctx *ctx, uint8_t key_size) {
	return (struct range_collector_stack_item
	){ctx->values + (ctx->stack_depth - 1),
	  ctx->to + (ctx->stack_depth - 1) * key_size};
}

static inline void
range_collector_stack_push(
	struct range_collector_ctx *ctx, uint8_t key_size, const uint8_t *to
) {
	if (ctx->stack_depth > 0) {
		struct range_collector_stack_item item =
			range_collector_stack_last(ctx, key_size);
		if (filter_key_cmp(key_size, to, item.to) == 0) {
			*item.value = LPM_VALUE_INVALID;
			return;
		}
	}

	++ctx->stack_depth;
	struct range_collector_stack_item item =
		range_collector_stack_last(ctx, key_size);
	*item.value = LPM_VALUE_INVALID;
	memcpy(item.to, to, key_size);
}

static inline int
range_collector_stack_emit(
	struct range_collector_ctx *ctx, uint8_t key_size, const uint8_t *to
) {
	struct range_collector_stack_item item =
		range_collector_stack_last(ctx, key_size);

	if (*item.value == LPM_VALUE_INVALID)
		*item.value = ctx->max_value++;

	if (lpm_insert(ctx->lpm, key_size, ctx->pos, to, *item.value))
		return -1;

	memcpy(ctx->pos, to, key_size);
	filter_key_inc(key_size, ctx->pos);

	return 0;
}

static inline int
range_collector_stack_emit_until(
	struct range_collector_ctx *ctx, uint8_t key_size, const uint8_t *to
) {
	while (ctx->stack_depth) {
		struct range_collector_stack_item item =
			range_collector_stack_last(ctx, key_size);
		if (filter_key_cmp(key_size, item.to, to) < 0) {
			if (range_collector_stack_emit(ctx, key_size, item.to))
				return -1;
			--ctx->stack_depth;
		} else if (filter_key_cmp(key_size, ctx->pos, to) < 0) {
			uint8_t emit_to[key_size];
			memcpy(emit_to, to, key_size);
			filter_key_dec(key_size, emit_to);
			return range_collector_stack_emit(
				ctx, key_size, emit_to
			);
		} else {
			break;
		}
	}

	return 0;
}

static int
range_collector_add_network(
	uint8_t key_size,
	const uint8_t *from,
	const uint8_t *to,
	struct range_collector_ctx *ctx
) {
	if (range_collector_stack_emit_until(ctx, key_size, from))
		return -1;
	range_collector_stack_push(ctx, key_size, to);

	return 0;
}

static int
range_collector_iterate(
	uint8_t key_size, const uint8_t *from, uint32_t value, void *data
) {
	struct range_collector_ctx *ctx = (struct range_collector_ctx *)data;

	const uint8_t *mask = ADDR_OF(ctx->collector, ctx->collector->masks) +
			      value * key_size;
	uint8_t to[key_size];

	for (uint8_t idx = 0; idx < key_size; ++idx) {
		uint8_t mask_item = mask[idx];
		while (mask_item) {
			uint8_t prefix =
				idx * 8 + (__builtin_clzll(mask_item) - 56) + 1;
			filter_key_apply_prefix(key_size, from, to, prefix);
			mask_item ^= 0x01 << (7 - (prefix - 1) % 8);
			if (range_collector_add_network(
				    key_size, from, to, ctx
			    ))
				return -1;
		}
	}

	return 0;
}

static int
range_collector_collect(
	struct range_collector *collector, uint8_t key_size, struct lpm *lpm64
) {
	struct range_collector_ctx ctx;
	ctx.collector = collector;
	ctx.max_value = 0;

	ctx.lpm = lpm64;

	uint32_t stack_size = key_size * 8 + 1;
	uint32_t values[stack_size];
	uint8_t to[key_size * stack_size];
	ctx.stack_depth = 0;
	ctx.values = values;
	ctx.to = to;

	uint8_t pos[key_size];
	memset(pos, 0, key_size);
	ctx.pos = pos;

	uint8_t to_any[key_size];
	memset(to_any, 0xff, key_size);
	range_collector_stack_push(&ctx, key_size, to_any);

	if (radix_walk(
		    &collector->radix, key_size, range_collector_iterate, &ctx
	    ))
		goto error;

	while (ctx.stack_depth > 0) {
		struct range_collector_stack_item item =
			range_collector_stack_last(&ctx, key_size);
		if (range_collector_stack_emit(&ctx, key_size, item.to))
			goto error;
		--ctx.stack_depth;
	}

	collector->count = ctx.max_value;

	return 0;

error:

	return -1;
}

static inline int
range8_collector_add(
	struct range_collector *collector, const uint8_t *from, uint8_t prefix
) {
	return range_collector_add(collector, 8, from, prefix);
}

static inline int
range8_collector_collect(struct range_collector *collector, struct lpm *lpm) {
	return range_collector_collect(collector, 8, lpm);
}

static inline int
range4_collector_add(
	struct range_collector *collector, const uint8_t *from, uint8_t prefix
) {
	return range_collector_add(collector, 4, from, prefix);
}

static inline int
range4_collector_collect(struct range_collector *collector, struct lpm *lpm) {
	return range_collector_collect(collector, 4, lpm);
}
