#include <assert.h>
#include <netinet/in.h>
#include <stdalign.h>
#include <stdlib.h>
#include <string.h>

#include "common/ttlmap/ttlmap.h"

#include "modules/balancer/dataplane/dataplane.h"
#include "modules/balancer/dataplane/types/session.h"

#include "sessions.h"

struct move_ctx {
	struct ttlmap *dst;
	uint32_t now;
};

static int
move_session_cb(void *key, void *value, void *userdata) {
	struct move_ctx *ctx = userdata;
	struct balancer_session_state *state = value;
	struct balancer_session_id *id = key;

	struct balancer_session_state *new_state;
	ttlmap_lock_t *lock;
	int res = TTLMAP_GET(
		ctx->dst, id, &new_state, &lock, ctx->now, state->timeout
	);
	int status = TTLMAP_STATUS(res);

	if (status == TTLMAP_INSERTED || status == TTLMAP_REPLACED) {
		memcpy(new_state, state, sizeof(*state));
		ttlmap_release_lock(lock);
	} else if (status == TTLMAP_FOUND) {
		ttlmap_release_lock(lock);
	}

	return 0;
}

int
balancer_st_resize(
	struct balancer_session_table *st, size_t new_size, uint32_t now
) {
	uint32_t gen =
		atomic_load_explicit(&st->current_gen, memory_order_acquire);

	struct ttlmap *next = balancer_st_prev_map(st, gen);
	int res = TTLMAP_INIT(
		next,
		&st->mctx,
		struct balancer_session_id,
		struct balancer_session_state,
		new_size
	);
	if (res != 0) {
		return -1;
	}

	/* Begin transition. */
	struct ttlmap *cur = balancer_st_cur_map(st, gen);
	gen++;
	atomic_store_explicit(&st->current_gen, gen, memory_order_release);

	/* Migrate sessions. */
	struct move_ctx ctx = {.dst = next, .now = now};
	TTLMAP_ITER(
		cur,
		struct balancer_session_id,
		struct balancer_session_state,
		now,
		move_session_cb,
		&ctx
	);

	/* End transition. */
	gen++;
	atomic_store_explicit(&st->current_gen, gen, memory_order_release);

	TTLMAP_FREE(cur);
	return 0;
}

size_t
balancer_st_capacity(struct balancer_session_table *st) {
	uint32_t gen =
		atomic_load_explicit(&st->current_gen, memory_order_acquire);
	return ttlmap_capacity(balancer_st_cur_map(st, gen));
}

struct buf_ctx {
	struct balancer_session_entry *buf;
	int count;
};

static int
fill_buf_cb(
	struct balancer_session_id *id,
	struct balancer_session_state *state,
	void *userdata
) {
	struct buf_ctx *ctx = userdata;
	ctx->buf[ctx->count].id = *id;
	ctx->buf[ctx->count].state = *state;
	ctx->count++;
	return 0;
}

int
balancer_st_iter_next_bucket_buf(
	struct balancer_session_table_iter *iter,
	uint32_t now,
	struct balancer_session_entry *buf,
	int *count
) {
	struct buf_ctx ctx = {.buf = buf, .count = 0};
	int ret = balancer_st_iter_next_bucket(iter, now, fill_buf_cb, &ctx);
	*count = ctx.count;
	return ret;
}

void
balancer_st_iter_init(
	struct balancer_session_table_iter *iter,
	struct balancer_session_table *st
) {
	iter->gen =
		atomic_load_explicit(&st->current_gen, memory_order_acquire);
	struct ttlmap *map = balancer_st_cur_map(st, iter->gen);
	ttlmap_bucket_iter_init(&iter->ttlmap_iter, map);
}

int
balancer_st_iter_next_bucket(
	struct balancer_session_table_iter *iter,
	uint32_t now,
	balancer_st_iter_callback cb,
	void *userdata
) {
	return TTLMAP_ITER_NEXT(
		&iter->ttlmap_iter,
		struct balancer_session_id,
		struct balancer_session_state,
		now,
		cb,
		userdata
	);
}