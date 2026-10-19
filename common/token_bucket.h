#pragma once

#include <assert.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <stdint.h>

#include "common/cache.h"

// Shared best-effort token bucket using a virtual finish time.
//
// Active reservations use relaxed fetch-add; idle restarts use one strong CAS.
// Late active refusals refund their cost; refunds can move virtual time
// backwards and cross an idle restart. Idle CAS losers pass uncharged, and
// stale active tickets can pass one cost beyond the cap. There are no retries
// or fairness guarantees, nor strict burst or wall-clock rate bounds. Modular
// arithmetic requires clock differences and outstanding debt to stay below 2^63
// ticks.
struct token_bucket {
	_Alignas(YANET_CACHE_LINE_SIZE) _Atomic uint64_t edt;
	uint64_t cost;
	uint64_t cap;
};

// Anchor fresh credit to the callers' clock domain, including near wrap.
static inline void
token_bucket_init(
	struct token_bucket *bucket, uint64_t cost, uint64_t burst, uint64_t now
) {
	assert(cost != 0 && burst != 0);
	assert(cost <= UINT64_MAX / 2);
	assert(burst <= (UINT64_MAX / 2 - cost) / cost);
	atomic_init(&bucket->edt, now);
	bucket->cost = cost;
	bucket->cap = cost * burst;
}

static inline bool
token_bucket_full_at(
	const struct token_bucket *bucket, uint64_t now, uint64_t edt
) {
	uint64_t debt = edt - now;
	return debt <= UINT64_MAX / 2 && debt > bucket->cap - bucket->cost;
}

// A full check is read-only, so exhausted queues do not add reservation debt.
static inline bool
token_bucket_full(const struct token_bucket *bucket, uint64_t now) {
	return token_bucket_full_at(
		bucket,
		now,
		atomic_load_explicit(&bucket->edt, memory_order_relaxed)
	);
}

static inline bool
token_bucket_reserve(struct token_bucket *bucket, uint64_t now, uint64_t edt) {
	if (token_bucket_full_at(bucket, now, edt)) {
		return false;
	}
	if (edt - now > UINT64_MAX / 2) {
		atomic_compare_exchange_strong_explicit(
			&bucket->edt,
			&edt,
			now + bucket->cost,
			memory_order_relaxed,
			memory_order_relaxed
		);
		return true;
	}
	uint64_t previous = atomic_fetch_add_explicit(
		&bucket->edt, bucket->cost, memory_order_relaxed
	);
	if (previous - now <= bucket->cap) {
		return true;
	}
	atomic_fetch_sub_explicit(
		&bucket->edt, bucket->cost, memory_order_relaxed
	);
	return false;
}

static inline bool
token_bucket_take(struct token_bucket *bucket, uint64_t now) {
	return token_bucket_reserve(
		bucket,
		now,
		atomic_load_explicit(&bucket->edt, memory_order_relaxed)
	);
}
