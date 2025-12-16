/* SPDX-License-Identifier: BSD-3-Clause
 * Copyright(c) 2010-2014 Intel Corporation
 */

#pragma once

#include <emmintrin.h>
#include <stdatomic.h>

#ifndef likely
#define likely(x) __builtin_expect(!!(x), 1)
#endif // likely

/*
 * The rwlock_t type (adapted from DPDK - mostly copy-paste from original)"
 *
 * Readers increment the counter by YANET_RWLOCK_READ (4)
 * Writers set the YANET_RWLOCK_WRITE bit when the lock is held
 *     and set the YANET_RWLOCK_WAIT bit while waiting.
 *
 * 31                 2 1 0
 * +-------------------+-+-+
 * |  readers          | | |
 * +-------------------+-+-+
 *                      ^ ^
 *                      | |
 * WRITE: lock held ----/ |
 * WAIT: writer pending --/
 */

#define YANET_RWLOCK_WAIT 0x1  /* Writer is waiting */
#define YANET_RWLOCK_WRITE 0x2 /* Writer has the lock */
#define YANET_RWLOCK_MASK (YANET_RWLOCK_WAIT | YANET_RWLOCK_WRITE)
/* Writer is waiting or has lock */
#define YANET_RWLOCK_READ 0x4 /* Reader increment */

typedef struct {
	_Atomic(int32_t) cnt;
} rwlock_t;

/**
 * Static rwlock initializer.
 */
#define YANET_RWLOCK_INITIALIZER {0}

/**
 * Acquire a read lock. Loops until the lock is held.
 *
 * @note The RW lock is not recursive, so calling this function on the same
 * lock twice without releasing it could potentially result in a deadlock
 * scenario when a write lock is involved.
 *
 * @param rwl
 *   A pointer to an rwlock structure.
 */
static inline void
rwlock_read_lock(rwlock_t *rwl) {
	int32_t x;

	while (1) {
		/*
		    // NOTE: Based on perf output, this while loop doesn't
		    // really help
			// Wait while writer is present or pending
			while (atomic_load_explicit(&rwl->cnt,
		   memory_order_relaxed) & YANET_RWLOCK_MASK) { _mm_pause();
			}
		*/

		/* Try to get read lock */
		x = atomic_fetch_add_explicit(
			    &rwl->cnt, YANET_RWLOCK_READ, memory_order_acquire
		    ) +
		    YANET_RWLOCK_READ;

		/* If no writer, then acquire was successful */
		if (likely(!(x & YANET_RWLOCK_MASK))) {
			return;
		}

		/* Lost race with writer, backout the change. */
		atomic_fetch_sub_explicit(
			&rwl->cnt, YANET_RWLOCK_READ, memory_order_relaxed
		);
	}
}

/**
 * Release a read lock.
 *
 * @param rwl
 *   A pointer to an rwlock structure.
 */
static inline void
rwlock_read_unlock(rwlock_t *rwl) {
	atomic_fetch_sub_explicit(
		&rwl->cnt, YANET_RWLOCK_READ, memory_order_release
	);
}

/**
 * Acquire a write lock. Loops until the lock is held.
 *
 * @param rwl
 *   A pointer to an rwlock structure.
 */
static inline void
rwlock_write_lock(rwlock_t *rwl) {
	int32_t x;

	while (1) {
		// x = atomic_load_explicit(&rwl->cnt, memory_order_relaxed);

		/* Wait until no readers before trying again */
		while ((x = atomic_load_explicit(
				&rwl->cnt, memory_order_relaxed
			)) > YANET_RWLOCK_WAIT) {
			_mm_pause();
		}

		/* No readers or writers? */
		if (likely(x < YANET_RWLOCK_WRITE)) {
			/* Turn off YANET_RWLOCK_WAIT, turn on
			 * YANET_RWLOCK_WRITE */
			if (atomic_compare_exchange_weak_explicit(
				    &rwl->cnt,
				    &x,
				    YANET_RWLOCK_WRITE,
				    memory_order_acquire,
				    memory_order_relaxed
			    ))
				return;
		}

		/* Turn on writer wait bit */
		if (!(x & YANET_RWLOCK_WAIT))
			atomic_fetch_or_explicit(
				&rwl->cnt,
				YANET_RWLOCK_WAIT,
				memory_order_relaxed
			);
	}
}

/**
 * Release a write lock.
 *
 * @param rwl
 *   A pointer to an rwlock structure.
 */
static inline void
rwlock_write_unlock(rwlock_t *rwl) {
	atomic_fetch_sub_explicit(
		&rwl->cnt, YANET_RWLOCK_WRITE, memory_order_release
	);
}
