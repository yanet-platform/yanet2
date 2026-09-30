/*
 * Benchmark that compares the old pdump writer with the ring writer.
 *
 * Each record size runs in three ways: the writer alone, the writer with
 * one reader per worker, and the writer paced to a fixed record rate. The
 * ring writer uses a publish batch of 1, 8 or 32 records. Its reader reads
 * up to the published write position. The old writer and its reader are
 * the baseline. The old writer comes from the pdump module, which still
 * owns its rings. This comparison goes away once pdump writes to this ring.
 *
 * Setup. The metadata of all workers is one contiguous, cache-line-aligned
 * allocation, as a block allocator gives it. So two-worker runs show where
 * the old layout shares cache lines and the new one does not. Ring memory
 * is touched before timing. Every timed phase follows a writer-only warm-up
 * whose result is thrown away. Threads start together on a barrier, and
 * each thread runs pinned to its own CPU. Every second run measures the
 * cells in reverse order. Each cell shows the median of several runs.
 * There is no pass/fail threshold.
 *
 * Readers. A full reader is a C copy of the production Go reader. It
 * copies the bytes and then checks again that they were not evicted. It
 * runs on its own CPU and reads its worker's ring in that ring's format:
 * the new frame, or the old pdump header with the old index load order.
 * An index-only reader does the same index loads and cursor updates but
 * never touches the data area. Comparing the two separates the cost of
 * sharing the index line from the cost of sharing data lines.
 *
 * Timing. An unpaced phase writes records back to back and divides the
 * phase time by the record count. A paced phase writes short bursts, spaced
 * to match the target rate on average. It times each burst with two timer
 * reads, subtracts the timer overhead and leaves out the wait between
 * bursts. At these rates the reader keeps up with both writers, so both
 * writers are compared under the same conditions. A burst spans enough
 * timer ticks that a coarse timer still measures the cost of one record.
 * For example, a 24 MHz arm64 generic counter ticks about every 42 ns. The
 * burst size depends on the timer, the record size and the publish batch.
 * It is rounded up to whole batches, so writers with different batches
 * may get different burst sizes.
 *
 * Environment:
 * - RING_BENCH_CPUS (or argv[1]): "w0,r0[,w1,r1]", the writer and reader
 *   CPU of worker 0, then of worker 1. By default the benchmark takes the
 *   allowed CPUs in order. It skips the first one when at least five are
 *   allowed.
 * - RING_BENCH_REPS: runs per cell, 5 by default.
 * - RING_BENCH_RATES: paced rates in Mrecords/s, "1,5,10" by default, or
 *   "1,10" in the quick mode.
 * - RING_BENCH_QUICK=1: fewer sizes, rates and batches.
 * - RING_BENCH_CHUNK: an eviction chunk in bytes that replaces the ring's
 *   own. It is capped at a quarter of each ring.
 * - RING_BENCH_TSV: a file. The benchmark appends the medians of every cell
 *   to it as tab-separated rows.
 */

#ifndef _GNU_SOURCE
#define _GNU_SOURCE
#endif

#include "common/numutils.h"
#include "common/ring.h"
#include "modules/pdump/dataplane/ring.h"

#include <pthread.h>
#include <sched.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

// Minimum duration of a timed phase.
//
// It is long enough that creating and joining the threads costs almost
// nothing next to the measured work.
#define BENCH_PHASE_NS (100 * 1000 * 1000ull)
// Duration of the warm-up before every timed phase. Its result is dropped.
#define BENCH_WARMUP_NS (20 * 1000 * 1000ull)
// Records written between two clock reads.
//
// Reading the clock less often keeps it from adding to the cost of the
// smallest records.
#define BENCH_CHECK_BATCH 256
// Largest number of runs per cell. It sizes the sample arrays.
#define BENCH_MAX_REPS 32
#define BENCH_DEFAULT_REPS 5
#define BENCH_MAX_RATES 8
#define BENCH_MAX_CELLS 512
// Number of publish batch sizes the ring writer is measured with.
#define BENCH_BATCH_COUNT 3

// Alignment of the private context of every thread: two cache lines.
//
// Some CPUs prefetch cache lines in adjacent pairs. With this alignment,
// the writes of one thread to its own context never slow down another
// thread. Only the ring under test is shared.
#define BENCH_PRIVATE_ALIGN 128

// Ring size of the reader and paced phases: the smallest pdump ring.
#define READER_RING_SIZE (1u << 20)
// Largest number of bytes one read copies.
//
// It matches the default pdump read chunk: 32 snapshots of the default
// 16 KiB snap length.
#define READER_READ_BUDGET (512u << 10)

// Share of the target rate a paced writer must reach.
//
// The tables mark a writer that reaches less. Its bursts ran back to back.
#define PACED_RATE_MET 0.95
// Smallest number of timer ticks one paced burst spans.
//
// With this many ticks, the tick size and the error in the subtracted timer
// overhead stay near 1% of every timed sample.
#define PACED_SAMPLE_TICKS 100
// Record cost used to size a paced burst.
//
// It is below the cost of the cheapest record of any writer. So a burst
// never spans fewer ticks than intended.
#define PACED_FLOOR_NS 10.0
// One paced burst fills at most this fraction (1/N) of the ring.
//
// A reader that keeps up with the target rate then never loses a burst.
#define PACED_BURST_RING_SHARE 8

static uint64_t
now_ns(void) {
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return (uint64_t)ts.tv_sec * 1000000000ull + (uint64_t)ts.tv_nsec;
}

static const uint32_t bench_batches[BENCH_BATCH_COUNT] = {1, 8, 32};

// Eviction chunk in bytes that replaces the ring's own.
//
// A negative value keeps the ring's own chunk.
static long bench_chunk = -1;

// Abort the benchmark with a message.
//
// A run with a failed setup step does not measure what it claims, so the
// benchmark stops instead of timing it.
static void
bench_die(const char *what) {
	fprintf(stderr, "ring_bench: %s\n", what);
	abort();
}

// Set up an empty ring that publishes after the given number of records.
//
// It applies the eviction chunk override, capped at a quarter of the ring.
// The cap leaves a small ring enough room for its batches.
static void
bench_ring_init(struct ring_worker *ring, uint32_t size, uint32_t batch) {
	ring_worker_init(ring, size, batch);
	if (bench_chunk >= 0) {
		uint32_t cap = (size / 4) & ~3u;
		ring->local.evict_chunk = (uint32_t)bench_chunk < cap
						  ? (uint32_t)bench_chunk
						  : cap;
	}
}

// Allocate a buffer and touch every byte. Abort on failure.
//
// The first write in a timed phase then never takes a page fault.
static uint8_t *
alloc_touched(size_t size) {
	uint8_t *block = malloc(size);
	if (block == NULL) {
		bench_die("failed to allocate a buffer");
	}
	memset(block, 0, size);
	return block;
}

// Pin the calling thread to one CPU.
//
// A negative CPU leaves the thread unpinned.
static void
pin_self(int cpu) {
	if (cpu < 0) {
		return;
	}
	cpu_set_t set;
	CPU_ZERO(&set);
	CPU_SET(cpu, &set);
	if (pthread_setaffinity_np(pthread_self(), sizeof(set), &set) != 0) {
		bench_die("failed to pin a thread: CPU not allowed");
	}
}

static inline void
cpu_relax(void) {
#if defined(__x86_64__) || defined(__i386__)
	__builtin_ia32_pause();
#elif defined(__aarch64__)
	__asm__ volatile("yield" ::: "memory");
#else
	atomic_signal_fence(memory_order_seq_cst);
#endif
}

// Read the CPU timer, ordered against the code around it.
//
// The barrier instructions keep the timed instructions between the two
// timer reads. Stores may still leave the store buffer after the second
// read, as they do in production. On a CPU without a known timer, the
// monotonic clock is used instead.
static inline uint64_t
tick_now(void) {
#if defined(__x86_64__)
	uint32_t lo;
	uint32_t hi;
	__asm__ volatile("lfence\n\trdtsc\n\tlfence"
			 : "=a"(lo), "=d"(hi)
			 :
			 : "memory");
	return ((uint64_t)hi << 32) | lo;
#elif defined(__aarch64__)
	uint64_t val;
	__asm__ volatile("isb\n\tmrs %0, cntvct_el0" : "=r"(val) : : "memory");
	return val;
#else
	return now_ns();
#endif
}

// Timer calibration shared by every paced phase.
struct bench_timer {
	double ns_per_tick;
	// Mean ticks between two timer reads with no work between them.
	double overhead_ticks;
	// CPU relax iterations that take about one tick.
	//
	// Set only when a tick is long next to the cost of a record. Zero
	// otherwise.
	uint32_t dither_spins;
	const char *name;
};

static struct bench_timer bench_timer;

static inline uint32_t
xorshift32(uint32_t *state) {
	uint32_t x = *state;
	x ^= x << 13;
	x ^= x >> 17;
	x ^= x << 5;
	*state = x;
	return x;
}

// Start timing a burst. The pacing wait ended at the given tick.
//
// The wait ends just after a timer tick. With a coarse timer, every burst
// would then start at the same point inside a tick, and the tick count
// would round its cost down. A random spin of up to one tick spreads the
// start points evenly. Then the mean tick count is not biased.
static inline uint64_t
timed_start(uint64_t released, uint32_t *rng) {
	uint32_t spins = bench_timer.dither_spins;
	if (spins == 0) {
		return released;
	}
	for (uint32_t i = xorshift32(rng) % spins; i > 0; --i) {
		cpu_relax();
	}
	return tick_now();
}

// Measure the timer on the CPU of the first writer.
//
// It measures the tick length and the cost of a timer read. For a coarse
// timer it also counts the CPU relax spins in one tick.
static void *
timer_calibrate(void *arg) {
	pin_self(*(const int *)arg);
	struct bench_timer *t = &bench_timer;

#if defined(__aarch64__)
	uint64_t freq;
	__asm__ volatile("mrs %0, cntfrq_el0" : "=r"(freq));
	t->ns_per_tick = 1e9 / (double)freq;
	t->name = "cntvct";
#elif defined(__x86_64__)
	uint64_t ns0 = now_ns();
	uint64_t tick0 = tick_now();
	while (now_ns() - ns0 < 50 * 1000 * 1000ull) {
		cpu_relax();
	}
	uint64_t ns1 = now_ns();
	uint64_t tick1 = tick_now();
	t->ns_per_tick = (double)(ns1 - ns0) / (double)(tick1 - tick0);
	t->name = "tsc";
#else
	t->ns_per_tick = 1.0;
	t->name = "clock";
#endif

	// A tick above 2 ns is long next to the cost of the smallest record.
	if (t->ns_per_tick > 2.0) {
		const uint32_t probe = 1u << 20;
		uint64_t start = tick_now();
		for (uint32_t i = 0; i < probe; ++i) {
			cpu_relax();
		}
		uint64_t ticks = tick_now() - start;
		uint64_t spins = ticks == 0 ? probe : probe / ticks;
		t->dither_spins = spins < 1 ? 1 : (uint32_t)spins;
	}

	// Very long samples come from interrupts, not from the timer. Skip
	// them.
	const uint32_t samples = 1u << 18;
	const uint64_t outlier = (uint64_t)(1000.0 / t->ns_per_tick) + 2;
	uint32_t rng = 0x9e3779b9u;
	uint64_t sum = 0;
	uint64_t kept = 0;
	for (uint32_t i = 0; i < samples; ++i) {
		uint64_t t0 = timed_start(tick_now(), &rng);
		uint64_t t1 = tick_now();
		if (t1 - t0 < outlier) {
			sum += t1 - t0;
			++kept;
		}
	}
	t->overhead_ticks = kept == 0 ? 0 : (double)sum / (double)kept;
	return NULL;
}

// Writer and reader CPU of each of up to two workers.
struct bench_cpus {
	int writer[2];
	int reader[2];
	// Number of workers that have both a writer and a reader CPU.
	int reader_workers;
};

enum bench_side { SIDE_OLD, SIDE_NEW, SIDE_COUNT };

// Old writer: the pdump writer with its fixed-size message header.
static inline void
old_write_record(
	struct ring_buffer *ring,
	uint8_t *data,
	uint8_t *payload,
	uint32_t payload_len
) {
	struct ring_msg_hdr hdr;
	memset(&hdr, 0, sizeof(hdr));
	hdr.total_len = (uint32_t)(sizeof(hdr) + payload_len);
	hdr.magic = RING_MSG_MAGIC;
	hdr.packet_len = payload_len;
	pdump_ring_write_msg(ring, data, &hdr, payload);
}

// New writer: the ring writer with its 8-byte frame.
//
// It commits one record into the batch that is not yet published.
static inline void
new_write_record(
	struct ring_worker *ring,
	uint8_t *data,
	uint8_t *payload,
	uint32_t payload_len
) {
	uint32_t total_len = (uint32_t)(RING_RECORD_FRAME_SIZE + payload_len);
	if (ring_worker_prepare(ring, data, total_len) != 0) {
		bench_die("ring_worker_prepare refused a benchmark record");
	}
	ring_worker_write(
		ring, data, RING_RECORD_FRAME_SIZE, payload, payload_len
	);
	ring_worker_commit(ring, data, total_len);
}

// One writer thread over the ring of one worker, old or new.
struct writer_ctx {
	struct ring_buffer *old_ring;
	struct ring_worker *new_ring;
	uint8_t *data;
	uint8_t *payload;
	uint32_t payload_len;
	// Records to write before the writer resets the positions to zero.
	//
	// The reset keeps the ring from overflowing. Zero means no reset, so
	// the ring evicts old records once it is full.
	uint64_t reset_after_records;
	// Ticks between the starts of two paced bursts. Zero means unpaced.
	uint64_t period_ticks;
	// Records per paced burst. One timing covers the whole burst.
	uint32_t burst;
	pthread_barrier_t *start_barrier;
	uint64_t run_ns;
	int cpu;
	uint64_t elapsed_ns; // out
	uint64_t iterations; // out
	// Ticks spent inside paced bursts, timer overhead included, and the
	// number of bursts.
	uint64_t busy_ticks; // out
	uint64_t bursts;     // out
} __attribute__((aligned(BENCH_PRIVATE_ALIGN)));

static inline __attribute__((always_inline)) void
writer_reset(struct writer_ctx *ctx, enum bench_side side) {
	if (side == SIDE_OLD) {
		ctx->old_ring->write_idx = 0;
		ctx->old_ring->readable_idx = 0;
	} else {
		ring_worker_set_positions(ctx->new_ring, 0, 0);
	}
}

static inline __attribute__((always_inline)) void
writer_write(struct writer_ctx *ctx, enum bench_side side) {
	if (side == SIDE_OLD) {
		old_write_record(
			ctx->old_ring, ctx->data, ctx->payload, ctx->payload_len
		);
	} else {
		// The ring publishes each full batch by itself.
		new_write_record(
			ctx->new_ring, ctx->data, ctx->payload, ctx->payload_len
		);
	}
}

// Write records back to back until the deadline.
//
// The clock is read once per group of records, not once per record.
static inline __attribute__((always_inline)) void
writer_run_unpaced(struct writer_ctx *ctx, enum bench_side side) {
	uint64_t start = now_ns();
	uint64_t deadline = start + ctx->run_ns;
	uint64_t iterations = 0;
	uint64_t since_reset = 0;
	for (;;) {
		for (uint64_t b = 0; b < BENCH_CHECK_BATCH; ++b) {
			if (ctx->reset_after_records != 0 &&
			    since_reset >= ctx->reset_after_records) {
				writer_reset(ctx, side);
				since_reset = 0;
			}
			writer_write(ctx, side);
			++since_reset;
			++iterations;
		}
		if (now_ns() >= deadline) {
			break;
		}
	}
	ctx->elapsed_ns = now_ns() - start;
	ctx->iterations = iterations;
}

// Start one burst of records per period and time each burst on its own.
//
// A writer that falls more than one period behind does not catch up with
// extra bursts. It starts the next period from the current time. A writer
// that cannot reach the rate at all writes back to back. Its measured rate
// then shows this.
static inline __attribute__((always_inline)) void
writer_run_paced(struct writer_ctx *ctx, enum bench_side side) {
	uint64_t period = ctx->period_ticks;
	uint32_t burst = ctx->burst;
	uint64_t run_ticks =
		(uint64_t)((double)ctx->run_ns / bench_timer.ns_per_tick);
	uint32_t rng = 0x2545f491u ^ (uint32_t)ctx->cpu;
	uint64_t start = tick_now();
	uint64_t end = start + run_ticks;
	uint64_t next = start;
	uint64_t busy = 0;
	uint64_t bursts = 0;
	uint64_t iterations = 0;
	uint64_t released;
	for (;;) {
		while ((released = tick_now()) < next) {
			cpu_relax();
		}
		if (released >= end) {
			break;
		}
		uint64_t t0 = timed_start(released, &rng);
		for (uint32_t k = 0; k < burst; ++k) {
			writer_write(ctx, side);
		}
		uint64_t t1 = tick_now();
		busy += t1 - t0;
		++bursts;
		iterations += burst;
		next += period;
		if (t1 > next + period) {
			next = t1;
		}
	}
	ctx->elapsed_ns = (uint64_t)((double)(tick_now() - start) *
				     bench_timer.ns_per_tick);
	ctx->iterations = iterations;
	ctx->busy_ticks = busy;
	ctx->bursts = bursts;
}

static void *
old_bench_thread(void *arg) {
	struct writer_ctx *ctx = arg;
	pin_self(ctx->cpu);
	pthread_barrier_wait(ctx->start_barrier);
	if (ctx->period_ticks == 0) {
		writer_run_unpaced(ctx, SIDE_OLD);
	} else {
		writer_run_paced(ctx, SIDE_OLD);
	}
	return NULL;
}

static void *
new_bench_thread(void *arg) {
	struct writer_ctx *ctx = arg;
	pin_self(ctx->cpu);
	pthread_barrier_wait(ctx->start_barrier);
	if (ctx->period_ticks == 0) {
		writer_run_unpaced(ctx, SIDE_NEW);
	} else {
		writer_run_paced(ctx, SIDE_NEW);
	}
	return NULL;
}

// Stop flag that the readers poll.
//
// It has its own cache lines. Writes to other data then never invalidate
// the readers' copy of the flag.
struct bench_stop {
	atomic_bool flag;
} __attribute__((aligned(BENCH_PRIVATE_ALIGN)));

// Record format a reader parses. It matches the format of its writer.
enum reader_format {
	// The pdump header: it has a magic value and no sequence number.
	READER_FORMAT_OLD,
	// The 8-byte ring frame with a sequence number.
	READER_FORMAT_NEW,
};

// Kind of reader that runs next to every writer of a phase.
enum reader_mode {
	READER_NONE,
	// Copies, checks again and parses every record.
	READER_FULL,
	// Does the same index loads and cursor updates, but reads no data.
	READER_INDEX_ONLY,
	READER_MODE_COUNT,
};

static const char *const reader_mode_names[READER_MODE_COUNT] = {
	"none", "full", "index-only"
};

// One reader over the ring of one worker.
//
// It is a C copy of the production Go reader, with its own cursor, scratch
// buffer and statistics.
struct reader_ctx {
	enum reader_format format;
	bool index_only;
	_Atomic uint64_t *write_idx;
	_Atomic uint64_t *readable_idx;
	const uint8_t *data;
	uint64_t mask;
	uint32_t capacity;
	// Length of every record the writer commits.
	uint32_t record_len;
	pthread_barrier_t *start_barrier;
	const atomic_bool *stop;
	int cpu;

	// The cursor is atomic, as in Go, where a waker goroutine polls it.
	_Atomic uint64_t cursor;
	// Bytes copied but not yet parsed.
	//
	// It holds at most one unfinished record left from the last read plus
	// one read budget.
	uint8_t *buf;
	uint64_t buf_len;
	uint64_t buf_cap;
	// Destination of every returned payload.
	//
	// The Go reader also copies each payload out of its scratch buffer.
	uint8_t *out;
	uint32_t next_seqno;

	uint64_t elapsed_ns; // out
	uint64_t records;    // out
	// Bytes the index-only reader kept after its second check.
	uint64_t consumed; // out
	// Unread bytes seen by reads that found any, and the number of such
	// reads.
	uint64_t backlog_bytes; // out
	uint64_t backlog_reads; // out
	// Returned records with a wrong length, magic or sequence order.
	uint64_t bad; // out
	// Reads that found the whole buffer invalid.
	uint64_t corrupt; // out
} __attribute__((aligned(BENCH_PRIVATE_ALIGN)));

// Copy a logical byte range out of the ring data area.
//
// The copy wraps to the start when it reaches the end of the area.
static void
reader_copy_range(
	const struct reader_ctx *ctx,
	uint8_t *dst,
	uint64_t start,
	uint64_t size
) {
	uint64_t pos = start & ctx->mask;
	uint64_t tail = (uint64_t)ctx->capacity - pos;
	if (size <= tail) {
		memcpy(dst, ctx->data + pos, size);
		return;
	}
	memcpy(dst, ctx->data + pos, tail);
	memcpy(dst + tail, ctx->data, size - tail);
}

static void
reader_drop_prefix(struct reader_ctx *ctx, uint64_t n) {
	memmove(ctx->buf, ctx->buf + n, ctx->buf_len - n);
	ctx->buf_len -= n;
}

// Parse the complete records at the front of the buffer and drop them.
//
// Returns false when a length is outside the ring bounds, or when an old
// format record has a bad magic. The buffer then stays as it is, and the
// caller discards it.
static bool
reader_parse(struct reader_ctx *ctx) {
	uint64_t hdr_size = ctx->format == READER_FORMAT_NEW
				    ? RING_RECORD_FRAME_SIZE
				    : sizeof(struct ring_msg_hdr);
	uint64_t parsed = 0;
	while (ctx->buf_len - parsed >= hdr_size) {
		const uint8_t *rec = ctx->buf + parsed;
		uint32_t total_len;
		memcpy(&total_len, rec, sizeof(total_len));
		// The old Go reader has no upper bound. This one has it, so a
		// torn header cannot run past the end of the fixed buffer.
		if (total_len < hdr_size || total_len > ctx->capacity) {
			return false;
		}
		if (ctx->format == READER_FORMAT_OLD) {
			uint32_t magic;
			memcpy(&magic,
			       rec + offsetof(struct ring_msg_hdr, magic),
			       sizeof(magic));
			if (magic != RING_MSG_MAGIC) {
				return false;
			}
		}
		uint64_t skip = ring_align4(total_len);
		if (skip > ctx->buf_len - parsed) {
			break;
		}

		if (ctx->format == READER_FORMAT_NEW) {
			uint32_t seqno;
			memcpy(&seqno, rec + sizeof(uint32_t), sizeof(seqno));
			// Eviction may skip sequence numbers. It never puts
			// them out of order.
			if ((int32_t)(seqno - ctx->next_seqno) < 0) {
				++ctx->bad;
			}
			ctx->next_seqno = seqno + 1;
		}
		if (total_len != ctx->record_len) {
			++ctx->bad;
		}
		memcpy(ctx->out, rec + hdr_size, total_len - hdr_size);
		++ctx->records;
		parsed += skip;
	}
	reader_drop_prefix(ctx, parsed);
	return true;
}

// Load both positions in the order the Go reader of this format uses.
static inline void
reader_load_indices(
	const struct reader_ctx *ctx, uint64_t *write, uint64_t *readable
) {
	if (ctx->format == READER_FORMAT_NEW) {
		*write = atomic_load_explicit(
			ctx->write_idx, memory_order_acquire
		);
		*readable = atomic_load_explicit(
			ctx->readable_idx, memory_order_acquire
		);
	} else {
		*readable = atomic_load_explicit(
			ctx->readable_idx, memory_order_acquire
		);
		*write = atomic_load_explicit(
			ctx->write_idx, memory_order_acquire
		);
	}
}

// Load the positions and move the cursor past the evicted bytes.
//
// Returns false when there is nothing to read. It then relaxes the CPU
// once, as the Go reader waits on an empty ring. Otherwise the read starts
// at the returned readable position and ends at the returned write
// position.
static inline bool
reader_begin(struct reader_ctx *ctx, uint64_t *write, uint64_t *readable) {
	reader_load_indices(ctx, write, readable);
	uint64_t cursor = atomic_load(&ctx->cursor);
	if (*readable > cursor) {
		ctx->buf_len = 0;
		atomic_store(&ctx->cursor, *readable);
	} else {
		*readable = cursor;
	}
	if (*write <= *readable) {
		cpu_relax();
		return false;
	}
	ctx->backlog_bytes += *write - *readable;
	++ctx->backlog_reads;
	return true;
}

// Do one full read.
//
// The reader loads the positions and copies the readable bytes. Then it
// moves the cursor and loads the readable position again. Bytes the writer
// evicted during the copy are dropped from the front. Then the rest is
// parsed.
static void
reader_read(struct reader_ctx *ctx) {
	uint64_t write;
	uint64_t readable;
	if (!reader_begin(ctx, &write, &readable)) {
		return;
	}

	uint64_t size = write - readable;
	if (size > READER_READ_BUDGET) {
		size = READER_READ_BUDGET;
	}
	uint64_t before = ctx->buf_len;
	if (before + size > ctx->buf_cap) {
		bench_die("reader buffer overflow");
	}
	reader_copy_range(ctx, ctx->buf + before, readable, size);
	ctx->buf_len = before + size;

	// This add is sequentially consistent, like every Go atomic. It keeps
	// the loads of the copy before the second readable position load.
	atomic_fetch_add(&ctx->cursor, size);

	uint64_t latest =
		atomic_load_explicit(ctx->readable_idx, memory_order_acquire);
	if (latest > readable) {
		uint64_t diff = latest - readable + before;
		if (diff > ctx->buf_len) {
			ctx->buf_len = 0;
			atomic_store(&ctx->cursor, latest);
			return;
		}
		reader_drop_prefix(ctx, diff);
	}

	if (!reader_parse(ctx)) {
		++ctx->corrupt;
		ctx->buf_len = 0;
		// Only the new reader skips ahead to the write position it
		// loaded.
		if (ctx->format == READER_FORMAT_NEW) {
			atomic_store(&ctx->cursor, write);
		}
	}
}

// Do one index-only read.
//
// It does the same position loads and cursor updates as a full read, in
// the same order. It copies and parses no bytes.
static void
reader_poll(struct reader_ctx *ctx) {
	uint64_t write;
	uint64_t readable;
	if (!reader_begin(ctx, &write, &readable)) {
		return;
	}

	uint64_t size = write - readable;
	if (size > READER_READ_BUDGET) {
		size = READER_READ_BUDGET;
	}
	atomic_fetch_add(&ctx->cursor, size);

	uint64_t latest =
		atomic_load_explicit(ctx->readable_idx, memory_order_acquire);
	uint64_t evicted = latest > readable ? latest - readable : 0;
	if (evicted >= size) {
		atomic_store(&ctx->cursor, latest);
		return;
	}
	ctx->consumed += size - evicted;
}

static void *
reader_thread(void *arg) {
	struct reader_ctx *ctx = arg;
	pin_self(ctx->cpu);
	pthread_barrier_wait(ctx->start_barrier);

	uint64_t start = now_ns();
	// After the stop request, read what the writer has published so far.
	for (;;) {
		bool stopping =
			atomic_load_explicit(ctx->stop, memory_order_acquire);
		if (ctx->index_only) {
			reader_poll(ctx);
		} else {
			reader_read(ctx);
		}
		if (stopping &&
		    atomic_load(&ctx->cursor) >=
			    atomic_load_explicit(
				    ctx->write_idx, memory_order_acquire
			    )) {
			break;
		}
	}
	ctx->elapsed_ns = now_ns() - start;
	return NULL;
}

// Set up a reader over the ring of one worker.
//
// Its buffers are touched in advance, so reads never take a page fault.
static void
reader_init(
	struct reader_ctx *ctx,
	enum reader_format format,
	enum reader_mode mode,
	_Atomic uint64_t *write_idx,
	_Atomic uint64_t *readable_idx,
	const uint8_t *data,
	uint32_t capacity
) {
	memset(ctx, 0, sizeof(*ctx));
	ctx->format = format;
	ctx->index_only = mode == READER_INDEX_ONLY;
	ctx->write_idx = write_idx;
	ctx->readable_idx = readable_idx;
	ctx->data = data;
	ctx->capacity = capacity;
	ctx->mask = capacity - 1;
	ctx->buf_cap = (uint64_t)capacity + READER_READ_BUDGET;
	ctx->buf = alloc_touched(ctx->buf_cap);
	ctx->out = alloc_touched(capacity);
}

static void
reader_free(struct reader_ctx *ctx) {
	free(ctx->buf);
	free(ctx->out);
}

// Result of one timed phase over all its workers.
struct bench_sample {
	// Mean writer cost per committed record.
	//
	// Unpaced, it is the phase time divided by the records. Paced, it is
	// the time inside the bursts divided by the records.
	double writer_ns;
	// Smallest paced cost the timer can measure: one tick per burst.
	//
	// A smaller paced cost is raised to this value. Zero when unpaced.
	double resolution_ns;
	// Million records per second each writer committed, averaged over the
	// workers.
	double writer_mrps;
	// The reader metrics below are zero when the phase has no reader.
	//
	// The rate is per reader, averaged over the workers.
	double reader_mrps;
	// Share of committed records that no reader returned.
	double lost;
	// Mean number of unread records a read found.
	double backlog;
	uint64_t bad;
};

// Stop and join the readers of a timed phase, and add their statistics to
// the sample.
static void
reader_collect(
	struct reader_ctx *readers,
	pthread_t *threads,
	int count,
	atomic_bool *stop,
	uint64_t written,
	struct bench_sample *sample
) {
	atomic_store_explicit(stop, true, memory_order_release);
	uint64_t returned = 0;
	double rate = 0;
	double backlog = 0;
	for (int i = 0; i < count; ++i) {
		struct reader_ctx *r = &readers[i];
		pthread_join(threads[i], NULL);
		uint64_t slot = ring_align4(r->record_len);
		uint64_t records =
			r->index_only ? r->consumed / slot : r->records;
		returned += records;
		sample->bad += r->bad;
		rate += (double)records * 1e3 / (double)r->elapsed_ns;
		if (r->backlog_reads != 0) {
			backlog += (double)r->backlog_bytes /
				   (double)r->backlog_reads / (double)slot;
		}
		reader_free(r);
	}
	sample->reader_mrps = rate / count;
	sample->backlog = backlog / count;
	sample->lost = 1.0 - (double)returned / (double)written;
}

// Allocate the old metadata of adjacent workers and their data areas.
//
// The metadata is one contiguous array aligned to 64 bytes. This is the
// layout a block allocator gives, and it does not depend on the stack
// alignment. Each worker gets its own data area, touched in advance. Abort
// on failure.
static struct ring_buffer *
old_alloc_workers(int count, uint32_t ring_size, uint8_t *data[2]) {
	struct ring_buffer *workers;
	if (posix_memalign(
		    (void **)&workers,
		    64,
		    sizeof(struct ring_buffer) * (size_t)count
	    ) != 0) {
		bench_die("failed to allocate old worker metadata");
	}
	memset(workers, 0, sizeof(struct ring_buffer) * (size_t)count);

	for (int i = 0; i < count; ++i) {
		data[i] = alloc_touched(ring_size);
		workers[i].size = ring_size;
		workers[i].mask = ring_size - 1;
	}
	return workers;
}

// Allocate the new metadata of adjacent workers and their data areas.
//
// The layout is the same as for the old writer. The array is aligned to the
// cache-line alignment of the new metadata. On targets with 128-byte cache
// lines this is more than 64 bytes. Abort on failure.
static struct ring_worker *
new_alloc_workers(
	int count, uint32_t ring_size, uint32_t batch, uint8_t *data[2]
) {
	struct ring_worker *workers;
	if (posix_memalign(
		    (void **)&workers,
		    _Alignof(struct ring_worker),
		    sizeof(struct ring_worker) * (size_t)count
	    ) != 0) {
		bench_die("failed to allocate new worker metadata");
	}
	memset(workers, 0, sizeof(struct ring_worker) * (size_t)count);

	for (int i = 0; i < count; ++i) {
		data[i] = alloc_touched(ring_size);
		bench_ring_init(&workers[i], ring_size, batch);
	}
	return workers;
}

// Ring kind of a phase.
enum bench_ring {
	// The positions reset before the ring fills, so nothing is evicted.
	RING_NO_OVERFLOW,
	// A 64 KiB ring that evicts old records once it is full.
	RING_OVERFLOW,
	// The 1 MiB ring of the reader and paced phases. It evicts old records
	// once it is full.
	RING_LARGE,
	RING_KIND_COUNT,
};

static const char *const ring_names[RING_KIND_COUNT] = {
	"no-overflow", "overflow", "1m-ring"
};

// Settings of one timed phase.
//
// They are the writer kind, the ring kind, the number of writers, their
// rate, the publish batch and the kind of reader next to each writer.
struct bench_case {
	enum bench_side side;
	enum bench_ring ring;
	uint32_t size;
	int count;
	// Target rate per writer, in million records per second. Zero means
	// unpaced.
	double rate_mrps;
	enum reader_mode reader;
	// Publish batch of the new ring. Always 1 for the old writer.
	uint32_t batch;
	const struct bench_cpus *cpus;
};

// Ring size and reset interval for the ring kind of a case.
static void
case_ring(const struct bench_case *bc, uint32_t *ring_size, uint64_t *reset) {
	// Records written before the positions reset, in the no-overflow ring.
	//
	// A larger value means a larger ring and fewer resets. Only the order
	// of magnitude matters.
	const uint32_t records_per_pass = 256;

	*reset = 0;
	switch (bc->ring) {
	case RING_NO_OVERFLOW:
		*ring_size = (uint32_t
		)next_power_of_two((uint64_t)bc->size * records_per_pass);
		*reset = *ring_size / bc->size;
		break;
	case RING_OVERFLOW:
		*ring_size = 1u << 16;
		break;
	default:
		*ring_size = READER_RING_SIZE;
		break;
	}
}

// Records one batch really holds for a ring size and a record size.
//
// It is the requested count, cut down to what the batch byte limit of the
// ring allows. It is never less than 1.
static uint32_t
case_batch(const struct bench_case *bc, uint32_t ring_size) {
	struct ring_worker probe;
	bench_ring_init(&probe, ring_size, 1);
	uint32_t fit = ring_worker_batch_max(&probe) / ring_align4(bc->size);
	uint32_t batch = bc->batch < fit ? bc->batch : fit;
	return batch == 0 ? 1 : batch;
}

// Records per paced burst for a record length and a batch.
//
// A burst has enough records to span the minimum sample ticks at the
// floor cost. It fills about its share of the reader ring. It is also a
// whole number of batches, so rounding up may go a little past that share.
// So every burst ends with a publication, as a producer call does.
static uint32_t
paced_burst(uint32_t record_len, uint32_t batch) {
	uint32_t burst = (uint32_t)(PACED_SAMPLE_TICKS *
				    bench_timer.ns_per_tick / PACED_FLOOR_NS) +
			 1;
	uint32_t cap = READER_RING_SIZE / PACED_BURST_RING_SHARE /
		       ring_align4(record_len);
	if (burst > cap) {
		burst = cap;
	}
	if (burst < batch) {
		burst = batch;
	}
	return (burst + batch - 1) / batch * batch;
}

// Run the adjacent writers of one kind through a warm-up and a timed phase.
//
// The warm-up has no reader and no pacing, and its result is dropped.
// After it, the positions and the sequence counter start again at zero.
// Readers, if any, then wait with the writers on the start barrier of the
// timed phase. In the no-overflow ring, a reset interval keeps the ring
// from overflowing.
static struct bench_sample
run_phase(const struct bench_case *bc) {
	enum bench_side side = bc->side;
	uint32_t ring_size;
	uint64_t reset;
	case_ring(bc, &ring_size, &reset);
	uint32_t batch = side == SIDE_NEW ? case_batch(bc, ring_size) : 1;

	uint32_t record_len = bc->size;
	uint32_t hdr_len = side == SIDE_OLD ? sizeof(struct ring_msg_hdr)
					    : RING_RECORD_FRAME_SIZE;
	uint8_t *payload = alloc_touched(record_len);
	memset(payload, 0xAB, record_len);

	uint8_t *data[2];
	struct ring_buffer *old_workers = NULL;
	struct ring_worker *new_workers = NULL;
	if (side == SIDE_OLD) {
		old_workers = old_alloc_workers(bc->count, ring_size, data);
	} else {
		new_workers =
			new_alloc_workers(bc->count, ring_size, batch, data);
	}
	void *(*writer_fn)(void *) =
		side == SIDE_OLD ? old_bench_thread : new_bench_thread;

	bool with_reader = bc->reader != READER_NONE;
	pthread_barrier_t warmup_barrier;
	pthread_barrier_init(&warmup_barrier, NULL, (unsigned)bc->count);
	int parties = bc->count * (with_reader ? 2 : 1);
	pthread_barrier_t barrier;
	pthread_barrier_init(&barrier, NULL, (unsigned)parties);

	struct writer_ctx ctx[2];
	pthread_t threads[2];
	for (int i = 0; i < bc->count; ++i) {
		ctx[i] = (struct writer_ctx){
			.old_ring = old_workers ? &old_workers[i] : NULL,
			.new_ring = new_workers ? &new_workers[i] : NULL,
			.data = data[i],
			.payload = payload,
			.payload_len = record_len - hdr_len,
			.reset_after_records = reset,
			.start_barrier = &warmup_barrier,
			.run_ns = BENCH_WARMUP_NS,
			.cpu = bc->cpus->writer[i],
		};
	}
	for (int i = 0; i < bc->count; ++i) {
		pthread_create(&threads[i], NULL, writer_fn, &ctx[i]);
	}
	for (int i = 0; i < bc->count; ++i) {
		pthread_join(threads[i], NULL);
	}
	uint64_t period = 0;
	uint32_t burst = 1;
	if (bc->rate_mrps > 0) {
		burst = paced_burst(record_len, batch);
		period = (uint64_t)(1e3 * burst / bc->rate_mrps /
					    bench_timer.ns_per_tick +
				    0.5);
		period = period == 0 ? 1 : period;
	}
	for (int i = 0; i < bc->count; ++i) {
		writer_reset(&ctx[i], side);
		if (side == SIDE_NEW) {
			new_workers[i].local.next_seqno = 0;
		}
		ctx[i].run_ns = BENCH_PHASE_NS;
		ctx[i].start_barrier = &barrier;
		ctx[i].period_ticks = period;
		ctx[i].burst = burst;
	}

	struct bench_stop stop = {.flag = false};
	struct reader_ctx readers[2];
	pthread_t reader_threads[2];
	if (with_reader) {
		for (int i = 0; i < bc->count; ++i) {
			_Atomic uint64_t *write_idx =
				side == SIDE_OLD
					? &old_workers[i].write_idx
					: &new_workers[i].published.write_idx;
			_Atomic uint64_t *readable_idx =
				side == SIDE_OLD
					? &old_workers[i].readable_idx
					: &new_workers[i]
						   .published.readable_idx;
			reader_init(
				&readers[i],
				side == SIDE_OLD ? READER_FORMAT_OLD
						 : READER_FORMAT_NEW,
				bc->reader,
				write_idx,
				readable_idx,
				data[i],
				ring_size
			);
			readers[i].record_len = record_len;
			readers[i].start_barrier = &barrier;
			readers[i].stop = &stop.flag;
			readers[i].cpu = bc->cpus->reader[i];
			pthread_create(
				&reader_threads[i],
				NULL,
				reader_thread,
				&readers[i]
			);
		}
	}

	for (int i = 0; i < bc->count; ++i) {
		pthread_create(&threads[i], NULL, writer_fn, &ctx[i]);
	}
	uint64_t total_ns = 0;
	uint64_t total_iterations = 0;
	uint64_t total_busy = 0;
	uint64_t total_bursts = 0;
	for (int i = 0; i < bc->count; ++i) {
		pthread_join(threads[i], NULL);
		total_ns += ctx[i].elapsed_ns;
		total_iterations += ctx[i].iterations;
		total_busy += ctx[i].busy_ticks;
		total_bursts += ctx[i].bursts;
	}

	for (int i = 0; i < bc->count; ++i) {
		if (side == SIDE_NEW) {
			ring_worker_publish(&new_workers[i]);
		}
	}

	struct bench_sample sample = {
		.writer_mrps =
			(double)total_iterations * 1e3 / (double)total_ns,
	};
	if (period == 0) {
		sample.writer_ns = (double)total_ns / (double)total_iterations;
	} else {
		double ticks = (double)total_busy / (double)total_bursts -
			       bench_timer.overhead_ticks;
		sample.resolution_ns = bench_timer.ns_per_tick / burst;
		sample.writer_ns = ticks * bench_timer.ns_per_tick / burst;
		if (sample.writer_ns < sample.resolution_ns) {
			sample.writer_ns = sample.resolution_ns;
		}
	}
	if (with_reader) {
		reader_collect(
			readers,
			reader_threads,
			bc->count,
			&stop.flag,
			total_iterations,
			&sample
		);
	}

	pthread_barrier_destroy(&warmup_barrier);
	pthread_barrier_destroy(&barrier);
	free(old_workers);
	free(new_workers);
	for (int i = 0; i < bc->count; ++i) {
		free(data[i]);
	}
	free(payload);
	return sample;
}

// Parse "w0,r0[,w1,r1]". Returns false when the list is malformed.
static bool
parse_cpus(const char *spec, struct bench_cpus *cpus) {
	int vals[4];
	int n = 0;
	const char *pos = spec;
	while (*pos != '\0') {
		char *end;
		long val = strtol(pos, &end, 10);
		if (end == pos || val < 0 || val >= CPU_SETSIZE || n == 4) {
			return false;
		}
		vals[n++] = (int)val;
		if (*end == ',') {
			++end;
		} else if (*end != '\0') {
			return false;
		}
		pos = end;
	}
	if (n != 2 && n != 4) {
		return false;
	}
	*cpus = (struct bench_cpus){
		.writer = {vals[0], n == 4 ? vals[2] : -1},
		.reader = {vals[1], n == 4 ? vals[3] : -1},
		.reader_workers = n / 2,
	};
	return true;
}

// Parse a comma-separated list of positive rates.
//
// Returns the number of rates, or 0 when the list is malformed.
static int
parse_rates(const char *spec, double rates[BENCH_MAX_RATES]) {
	int n = 0;
	const char *pos = spec;
	while (*pos != '\0') {
		char *end;
		double val = strtod(pos, &end);
		if (end == pos || !(val > 0) || n == BENCH_MAX_RATES) {
			return 0;
		}
		rates[n++] = val;
		if (*end == ',') {
			++end;
		} else if (*end != '\0') {
			return 0;
		}
		pos = end;
	}
	return n;
}

// Pick different CPUs from the allowed set.
//
// The first allowed CPU usually runs system housekeeping. It is skipped
// when at least five CPUs are allowed.
static void
default_cpus(struct bench_cpus *cpus) {
	cpu_set_t set;
	int allowed[CPU_SETSIZE];
	int n = 0;
	if (sched_getaffinity(0, sizeof(set), &set) == 0) {
		for (int cpu = 0; cpu < CPU_SETSIZE; ++cpu) {
			if (CPU_ISSET(cpu, &set)) {
				allowed[n++] = cpu;
			}
		}
	}
	int skip = n >= 5 ? 1 : 0;
	int *pick = allowed + skip;
	int avail = n - skip;
	*cpus = (struct bench_cpus){
		.writer = {-1, -1},
		.reader = {-1, -1},
	};
	if (avail >= 4) {
		// Adjacent writers, then their readers.
		*cpus = (struct bench_cpus){
			.writer = {pick[0], pick[1]},
			.reader = {pick[2], pick[3]},
			.reader_workers = 2,
		};
	} else if (avail >= 2) {
		*cpus = (struct bench_cpus){
			.writer = {pick[0], pick[1]},
			.reader = {pick[1], -1},
			.reader_workers = 1,
		};
	}
}

static int
cmp_double(const void *a, const void *b) {
	double x = *(const double *)a;
	double y = *(const double *)b;
	return (x > y) - (x < y);
}

static double
median(const double *vals, int n) {
	double sorted[BENCH_MAX_REPS];
	memcpy(sorted, vals, sizeof(double) * (size_t)n);
	qsort(sorted, (size_t)n, sizeof(double), cmp_double);
	return n % 2 ? sorted[n / 2]
		     : (sorted[n / 2 - 1] + sorted[n / 2]) / 2.0;
}

// One measured cell: a case and its samples from all runs.
struct bench_cell {
	struct bench_case bc;
	struct bench_sample samples[BENCH_MAX_REPS];
};

// Medians of the metrics of one cell over all runs.
struct cell_stats {
	bool present;
	double writer_ns;
	// Paced cost resolution. It is the same for every run of a cell.
	double resolution_ns;
	double writer_mrps;
	double reader_mrps;
	double lost_pct;
	double backlog;
	// Summed over all runs.
	unsigned long bad;
};

struct bench_plan {
	struct bench_cell *cells;
	int count;
	int reps;
};

static void
plan_add(struct bench_plan *plan, struct bench_case bc) {
	if (bc.reader != READER_NONE && bc.count > bc.cpus->reader_workers) {
		return;
	}
	if (plan->count == BENCH_MAX_CELLS) {
		bench_die("too many benchmark cells");
	}
	plan->cells[plan->count++].bc = bc;
}

// Add one cell for the old writer and one new writer cell per batch.
static void
plan_add_sides(
	struct bench_plan *plan,
	struct bench_case bc,
	const uint32_t *batches,
	int batches_count
) {
	bc.side = SIDE_OLD;
	bc.batch = 1;
	plan_add(plan, bc);

	bc.side = SIDE_NEW;
	for (int bi = 0; bi < batches_count; ++bi) {
		bc.batch = batches[bi];
		plan_add(plan, bc);
	}
}

static bool
case_equal(const struct bench_case *a, const struct bench_case *b) {
	return a->side == b->side && a->ring == b->ring && a->size == b->size &&
	       a->count == b->count && a->rate_mrps == b->rate_mrps &&
	       a->reader == b->reader && a->batch == b->batch;
}

static struct cell_stats
cell_stats(const struct bench_plan *plan, const struct bench_case *key) {
	struct cell_stats st = {0};
	const struct bench_cell *cell = NULL;
	for (int i = 0; i < plan->count; ++i) {
		if (case_equal(&plan->cells[i].bc, key)) {
			cell = &plan->cells[i];
			break;
		}
	}
	if (cell == NULL) {
		return st;
	}
	st.present = true;
	int reps = plan->reps;
	double ns[BENCH_MAX_REPS];
	double wrate[BENCH_MAX_REPS];
	double rate[BENCH_MAX_REPS];
	double lost[BENCH_MAX_REPS];
	double backlog[BENCH_MAX_REPS];
	for (int r = 0; r < reps; ++r) {
		const struct bench_sample *s = &cell->samples[r];
		ns[r] = s->writer_ns;
		wrate[r] = s->writer_mrps;
		rate[r] = s->reader_mrps;
		lost[r] = s->lost;
		backlog[r] = s->backlog;
		st.bad += (unsigned long)s->bad;
	}
	st.writer_ns = median(ns, reps);
	st.resolution_ns = cell->samples[0].resolution_ns;
	st.writer_mrps = median(wrate, reps);
	st.reader_mrps = median(rate, reps);
	st.lost_pct = 100 * median(lost, reps);
	st.backlog = median(backlog, reps);
	return st;
}

// Records per paced burst of a cell. 1 when unpaced.
static uint32_t
case_burst(const struct bench_case *bc) {
	if (bc->rate_mrps <= 0) {
		return 1;
	}
	uint32_t ring_size;
	uint64_t reset;
	case_ring(bc, &ring_size, &reset);
	uint32_t batch = bc->side == SIDE_NEW ? case_batch(bc, ring_size) : 1;
	return paced_burst(bc->size, batch);
}

// Append the medians of every cell to a file, one row per cell.
//
// Scripts use the rows to compare builds. The columns are: ring, size,
// workers, rate, reader, side, writer ns/record, writer Mrecords/s, reader
// Mrecords/s, lost %, backlog records, bad records, records per paced
// burst, paced cost resolution in ns, and the requested publish batch. The
// ring may use a smaller batch when large records do not fit. Unpaced
// rows have 1 record per burst and a resolution of 0.
static void
write_tsv(const struct bench_plan *plan, const char *path) {
	FILE *f = fopen(path, "a");
	if (f == NULL) {
		bench_die("failed to open RING_BENCH_TSV");
	}
	for (int i = 0; i < plan->count; ++i) {
		const struct bench_case *bc = &plan->cells[i].bc;
		struct cell_stats st = cell_stats(plan, bc);
		fprintf(f,
			"%s\t%u\t%d\t%g\t%s\t%s\t%.3f\t%.4f\t%.4f\t%.3f\t%.2f\t"
			"%lu\t%u\t%.4f\t%u\n",
			ring_names[bc->ring],
			bc->size,
			bc->count,
			bc->rate_mrps,
			reader_mode_names[bc->reader],
			bc->side == SIDE_OLD ? "old" : "new",
			st.writer_ns,
			st.writer_mrps,
			st.reader_mrps,
			st.lost_pct,
			st.backlog,
			st.bad,
			case_burst(bc),
			st.resolution_ns,
			bc->batch);
	}
	fclose(f);
}

// Metric a table column prints for a cell.
enum bench_metric {
	METRIC_WRITER_NS,
	METRIC_READER_MRPS,
	METRIC_LOST_PCT,
};

// Print the metric of one cell in a column, or "-" for a missing cell.
//
// For a writer cost, a trailing "*" marks a paced writer that missed its
// target rate. A leading "<" marks a paced cost at or below the timer
// resolution.
static void
print_metric(
	const struct cell_stats *st, enum bench_metric metric, double rate
) {
	if (!st->present) {
		printf(" %8s ", "-");
		return;
	}
	char val[32];
	bool missed = false;
	switch (metric) {
	case METRIC_WRITER_NS:
		missed = rate > 0 && st->writer_mrps < rate * PACED_RATE_MET;
		if (rate > 0 && st->writer_ns <= st->resolution_ns) {
			snprintf(val, sizeof(val), "<%.1f", st->resolution_ns);
		} else {
			snprintf(val, sizeof(val), "%.1f", st->writer_ns);
		}
		break;
	case METRIC_READER_MRPS:
		snprintf(val, sizeof(val), "%.2f", st->reader_mrps);
		break;
	case METRIC_LOST_PCT:
		snprintf(val, sizeof(val), "%.1f", st->lost_pct);
		break;
	}
	printf(" %8s%s", val, missed ? "*" : " ");
}

struct bench_matrix {
	const uint32_t *sizes;
	int sizes_count;
	const uint32_t *paced_sizes;
	int paced_sizes_count;
	const double *rates;
	int rates_count;
	const uint32_t *batches;
	int batches_count;
};

// Print the header of a table with the given row label.
//
// The columns are the old writer, then the new writer for each batch.
static void
print_columns(const struct bench_matrix *m, const char *key) {
	printf("%s |%9s ", key, "old");
	for (int bi = 0; bi < m->batches_count; ++bi) {
		char label[32];
		snprintf(label, sizeof(label), "b%u", m->batches[bi]);
		printf("%9s ", label);
	}
	printf("\n");
}

// Print one metric for every cell of a row, in the table header's column
// order.
static void
print_row(
	const struct bench_plan *plan,
	const struct bench_matrix *m,
	struct bench_case key,
	enum bench_metric metric
) {
	printf(" |");
	key.side = SIDE_OLD;
	key.batch = 1;
	struct cell_stats st = cell_stats(plan, &key);
	print_metric(&st, metric, key.rate_mrps);
	key.side = SIDE_NEW;
	for (int bi = 0; bi < m->batches_count; ++bi) {
		key.batch = m->batches[bi];
		st = cell_stats(plan, &key);
		print_metric(&st, metric, key.rate_mrps);
	}
	printf("\n");
}

static void
print_columns_legend(void) {
	printf("old = pdump writer and reader; bN = ring writer with a "
	       "publish batch of N records.\n");
}

static void
print_writer_alone(
	const struct bench_plan *plan, const struct bench_matrix *m
) {
	printf("\nWriter cost, ns/record (lower is better) - unpaced, no "
	       "reader\n");
	const char *key = "size, B  ring         workers";
	print_columns(m, key);
	for (int si = 0; si < m->sizes_count; ++si) {
		for (int count = 1; count <= 2; ++count) {
			for (int ring = 0; ring < RING_KIND_COUNT; ++ring) {
				struct bench_case bc = {
					.ring = ring,
					.size = m->sizes[si],
					.count = count,
				};
				printf("%7u  %-11s  %7d",
				       bc.size,
				       ring_names[ring],
				       count);
				print_row(plan, m, bc, METRIC_WRITER_NS);
			}
		}
	}
	print_columns_legend();
	printf("ring: no-overflow = indices reset before the ring fills; "
	       "overflow = 64 KiB ring\nevicting once full; 1m-ring = 1 MiB "
	       "ring evicting once full, the ring of every\nreader and paced "
	       "row.\n");
}

static void
print_writer_by_reader(
	const struct bench_plan *plan,
	const struct bench_matrix *m,
	int reader_workers
) {
	printf("\nWriter cost, ns/record (lower is better) - unpaced, 1 MiB "
	       "ring, a reader per writer\n");
	print_columns(m, "size, B  workers  reader    ");
	for (int si = 0; si < m->sizes_count; ++si) {
		for (int count = 1; count <= reader_workers; ++count) {
			for (int mode = READER_FULL; mode < READER_MODE_COUNT;
			     ++mode) {
				struct bench_case bc = {
					.ring = RING_LARGE,
					.size = m->sizes[si],
					.count = count,
					.reader = mode,
				};
				printf("%7u  %7d  %-10s",
				       bc.size,
				       count,
				       reader_mode_names[mode]);
				print_row(plan, m, bc, METRIC_WRITER_NS);
			}
		}
	}
	print_columns_legend();
	printf("full = copy-then-recheck reader copying and parsing every "
	       "record; index-only = the\nsame index loads and cursor "
	       "atomics, never touching the data area.\n");
}

static void
print_writer_paced(
	const struct bench_plan *plan, const struct bench_matrix *m
) {
	printf("\nWriter cost, ns/record (lower is better) - paced, 1 MiB "
	       "ring, 1 worker; timer %s, %.3f ns/tick\n",
	       bench_timer.name,
	       bench_timer.ns_per_tick);
	print_columns(m, "size, B  rate, Mrec/s  reader    ");
	for (int si = 0; si < m->paced_sizes_count; ++si) {
		for (int ri = 0; ri < m->rates_count; ++ri) {
			for (int mode = 0; mode < READER_MODE_COUNT; ++mode) {
				struct bench_case bc = {
					.ring = RING_LARGE,
					.size = m->paced_sizes[si],
					.count = 1,
					.rate_mrps = m->rates[ri],
					.reader = mode,
				};
				printf("%7u  %12g  %-10s",
				       bc.size,
				       bc.rate_mrps,
				       reader_mode_names[mode]);
				print_row(plan, m, bc, METRIC_WRITER_NS);
			}
		}
	}
	print_columns_legend();
	printf("rate = target records/s per writer, in millions. Records are "
	       "written in bursts\nspaced at the target rate (about %.0f ns "
	       "of records per burst, whole batches for bN);\ncost = time "
	       "inside a burst over its records, timer overhead subtracted, "
	       "pacing\nwait excluded; <x = at or below the timer's "
	       "resolution of one tick per burst;\n* = the writer reached "
	       "below %.0f%% of the target rate (bursts ran back to back).\n",
	       PACED_SAMPLE_TICKS * bench_timer.ns_per_tick,
	       PACED_RATE_MET * 100);
}

// Print a table of one reader metric for the full reader.
//
// It has the unpaced rows first, then the paced rows.
static void
print_reader_metric(
	const struct bench_plan *plan,
	const struct bench_matrix *m,
	int reader_workers,
	enum bench_metric metric,
	const char *title
) {
	printf("\n%s - full reader, 1 MiB ring\n", title);
	print_columns(m, "size, B  workers  rate, Mrec/s");
	for (int si = 0; si < m->sizes_count; ++si) {
		for (int count = 1; count <= reader_workers; ++count) {
			struct bench_case bc = {
				.ring = RING_LARGE,
				.size = m->sizes[si],
				.count = count,
				.reader = READER_FULL,
			};
			printf("%7u  %7d  %12s", bc.size, count, "unpaced");
			print_row(plan, m, bc, metric);
		}
	}
	for (int si = 0; si < m->paced_sizes_count; ++si) {
		for (int ri = 0; ri < m->rates_count; ++ri) {
			struct bench_case bc = {
				.ring = RING_LARGE,
				.size = m->paced_sizes[si],
				.count = 1,
				.rate_mrps = m->rates[ri],
				.reader = READER_FULL,
			};
			printf("%7u  %7d  %12g", bc.size, 1, bc.rate_mrps);
			print_row(plan, m, bc, metric);
		}
	}
	print_columns_legend();
}

static void
print_reader(
	const struct bench_plan *plan,
	const struct bench_matrix *m,
	int reader_workers
) {
	print_reader_metric(
		plan,
		m,
		reader_workers,
		METRIC_READER_MRPS,
		"Reader throughput, Mrec/s (higher is better)"
	);
	printf("rate = target writer rate; throughput = records the reader "
	       "returned per second.\n");
	print_reader_metric(
		plan,
		m,
		reader_workers,
		METRIC_LOST_PCT,
		"Lost records, % (lower is better)"
	);
	printf("lost = committed records the reader never returned, "
	       "overwritten before it got there.\n");

	unsigned long bad[SIDE_COUNT] = {0};
	for (int i = 0; i < plan->count; ++i) {
		const struct bench_case *bc = &plan->cells[i].bc;
		if (bc->reader == READER_FULL) {
			bad[bc->side] += cell_stats(plan, bc).bad;
		}
	}
	printf("\nBad records, all cells and runs (must be 0 for the ring "
	       "writer): old %lu, new %lu\nbad = returned records with a "
	       "wrong length, magic or sequence order.\n",
	       bad[SIDE_OLD],
	       bad[SIDE_NEW]);
}

int
main(int argc, char **argv) {
	static const uint32_t sizes_full[] = {64, 256, 1500, 9000};
	static const uint32_t sizes_quick[] = {64, 1500};
	static const uint32_t paced_full[] = {64, 256, 1500};
	static const uint32_t paced_quick[] = {64, 1500};
	static const uint32_t batches_quick[] = {1, 32};

	struct bench_cpus cpus;
	const char *spec = argc > 1 ? argv[1] : getenv("RING_BENCH_CPUS");
	if (spec != NULL && *spec != '\0') {
		if (!parse_cpus(spec, &cpus)) {
			bench_die("CPU list must be w0,r0[,w1,r1]");
		}
	} else {
		default_cpus(&cpus);
	}
	int reps = BENCH_DEFAULT_REPS;
	const char *reps_env = getenv("RING_BENCH_REPS");
	if (reps_env != NULL && *reps_env != '\0') {
		reps = atoi(reps_env);
		if (reps < 1 || reps > BENCH_MAX_REPS) {
			bench_die("RING_BENCH_REPS must be 1..32");
		}
	}
	const char *chunk_env = getenv("RING_BENCH_CHUNK");
	if (chunk_env != NULL && *chunk_env != '\0') {
		bench_chunk = atol(chunk_env);
		if (bench_chunk < 0 || bench_chunk % 4 != 0 ||
		    bench_chunk > (long)READER_RING_SIZE) {
			bench_die("RING_BENCH_CHUNK must be a multiple of 4 "
				  "up to 1 MiB");
		}
	}
	const char *quick_env = getenv("RING_BENCH_QUICK");
	bool quick = quick_env != NULL && strcmp(quick_env, "1") == 0;
	double rates[BENCH_MAX_RATES];
	const char *rates_env = getenv("RING_BENCH_RATES");
	if (rates_env == NULL || *rates_env == '\0') {
		rates_env = quick ? "1,10" : "1,5,10";
	}
	int rates_count = parse_rates(rates_env, rates);
	if (rates_count == 0) {
		bench_die("RING_BENCH_RATES must be positive Mrecords/s, "
			  "comma-separated, at most 8");
	}

	struct bench_matrix m = {
		.sizes = quick ? sizes_quick : sizes_full,
		.sizes_count = quick ? 2 : 4,
		.paced_sizes = quick ? paced_quick : paced_full,
		.paced_sizes_count = quick ? 2 : 3,
		.rates = rates,
		.rates_count = rates_count,
		.batches = quick ? batches_quick : bench_batches,
		.batches_count = quick ? 2 : BENCH_BATCH_COUNT,
	};

	pthread_t calibrator;
	pthread_create(&calibrator, NULL, timer_calibrate, &cpus.writer[0]);
	pthread_join(calibrator, NULL);

	printf("# CPUs: writers %d,%d readers %d,%d; median of %d run(s); "
	       "%s matrix\n",
	       cpus.writer[0],
	       cpus.writer[1],
	       cpus.reader[0],
	       cpus.reader[1],
	       reps,
	       quick ? "quick" : "full");
	printf("# paced timer: %s, %.3f ns/tick, read overhead %.1f ns "
	       "subtracted per burst%s\n",
	       bench_timer.name,
	       bench_timer.ns_per_tick,
	       bench_timer.overhead_ticks * bench_timer.ns_per_tick,
	       bench_timer.dither_spins ? ", start phase dithered" : "");
	if (bench_chunk >= 0) {
		printf("# eviction chunk overridden: %ld bytes\n", bench_chunk);
	} else {
		printf("# eviction chunk: %u bytes in the 64 KiB and 1 MiB "
		       "rings\n",
		       ring_evict_chunk(1u << 16));
	}
	fflush(stdout);

	struct bench_plan plan = {.reps = reps};
	plan.cells = calloc(BENCH_MAX_CELLS, sizeof(*plan.cells));
	if (plan.cells == NULL) {
		bench_die("failed to allocate results");
	}
	for (int si = 0; si < m.sizes_count; ++si) {
		for (int count = 1; count <= 2; ++count) {
			for (int ring = 0; ring < RING_KIND_COUNT; ++ring) {
				plan_add_sides(
					&plan,
					(struct bench_case){
						.ring = ring,
						.size = m.sizes[si],
						.count = count,
						.cpus = &cpus,
					},
					m.batches,
					m.batches_count
				);
			}
			for (int mode = READER_FULL; mode < READER_MODE_COUNT;
			     ++mode) {
				plan_add_sides(
					&plan,
					(struct bench_case){
						.ring = RING_LARGE,
						.size = m.sizes[si],
						.count = count,
						.reader = mode,
						.cpus = &cpus,
					},
					m.batches,
					m.batches_count
				);
			}
		}
	}
	for (int si = 0; si < m.paced_sizes_count; ++si) {
		for (int ri = 0; ri < m.rates_count; ++ri) {
			for (int mode = 0; mode < READER_MODE_COUNT; ++mode) {
				plan_add_sides(
					&plan,
					(struct bench_case){
						.ring = RING_LARGE,
						.size = m.paced_sizes[si],
						.count = 1,
						.rate_mrps = rates[ri],
						.reader = mode,
						.cpus = &cpus,
					},
					m.batches,
					m.batches_count
				);
			}
		}
	}

	// Every second run measures the cells in reverse order. So a cell does
	// not always run right after the same other cell.
	for (int rep = 0; rep < reps; ++rep) {
		for (int n = 0; n < plan.count; ++n) {
			int i = rep % 2 ? plan.count - 1 - n : n;
			struct bench_cell *cell = &plan.cells[i];
			cell->samples[rep] = run_phase(&cell->bc);
		}
	}

	print_writer_alone(&plan, &m);
	print_writer_paced(&plan, &m);
	if (cpus.reader_workers > 0) {
		print_writer_by_reader(&plan, &m, cpus.reader_workers);
		print_reader(&plan, &m, cpus.reader_workers);
	}

	const char *tsv = getenv("RING_BENCH_TSV");
	if (tsv != NULL && *tsv != '\0') {
		write_tsv(&plan, tsv);
	}

	free(plan.cells);
	return 0;
}
