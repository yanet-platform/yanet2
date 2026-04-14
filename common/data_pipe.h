#pragma once

#include <stdatomic.h>
#include <stdlib.h>

// TODO: move to project constants, since cache line size is not always 64.
#ifndef DATA_PIPE_CACHE_LINE_SIZE
#define DATA_PIPE_CACHE_LINE_SIZE 64
#endif

// Lock-free SPSC ring buffer.
//
// Used to pass data (typically packet pointers) between dataplane workers
// running on different cores.
//
// The ring has (1 << size) slots.
//
//
// Three-phase protocol
// ====================
//
// Each slot goes through three stages in a cycle:
//
//   1. push  (w_pos advances) - producer places items into the ring.
//   2. pop   (r_pos advances) - consumer takes items and may modify them
//                               in-place (e.g. write tx_result).
//   3. free  (f_pos advances) - producer reclaims slots and observes any
//                               modifications made by the consumer.
//
// This gives a request-response pattern over a ring buffer:
//   push item -> remote side processes it -> collect result.
//
//          f_pos          r_pos          w_pos
//            |              |              |
//            v              v              v
//   +--------+--------------+--------------+--------+
//   |  free  |   consumed   |    pushed    |  free  |
//   +--------+--------------+--------------+--------+
//            |              |              |
//            |   pop done,  | ready to pop |
//            | ready to free|              |
//            |              |              |
//        free moves     pop moves      push moves
//         right -->      right -->      right -->
//
// Position invariant (modulo wrap):  f_pos <= r_pos <= w_pos
//
// Available slots per operation:
//   push:  ring_size - (w_pos - f_pos)   (free slots to write into)
//   pop:   w_pos - r_pos                 (items ready to consume)
//   free:  r_pos - f_pos                 (items consumed, ready to reclaim)
//
//
// Memory layout
// =============
//
// w_pos and f_pos are placed in the same cache line (both accessed by the
// producer).
// r_pos is DATA_PIPE_CACHE_LINE_SIZE bytes away on a separate cache line to
// avoid false sharing with the consumer.
//
//
// Callback pattern
// ================
//
// All operations take a data_pipe_handle_func callback. The callback receives
// a pointer directly into the ring buffer and the number of contiguous
// available slots; it returns how many slots it actually processed.
//
//
// Memory ordering
// ===============
//
// from_pos (owned side) is loaded with relaxed ordering.
// to_pos (remote side) is loaded with acquire to see the remote writes.
//
// After the callback, from_pos is stored with release so the remote side sees
// the data written by the callback.
struct data_pipe {
	_Atomic size_t *w_pos; // write position (producer)
	_Atomic size_t *r_pos; // read position (consumer), separate cache line
	_Atomic size_t *f_pos; // free position (producer), same line as w_pos

	void **data; // slot array, (1 << size) entries
	size_t size; // log2 of the ring capacity
};

// Initialize a data pipe.
//
// Returns 0 on success, -1 on error.
//
// FIXME: provide w_pos, r_pos, f_pos
static inline int
data_pipe_init(struct data_pipe *pipe, size_t size) {
	pipe->size = size;

	pipe->data = (void **)malloc(sizeof(void *) * (1 << size));
	if (pipe->data == NULL) {
		return -1;
	}
	pipe->w_pos = (_Atomic size_t *)malloc(2 * DATA_PIPE_CACHE_LINE_SIZE);
	if (pipe->w_pos == NULL) {
		free(pipe->data);
		return -1;
	}
	pipe->f_pos = pipe->w_pos + 1;
	pipe->r_pos = (_Atomic size_t *)((char *)pipe->w_pos +
					 DATA_PIPE_CACHE_LINE_SIZE);

	atomic_store_explicit(pipe->w_pos, 0, memory_order_relaxed);
	atomic_store_explicit(pipe->f_pos, 0, memory_order_relaxed);
	atomic_store_explicit(pipe->r_pos, 0, memory_order_relaxed);

	return 0;
}

// Free resources allocated by data_pipe_init().
static inline void
data_pipe_free(struct data_pipe *pipe) {
	free(pipe->data);
	free(pipe->w_pos);
}

// Callback type for ring buffer operations.
//
// Receives a pointer into the ring buffer, the number of available slots, and
// an opaque user context.
//
// Returns how many slots were actually processed (must be <= count).
typedef size_t (*data_pipe_handle_func)(void **item, size_t count, void *data);

// Core ring buffer handler.
//
// Computes available = to - from + space, then clips to contiguous slots
// before the ring boundary.
//
// Invokes handle_func on the available range and advances from_pos.
//
// space is ring_size for push (counts free slots) and 0 for pop/free.
static inline size_t
data_pipe_ring_handle(
	_Atomic size_t *from_pos,
	_Atomic size_t *to_pos,
	void **data,
	size_t size,
	size_t space,
	data_pipe_handle_func handle_func,
	void *handle_func_data
) {
	size_t from = atomic_load_explicit(from_pos, memory_order_relaxed);
	size_t to = atomic_load_explicit(to_pos, memory_order_acquire);

	size_t available = to - from + space;
	size_t masked_from = from & ((1 << size) - 1);
	// Branchless code: the first part is 1 if and only if we wrap around
	// the ring size whereas the second part is size of the ring overflow.
	available -= ((masked_from + available) >> size) *
		     (masked_from + available - (1 << size));

	if (!available) {
		return 0;
	}

	size_t handled =
		handle_func(data + masked_from, available, handle_func_data);

	atomic_store_explicit(from_pos, from + handled, memory_order_release);

	return handled;
}

// Push items into the ring.
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

// Pop items from the ring.
//
// The callback may modify items in-place; those modifications become visible
// to the producer during the subsequent free phase.
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

// Reclaim consumed slots.
//
// The callback can observe modifications made by the consumer during pop.
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
