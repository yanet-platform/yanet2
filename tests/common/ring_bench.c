/*
 * Benchmark of the ring writer, alone and with one reader per writer.
 *
 * One or two adjacent workers commit records back to back with a publish
 * batch of 1, 8 or 32. A no-overflow ring resets its positions before it
 * fills; a 1 MiB ring evicts once full. A reader, a C copy of the Go reader,
 * runs on its own CPU. Threads are pinned and start on a barrier after a
 * warm-up; cells run in alternating order and print the median of runs.
 *
 * Environment:
 * - RING_BENCH_CPUS (or argv[1]): "w0,r0[,w1,r1]", the writer and reader
 *   CPU of worker 0, then of worker 1. By default the allowed CPUs.
 * - RING_BENCH_REPS: runs per cell, 5 by default.
 */

#include "common/numutils.h"
#include "common/ring.h"

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

// Duration of a timed phase. Thread start and join cost almost nothing
// next to it.
#define BENCH_PHASE_NS (100 * 1000 * 1000ull)
// Duration of the writer-only warm-up before every timed phase.
#define BENCH_WARMUP_NS (20 * 1000 * 1000ull)
// Records written between two clock reads.
#define BENCH_CHECK_BATCH 256
#define BENCH_MAX_REPS 32
#define BENCH_DEFAULT_REPS 5

// Alignment of every thread's private context: two cache lines, so the
// adjacent-line prefetcher shares nothing but the ring under test.
#define BENCH_PRIVATE_ALIGN 128

// Size of the evicting ring: the smallest pdump ring.
#define BENCH_LARGE_RING (1u << 20)
// Records the no-overflow ring holds before its positions reset.
#define BENCH_NO_OVERFLOW_RECORDS 256
// Largest number of bytes one read copies: pdump's default read chunk.
#define READER_READ_BUDGET (512u << 10)

static const uint32_t bench_sizes[] = {64, 1500};
static const uint32_t bench_batches[] = {1, 8, 32};

#define BENCH_SIZE_COUNT (sizeof(bench_sizes) / sizeof(bench_sizes[0]))
#define BENCH_BATCH_COUNT (sizeof(bench_batches) / sizeof(bench_batches[0]))

static uint64_t
now_ns(void) {
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return (uint64_t)ts.tv_sec * 1000000000ull + (uint64_t)ts.tv_nsec;
}

// Abort with a message: a run with a failed setup measures nothing useful.
static void
bench_die(const char *what) {
	fprintf(stderr, "ring_bench: %s\n", what);
	abort();
}

// Allocate a buffer and touch every byte, so timed code takes no page
// fault. Abort on failure.
static uint8_t *
alloc_touched(size_t size) {
	uint8_t *block = malloc(size);
	if (block == NULL) {
		bench_die("failed to allocate a buffer");
	}
	memset(block, 0, size);
	return block;
}

// Pin the calling thread to one CPU. A negative CPU leaves it unpinned.
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

// Writer and reader CPU of each of up to two workers.
struct bench_cpus {
	int writer[2];
	int reader[2];
	// Number of workers that have a reader CPU of their own.
	int reader_workers;
};

// Ring setup of a cell.
enum bench_ring {
	// The positions reset before the ring fills, so nothing is evicted.
	RING_NO_OVERFLOW,
	// A 1 MiB ring that evicts the oldest records once it is full.
	RING_LARGE,
	// The 1 MiB ring with a full reader next to every writer.
	RING_LARGE_READER,
	RING_KIND_COUNT,
};

static const char *const ring_names[RING_KIND_COUNT] = {
	"no-overflow", "1 MiB", "1 MiB+reader"
};

// One writer thread over the ring of one worker.
struct writer_ctx {
	struct ring_worker *ring;
	uint8_t *data;
	const uint8_t *payload;
	uint32_t payload_len;
	// Records written before the positions reset to zero. Zero means no
	// reset, so the ring evicts once it is full.
	uint64_t reset_after_records;
	pthread_barrier_t *start_barrier;
	uint64_t run_ns;
	int cpu;
	uint64_t elapsed_ns; // out
	uint64_t records;    // out
} __attribute__((aligned(BENCH_PRIVATE_ALIGN)));

// Commit one record. The ring publishes each full batch by itself.
static inline void
writer_write(struct writer_ctx *ctx) {
	uint32_t total_len = RING_RECORD_FRAME_SIZE + ctx->payload_len;
	if (ring_worker_prepare(ctx->ring, ctx->data, total_len) != 0) {
		bench_die("ring_worker_prepare refused a benchmark record");
	}
	ring_worker_write(
		ctx->ring,
		ctx->data,
		RING_RECORD_FRAME_SIZE,
		ctx->payload,
		ctx->payload_len
	);
	ring_worker_commit(ctx->ring, ctx->data, total_len);
}

// Write records back to back until the deadline, reading the clock once
// per group of records.
static void *
writer_thread(void *arg) {
	struct writer_ctx *ctx = arg;
	pin_self(ctx->cpu);
	pthread_barrier_wait(ctx->start_barrier);

	uint64_t start = now_ns();
	uint64_t deadline = start + ctx->run_ns;
	uint64_t records = 0;
	uint64_t since_reset = 0;
	do {
		for (uint32_t b = 0; b < BENCH_CHECK_BATCH; ++b) {
			if (ctx->reset_after_records != 0 &&
			    since_reset == ctx->reset_after_records) {
				ring_worker_set_positions(ctx->ring, 0, 0);
				since_reset = 0;
			}
			writer_write(ctx);
			++since_reset;
		}
		records += BENCH_CHECK_BATCH;
	} while (now_ns() < deadline);
	ctx->elapsed_ns = now_ns() - start;
	ctx->records = records;
	return NULL;
}

// Stop flag that the readers poll, on cache lines of its own.
struct bench_stop {
	atomic_bool flag;
} __attribute__((aligned(BENCH_PRIVATE_ALIGN)));

// One reader over the ring of one worker.
//
// It is a C copy of the production Go reader: copy, move the cursor,
// recheck the readable position, then parse. It has its own cursor,
// scratch buffer and statistics.
struct reader_ctx {
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

	// Atomic, as in Go, where another goroutine polls it.
	_Atomic uint64_t cursor;
	// Bytes copied but not yet parsed: at most one unfinished record plus
	// one read budget.
	uint8_t *buf;
	uint64_t buf_len;
	uint64_t buf_cap;
	// Destination of every returned payload, as the Go reader copies each
	// payload out of its scratch buffer.
	uint8_t *out;
	uint32_t next_seqno;

	uint64_t elapsed_ns; // out
	uint64_t records;    // out
	// Records with a wrong length or sequence order, and corrupt reads.
	uint64_t bad; // out
} __attribute__((aligned(BENCH_PRIVATE_ALIGN)));

// Copy a logical byte range out of the data area, wrapping at its end.
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
// Returns false when a length is outside [frame size, capacity]. The caller
// then discards the buffer.
static bool
reader_parse(struct reader_ctx *ctx) {
	uint64_t parsed = 0;
	while (ctx->buf_len - parsed >= RING_RECORD_FRAME_SIZE) {
		const uint8_t *rec = ctx->buf + parsed;
		struct ring_record_frame frame;
		memcpy(&frame, rec, sizeof(frame));
		if (frame.total_len < RING_RECORD_FRAME_SIZE ||
		    frame.total_len > ctx->capacity) {
			return false;
		}
		uint64_t skip = ring_align4(frame.total_len);
		if (skip > ctx->buf_len - parsed) {
			break;
		}
		// Eviction may skip sequence numbers but never reorders them.
		if ((int32_t)(frame.seqno - ctx->next_seqno) < 0 ||
		    frame.total_len != ctx->record_len) {
			++ctx->bad;
		}
		ctx->next_seqno = frame.seqno + 1;
		memcpy(ctx->out,
		       rec + RING_RECORD_FRAME_SIZE,
		       frame.total_len - RING_RECORD_FRAME_SIZE);
		++ctx->records;
		parsed += skip;
	}
	reader_drop_prefix(ctx, parsed);
	return true;
}

// Do one read, in the order of the Go reader.
static void
reader_read(struct reader_ctx *ctx) {
	uint64_t write =
		atomic_load_explicit(ctx->write_idx, memory_order_acquire);
	uint64_t readable =
		atomic_load_explicit(ctx->readable_idx, memory_order_acquire);
	uint64_t cursor = atomic_load(&ctx->cursor);
	if (readable > cursor) {
		ctx->buf_len = 0;
		atomic_store(&ctx->cursor, readable);
	} else {
		readable = cursor;
	}
	if (write <= readable) {
		cpu_relax();
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

	// Sequentially consistent like every Go atomic: it keeps the loads of
	// the copy before the recheck below.
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
		++ctx->bad;
		ctx->buf_len = 0;
		atomic_store(&ctx->cursor, write);
	}
}

// Read until stopped, then drain what the writer has published.
static void *
reader_thread(void *arg) {
	struct reader_ctx *ctx = arg;
	pin_self(ctx->cpu);
	pthread_barrier_wait(ctx->start_barrier);

	uint64_t start = now_ns();
	for (;;) {
		bool stopping =
			atomic_load_explicit(ctx->stop, memory_order_acquire);
		reader_read(ctx);
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

// Set up a reader over one worker's ring, with its buffers touched.
static void
reader_init(
	struct reader_ctx *ctx,
	struct ring_worker *ring,
	const uint8_t *data,
	uint32_t record_len
) {
	memset(ctx, 0, sizeof(*ctx));
	ctx->write_idx = &ring->published.write_idx;
	ctx->readable_idx = &ring->published.readable_idx;
	ctx->data = data;
	ctx->capacity = ring->local.size;
	ctx->mask = ring->local.mask;
	ctx->record_len = record_len;
	ctx->buf_cap = (uint64_t)ctx->capacity + READER_READ_BUDGET;
	ctx->buf = alloc_touched(ctx->buf_cap);
	ctx->out = alloc_touched(ctx->capacity);
}

// Settings of one cell.
struct bench_case {
	enum bench_ring ring;
	uint32_t size;
	int workers;
	uint32_t batch;
};

// Result of one timed phase, averaged over its workers.
struct bench_sample {
	double writer_ns;
	// Zero when the cell has no reader.
	double reader_mrps;
	double lost_pct;
	uint64_t bad;
};

// Allocate the metadata of adjacent workers as one cache-line-aligned
// array, as a block allocator gives it, and a touched data area each.
static struct ring_worker *
alloc_workers(int count, uint32_t ring_size, uint32_t batch, uint8_t *data[2]) {
	struct ring_worker *workers;
	size_t bytes = sizeof(struct ring_worker) * (size_t)count;
	if (posix_memalign(
		    (void **)&workers, _Alignof(struct ring_worker), bytes
	    ) != 0) {
		bench_die("failed to allocate worker metadata");
	}
	memset(workers, 0, bytes);
	for (int i = 0; i < count; ++i) {
		data[i] = alloc_touched(ring_size);
		ring_worker_init(&workers[i], ring_size, batch);
	}
	return workers;
}

// Run the writers of a cell through a warm-up and a timed phase.
//
// The warm-up has no reader and its result is dropped. Then the positions
// and the sequence counter start again at zero, and the readers, if any,
// wait with the writers on the start barrier.
static struct bench_sample
run_phase(const struct bench_case *bc, const struct bench_cpus *cpus) {
	uint32_t ring_size = BENCH_LARGE_RING;
	uint64_t reset = 0;
	if (bc->ring == RING_NO_OVERFLOW) {
		ring_size = (uint32_t)next_power_of_two(
			(uint64_t)bc->size * BENCH_NO_OVERFLOW_RECORDS
		);
		reset = ring_size / ring_align4(bc->size);
	}
	bool with_reader = bc->ring == RING_LARGE_READER;
	int count = bc->workers;

	uint8_t *payload = alloc_touched(bc->size);
	memset(payload, 0xAB, bc->size);
	uint8_t *data[2];
	struct ring_worker *workers =
		alloc_workers(count, ring_size, bc->batch, data);

	pthread_barrier_t warmup_barrier;
	pthread_barrier_t barrier;
	pthread_barrier_init(&warmup_barrier, NULL, (unsigned)count);
	pthread_barrier_init(
		&barrier, NULL, (unsigned)(count * (with_reader ? 2 : 1))
	);

	struct writer_ctx writers[2];
	pthread_t threads[2];
	for (int i = 0; i < count; ++i) {
		writers[i] = (struct writer_ctx){
			.ring = &workers[i],
			.data = data[i],
			.payload = payload,
			.payload_len = bc->size - RING_RECORD_FRAME_SIZE,
			.reset_after_records = reset,
			.start_barrier = &warmup_barrier,
			.run_ns = BENCH_WARMUP_NS,
			.cpu = cpus->writer[i],
		};
		pthread_create(&threads[i], NULL, writer_thread, &writers[i]);
	}
	for (int i = 0; i < count; ++i) {
		pthread_join(threads[i], NULL);
		ring_worker_set_positions(&workers[i], 0, 0);
		workers[i].local.next_seqno = 0;
		writers[i].start_barrier = &barrier;
		writers[i].run_ns = BENCH_PHASE_NS;
	}

	struct bench_stop stop = {.flag = false};
	struct reader_ctx readers[2];
	pthread_t reader_threads[2];
	for (int i = 0; with_reader && i < count; ++i) {
		reader_init(&readers[i], &workers[i], data[i], bc->size);
		readers[i].start_barrier = &barrier;
		readers[i].stop = &stop.flag;
		readers[i].cpu = cpus->reader[i];
		pthread_create(
			&reader_threads[i], NULL, reader_thread, &readers[i]
		);
	}
	for (int i = 0; i < count; ++i) {
		pthread_create(&threads[i], NULL, writer_thread, &writers[i]);
	}

	struct bench_sample sample = {0};
	uint64_t written = 0;
	for (int i = 0; i < count; ++i) {
		pthread_join(threads[i], NULL);
		ring_worker_publish(&workers[i]);
		sample.writer_ns += (double)writers[i].elapsed_ns /
				    (double)writers[i].records / count;
		written += writers[i].records;
	}

	if (with_reader) {
		atomic_store_explicit(&stop.flag, true, memory_order_release);
		uint64_t returned = 0;
		for (int i = 0; i < count; ++i) {
			struct reader_ctx *r = &readers[i];
			pthread_join(reader_threads[i], NULL);
			returned += r->records;
			sample.bad += r->bad;
			sample.reader_mrps += (double)r->records * 1e3 /
					      (double)r->elapsed_ns / count;
			free(r->buf);
			free(r->out);
		}
		sample.lost_pct =
			100.0 * (1.0 - (double)returned / (double)written);
	}

	pthread_barrier_destroy(&warmup_barrier);
	pthread_barrier_destroy(&barrier);
	for (int i = 0; i < count; ++i) {
		free(data[i]);
	}
	free(workers);
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

// Pick different CPUs from the allowed set: adjacent writers, then their
// readers. The first CPU usually runs housekeeping, so it is skipped when
// at least five are allowed.
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
	*cpus = (struct bench_cpus){.writer = {-1, -1}, .reader = {-1, -1}};
	if (avail >= 4) {
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

static int bench_reps = BENCH_DEFAULT_REPS;
static struct bench_cpus bench_cpus;
// Bad records or corrupt reads over all cells and runs.
static uint64_t bench_bad;
// Samples of every cell and run, by size, worker count - 1, ring and batch.
static struct bench_sample bench_results[BENCH_SIZE_COUNT][2][RING_KIND_COUNT]
					[BENCH_BATCH_COUNT][BENCH_MAX_REPS];

// Whether a cell has no reader CPUs and so is not measured.
static bool
cell_skipped(enum bench_ring ring, int workers) {
	return ring == RING_LARGE_READER && workers > bench_cpus.reader_workers;
}

// Print one table row: the median of a sample field for every publish
// batch, or "-" for a skipped cell.
static void
print_row(
	size_t si,
	int workers,
	enum bench_ring ring,
	size_t field,
	const char *label
) {
	printf("%7u  %7d  %-12s |", bench_sizes[si], workers, label);
	for (size_t bi = 0; bi < BENCH_BATCH_COUNT; ++bi) {
		if (cell_skipped(ring, workers)) {
			printf(" %8s", "-");
			continue;
		}
		double vals[BENCH_MAX_REPS];
		for (int r = 0; r < bench_reps; ++r) {
			const char *sample =
				(const char *)&bench_results[si][workers - 1]
							    [ring][bi][r];
			memcpy(&vals[r], sample + field, sizeof(double));
		}
		qsort(vals, (size_t)bench_reps, sizeof(double), cmp_double);
		int mid = bench_reps / 2;
		printf(" %8.2f",
		       bench_reps % 2 ? vals[mid]
				      : (vals[mid - 1] + vals[mid]) / 2);
	}
	printf("\n");
}

static void
print_batch_header(const char *title, const char *key) {
	printf("\n%s\n%s |", title, key);
	for (size_t bi = 0; bi < BENCH_BATCH_COUNT; ++bi) {
		char label[16];
		snprintf(label, sizeof(label), "batch %u", bench_batches[bi]);
		printf(" %8s", label);
	}
	printf("\n");
}

int
main(int argc, char **argv) {
	const char *spec = argc > 1 ? argv[1] : getenv("RING_BENCH_CPUS");
	if (spec != NULL && *spec != '\0') {
		if (!parse_cpus(spec, &bench_cpus)) {
			bench_die("CPU list must be w0,r0[,w1,r1]");
		}
	} else {
		default_cpus(&bench_cpus);
	}
	const char *reps_env = getenv("RING_BENCH_REPS");
	if (reps_env != NULL && *reps_env != '\0') {
		bench_reps = atoi(reps_env);
		if (bench_reps < 1 || bench_reps > BENCH_MAX_REPS) {
			bench_die("RING_BENCH_REPS must be 1..32");
		}
	}
	printf("# CPUs: writers %d,%d readers %d,%d; median of %d run(s) of "
	       "%llu ms\n",
	       bench_cpus.writer[0],
	       bench_cpus.writer[1],
	       bench_cpus.reader[0],
	       bench_cpus.reader[1],
	       bench_reps,
	       BENCH_PHASE_NS / 1000000);
	fflush(stdout);

	// Every second run measures the cells in reverse order, so a cell
	// does not always follow the same one.
	const int cells =
		BENCH_SIZE_COUNT * 2 * RING_KIND_COUNT * BENCH_BATCH_COUNT;
	for (int rep = 0; rep < bench_reps; ++rep) {
		for (int n = 0; n < cells; ++n) {
			int idx = rep % 2 ? cells - 1 - n : n;
			size_t bi = idx % BENCH_BATCH_COUNT;
			idx /= BENCH_BATCH_COUNT;
			enum bench_ring ring = idx % RING_KIND_COUNT;
			idx /= RING_KIND_COUNT;
			int workers = idx % 2 + 1;
			size_t si = idx / 2;
			if (cell_skipped(ring, workers)) {
				continue;
			}
			struct bench_case bc = {
				.ring = ring,
				.size = bench_sizes[si],
				.workers = workers,
				.batch = bench_batches[bi],
			};
			struct bench_sample sample =
				run_phase(&bc, &bench_cpus);
			bench_results[si][workers - 1][ring][bi][rep] = sample;
			bench_bad += sample.bad;
		}
	}

	print_batch_header(
		"Table 1. Writer cost, ns/record (lower is better)",
		"size, B  workers  ring        "
	);
	for (size_t si = 0; si < BENCH_SIZE_COUNT; ++si) {
		for (int workers = 1; workers <= 2; ++workers) {
			for (int ring = 0; ring < RING_KIND_COUNT; ++ring) {
				print_row(
					si,
					workers,
					ring,
					offsetof(
						struct bench_sample, writer_ns
					),
					ring_names[ring]
				);
			}
		}
	}
	if (bench_cpus.reader_workers == 0) {
		return 0;
	}
	print_batch_header(
		"Table 2. Full reader on the 1 MiB ring: throughput, "
		"Mrecords/s "
		"per reader (higher\nis better), and lost records, % of "
		"committed (lower is better)",
		"size, B  workers  metric      "
	);
	for (size_t si = 0; si < BENCH_SIZE_COUNT; ++si) {
		for (int workers = 1; workers <= bench_cpus.reader_workers;
		     ++workers) {
			print_row(
				si,
				workers,
				RING_LARGE_READER,
				offsetof(struct bench_sample, reader_mrps),
				"Mrec/s"
			);
			print_row(
				si,
				workers,
				RING_LARGE_READER,
				offsetof(struct bench_sample, lost_pct),
				"lost, %"
			);
		}
	}
	printf("Bad records or corrupt reads, all cells and runs (must be "
	       "0): %lu\n",
	       (unsigned long)bench_bad);
	return 0;
}
