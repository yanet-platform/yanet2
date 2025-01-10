#pragma once
#include <stdlib.h>

struct data_pipe {
	volatile size_t *w_pos;
	volatile size_t *r_pos;
	volatile size_t *f_pos;

	void **data;
	size_t size;
};

// FIXME: provide w_pos, r_pos, f_pos
static inline int
data_pipe_init(struct data_pipe *pipe, size_t size) {
	pipe->size = size;

	pipe->data = (void **)malloc(sizeof(void *) * (1 << size));
	if (pipe->data == NULL)
		return -1;
	pipe->w_pos = (size_t *)malloc(128);
	if (pipe->w_pos == NULL) {
		free(pipe->data);
		return -1;
	}
	pipe->f_pos = pipe->w_pos + 1;
	pipe->r_pos = pipe->w_pos + 64 / sizeof(size_t);

	*(pipe->w_pos) = 0;
	*(pipe->f_pos) = 0;
	*(pipe->r_pos) = 0;

	return 0;
}

static inline void
data_pipe_free(struct data_pipe *pipe) {
	free(pipe->data);
	free((void *)pipe->w_pos);
}

typedef size_t (*data_pipe_handle_func)(void **item, size_t count, void *data);

// FIXME: check if we have to use memory barriers here
static inline size_t
data_pipe_ring_handle(
	volatile size_t *from_pos,
	volatile size_t *to_pos,
	void **data,
	size_t size,
	size_t space,
	data_pipe_handle_func handle_func,
	void *handle_func_data
) {
	size_t from = *from_pos;
	size_t to = *to_pos;

	size_t available = to - from + space;
	from &= (1 << size) - 1;
	/*
	 * Branch-less code: the first part is 1 if and only if we wrap
	 * around the ring size whereas the second part is size of the ring
	 * overflow.
	 */
	available -=
		((from + available) >> size) * (from + available - (1 << size));

	if (!available)
		return 0;

	size_t handled = handle_func(data + from, available, handle_func_data);

	// FIXME write fence

	*from_pos += handled;

	return handled;
}

static inline size_t
data_pipe_item_push(
	struct data_pipe *data_pipe,
	data_pipe_handle_func push_func,
	void *push_func_data
) {
	return data_pipe_ring_handle(
		data_pipe->w_pos,
		data_pipe->f_pos,
		data_pipe->data,
		data_pipe->size,
		1 << data_pipe->size,
		push_func,
		push_func_data
	);
}

static inline size_t
data_pipe_item_pop(
	struct data_pipe *data_pipe,
	data_pipe_handle_func pop_func,
	void *pop_func_data
) {
	return data_pipe_ring_handle(
		data_pipe->r_pos,
		data_pipe->w_pos,
		data_pipe->data,
		data_pipe->size,
		0,
		pop_func,
		pop_func_data
	);
}

static inline size_t
data_pipe_item_free(
	struct data_pipe *data_pipe,
	data_pipe_handle_func free_func,
	void *free_func_data
) {
	return data_pipe_ring_handle(
		data_pipe->f_pos,
		data_pipe->r_pos,
		data_pipe->data,
		data_pipe->size,
		0,
		free_func,
		free_func_data
	);
}
