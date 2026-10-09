/*
 * Benchmark of the real pdump capture handler: BPF filter, metadata build
 * and ring write, through new_module_pdump()'s own handler, not a
 * reproduction of it. As bench setup only, each publish-batch config gets
 * its own dataplane_ut agent and ring object.
 *
 * One writer calls the handler on a fixed 32-packet front, reused call
 * after call so the timed loop allocates nothing. A ring overflows
 * constantly: 1 MiB, far below what the matrix's packet sizes fill in a
 * phase. A reader, a C mirror of the Go reader, may run on its own CPU:
 * busy-spinning, copying at most 512 KiB per read, same as the production
 * reader's own budget.
 *
 * The matrix crosses packet size (64, 256, 1500, 9000 B), publish batch (8,
 * 64) and reader (off, on), plus one row at 64 B where the "ip" filter
 * rejects half the packets. Every cell is measured PDUMP_BENCH_REPS times,
 * with cells visited in alternating order each pass so a cell never always
 * follows the same neighbour, and reports the min/median/max ns/packet of
 * its writer and the reader's record count.
 *
 * Environment:
 * - PDUMP_BENCH_CPUS (or argv[1]): "writer,reader". By default the first
 *   two allowed CPUs; a sandbox with fewer available degrades to sharing
 *   one.
 * - PDUMP_BENCH_REPS: runs per cell, 5 by default.
 */

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

#include <rte_eal.h>
#include <rte_mbuf.h>

#include "common/cache.h"
#include "common/ring.h"

#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"

#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"

#include "modules/pdump/dataplane/dataplane.h"
#include "modules/pdump/dataplane/record.h"

#include "objects/ring/api/ring_object.h"

#include "fixture.h"

// Packets a handler call carries, matching a real worker's batch size.
#define BURST 32u
// Capture length cap: above every packet size in the matrix, so every
// matrix cell captures the whole packet.
#define BENCH_SNAPLEN 16384u
// Per-worker ring capacity: this benchmark's own choice, large enough that
// every matrix cell overflows during warmup. The pdump minimum is 128 KiB
// (PDUMP_MIN_RING_CAPACITY in record.h), far below this.
#define BENCH_RING_CAPACITY (1u << 20)
// Largest number of bytes one reader read copies, matching the production
// reader's own budget.
#define READER_READ_BUDGET (512u << 10)

// Duration of a timed phase. Thread start and join cost almost nothing next
// to it.
#define BENCH_PHASE_NS (100 * 1000 * 1000ull)
// Minimum duration of the writer-only warm-up before every timed phase.
#define BENCH_WARMUP_MIN_NS (20 * 1000 * 1000ull)
// Handler calls between two clock reads, in warm-up and in a timed phase.
#define BENCH_CALL_CHECK 64
#define BENCH_MAX_REPS 32
#define BENCH_DEFAULT_REPS 5

// Alignment of the stop flag a reader polls every iteration: two cache
// lines, so the adjacent-line prefetcher shares nothing but the ring under
// test.
//
// Mirrors tests/common/ring_bench.c's constant of the same purpose, scaled
// to this build's cache line instead of a hardcoded one, since a cache
// line is twice as large on an arm64 DPDK build as on x86-64.
#define BENCH_PRIVATE_ALIGN (2u * YANET_CACHE_LINE_SIZE)

static const uint32_t bench_sizes[] = {64, 256, 1500, 9000};
static const uint32_t bench_batches[] = {8, 64};

#define BENCH_SIZE_COUNT (sizeof(bench_sizes) / sizeof(bench_sizes[0]))
#define BENCH_BATCH_COUNT (sizeof(bench_batches) / sizeof(bench_batches[0]))
// The full size x batch x reader cross product, plus one half-rejecting
// row at the smallest size.
#define BENCH_CASE_COUNT (BENCH_SIZE_COUNT * BENCH_BATCH_COUNT * 2 + 1)

// Record length a config of the given packet size always commits: the
// ring's own frame, the pdump metadata block and the whole packet, since
// this benchmark's capture length cap is above every packet size the
// matrix sends.
static uint32_t
bench_record_len(uint32_t pkt_size) {
	return (uint32_t)(RING_RECORD_FRAME_SIZE +
			  sizeof(struct pdump_record_hdr)) +
	       pkt_size;
}

static uint64_t
now_ns(void) {
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return (uint64_t)ts.tv_sec * 1000000000ull + (uint64_t)ts.tv_nsec;
}

// Abort with a message: a run with a failed setup measures nothing useful.
static void
bench_die(const char *what) {
	fprintf(stderr, "pdump_bench: %s\n", what);
	abort();
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

// Fill a front with BURST packets of one size, every other one non-IPv4
// when half is set, so the "ip" filter accepts exactly half of them.
static void
build_front(
	struct dataplane_ut *ut,
	struct packet_front *pf,
	uint32_t pkt_size,
	bool half
) {
	packet_front_init(pf);
	for (uint32_t i = 0; i < BURST; ++i) {
		bool ipv4 = !half || (i % 2 == 0);
		struct packet *packet =
			pdump_test_build_packet(ut, ipv4, (uint16_t)pkt_size);
		if (packet == NULL) {
			bench_die("failed to allocate a benchmark packet");
		}
		packet_front_input(pf, packet);
	}
}

// Run a handler call and feed its output back in as the next call's
// input, so the front always holds the same packets and the timed loop
// allocates nothing.
static inline void
call_handler(
	struct module *module,
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *pf
) {
	module->handler(dp_worker, module_ectx, pf);
	pf->input = pf->output;
	packet_list_init(&pf->output);
}

// A C mirror of the production Go reader for one worker's ring: acquire
// both published positions, copy a bounded chunk of new bytes, move the
// cursor, recheck the readable position, then parse whole records.
//
// Every record of one phase has the same length, known ahead of time from
// the packet size under test, so this checks it the way the production
// reader checks only the frame and the sequence order, without decoding
// the pdump metadata itself.
struct reader_ctx {
	_Atomic uint64_t *write_idx;
	_Atomic uint64_t *readable_idx;
	const uint8_t *data;
	uint64_t mask;
	uint32_t capacity;
	uint32_t record_len;
	pthread_barrier_t *start_barrier;
	const atomic_bool *stop;
	int cpu;

	_Atomic uint64_t cursor;
	uint8_t *buf;
	uint64_t buf_len;
	uint64_t buf_cap;
	uint32_t next_seqno;

	uint64_t records; // out
	uint64_t bad;	  // out
} __attribute__((aligned(BENCH_PRIVATE_ALIGN)));

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
// Returns false when a length is outside [frame size, capacity]. The
// caller then discards the buffer, as the production reader does on a
// corrupt read.
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
		++ctx->records;
		parsed += skip;
	}
	reader_drop_prefix(ctx, parsed);
	return true;
}

// Do one read, in the order the production Go reader uses.
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

static void *
reader_thread(void *arg) {
	struct reader_ctx *ctx = arg;
	pin_self(ctx->cpu);
	pthread_barrier_wait(ctx->start_barrier);

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
	return NULL;
}

static void
reader_init(
	struct reader_ctx *ctx,
	struct ring_worker *ring,
	const uint8_t *ring_data,
	uint32_t record_len,
	int cpu,
	pthread_barrier_t *barrier,
	const atomic_bool *stop
) {
	memset(ctx, 0, sizeof(*ctx));
	ctx->write_idx = &ring->published.write_idx;
	ctx->readable_idx = &ring->published.readable_idx;
	ctx->data = ring_data;
	ctx->capacity = ring->local.size;
	ctx->mask = ring->local.mask;
	ctx->record_len = record_len;
	ctx->cpu = cpu;
	ctx->start_barrier = barrier;
	ctx->stop = stop;
	ctx->buf_cap = (uint64_t)ctx->capacity + READER_READ_BUDGET;
	ctx->buf = malloc(ctx->buf_cap);
	if (ctx->buf == NULL) {
		bench_die("failed to allocate the reader buffer");
	}
}

// Stop flag a reader polls every iteration, isolated on its own cache
// lines so the writer's tight call loop never shares a line with it.
struct bench_stop {
	atomic_bool flag;
} __attribute__((aligned(BENCH_PRIVATE_ALIGN)));

// Settings of one matrix cell.
struct bench_case {
	size_t size_idx;
	size_t batch_idx;
	bool reader;
	// True only for the one row where the "ip" filter rejects half the
	// packets; false means every packet in the front is IPv4.
	bool half;
};

// One cell's result from one timed phase.
struct bench_result {
	double ns_per_pkt;
	uint64_t reader_records;
	uint64_t reader_bad;
};

// Run one cell's warm-up and timed phase.
//
// The warm-up runs the writer alone until the ring has turned over at
// least three times and a minimum warm-up time has passed, so the timed
// phase starts in steady overflow. Positions and the sequence counter
// then reset to zero, and the reader, if any, starts on a barrier with
// the writer.
static struct bench_result
run_case(
	const struct bench_case *bc,
	struct pdump_test_fixture *fx,
	struct packet_front *pf,
	uint32_t record_len,
	int reader_cpu
) {
	uint64_t start_write = fx->ring->local.write_idx;
	uint64_t warmup_start = now_ns();
	while (fx->ring->local.write_idx - start_write <
		       3ull * BENCH_RING_CAPACITY ||
	       now_ns() - warmup_start < BENCH_WARMUP_MIN_NS) {
		for (int k = 0; k < BENCH_CALL_CHECK; ++k) {
			call_handler(
				fx->module, &fx->dp_worker, &fx->module_ectx, pf
			);
		}
	}

	ring_worker_set_positions(fx->ring, 0, 0);
	fx->ring->local.next_seqno = 0;

	pthread_barrier_t barrier;
	pthread_barrier_init(&barrier, NULL, bc->reader ? 2u : 1u);

	struct bench_stop stop = {.flag = false};
	struct reader_ctx rctx;
	pthread_t reader_tid;
	if (bc->reader) {
		reader_init(
			&rctx,
			fx->ring,
			fx->ring_data,
			record_len,
			reader_cpu,
			&barrier,
			&stop.flag
		);
		pthread_create(&reader_tid, NULL, reader_thread, &rctx);
	}

	pthread_barrier_wait(&barrier);
	uint64_t start = now_ns();
	uint64_t deadline = start + BENCH_PHASE_NS;
	uint64_t calls = 0;
	do {
		for (int k = 0; k < BENCH_CALL_CHECK; ++k) {
			call_handler(
				fx->module, &fx->dp_worker, &fx->module_ectx, pf
			);
		}
		calls += BENCH_CALL_CHECK;
	} while (now_ns() < deadline);
	uint64_t elapsed = now_ns() - start;

	struct bench_result res = {
		.ns_per_pkt = (double)elapsed / (double)(calls * BURST),
	};
	if (bc->reader) {
		atomic_store_explicit(&stop.flag, true, memory_order_release);
		pthread_join(reader_tid, NULL);
		res.reader_records = rctx.records;
		res.reader_bad = rctx.bad;
		free(rctx.buf);
	}
	pthread_barrier_destroy(&barrier);
	return res;
}

static int
cmp_double(const void *a, const void *b) {
	double x = *(const double *)a;
	double y = *(const double *)b;
	return (x > y) - (x < y);
}

static int
cmp_u64(const void *a, const void *b) {
	uint64_t x = *(const uint64_t *)a;
	uint64_t y = *(const uint64_t *)b;
	return (x > y) - (x < y);
}

static double
median_double(double *vals, int n) {
	qsort(vals, (size_t)n, sizeof(double), cmp_double);
	int mid = n / 2;
	return n % 2 ? vals[mid] : (vals[mid - 1] + vals[mid]) / 2.0;
}

static uint64_t
median_u64(uint64_t *vals, int n) {
	qsort(vals, (size_t)n, sizeof(uint64_t), cmp_u64);
	return vals[n / 2];
}

// Fill the matrix: every packet size x publish batch x reader combination,
// plus one half-rejecting row at the smallest size with the default batch
// and no reader.
static size_t
build_cases(struct bench_case cases[BENCH_CASE_COUNT]) {
	size_t n = 0;
	for (size_t si = 0; si < BENCH_SIZE_COUNT; ++si) {
		for (size_t bi = 0; bi < BENCH_BATCH_COUNT; ++bi) {
			for (int rd = 0; rd < 2; ++rd) {
				cases[n++] = (struct bench_case){
					.size_idx = si,
					.batch_idx = bi,
					.reader = rd != 0,
					.half = false,
				};
			}
		}
	}
	cases[n++] = (struct bench_case){
		.size_idx = 0,
		.batch_idx = 0,
		.reader = false,
		.half = true,
	};
	return n;
}

// Parse "writer,reader". Returns false when the list is malformed.
static bool
parse_cpus(const char *spec, int *writer, int *reader) {
	int vals[2];
	int n = 0;
	const char *pos = spec;
	while (*pos != '\0') {
		char *end;
		long val = strtol(pos, &end, 10);
		if (end == pos || val < 0 || val >= CPU_SETSIZE || n == 2) {
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
	if (n != 2) {
		return false;
	}
	*writer = vals[0];
	*reader = vals[1];
	return true;
}

// Default to the first two CPUs this process may run on. A sandbox with
// only one allowed CPU shares it between writer and reader.
static void
default_cpus(int *writer, int *reader) {
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
	*writer = n >= 1 ? allowed[0] : -1;
	*reader = n >= 2 ? allowed[1] : *writer;
}

int
main(int argc, char **argv) {
	int writer_cpu;
	int reader_cpu;
	const char *spec = argc > 1 ? argv[1] : getenv("PDUMP_BENCH_CPUS");
	if (spec != NULL && *spec != '\0') {
		if (!parse_cpus(spec, &writer_cpu, &reader_cpu)) {
			bench_die("CPU list must be writer,reader");
		}
	} else {
		default_cpus(&writer_cpu, &reader_cpu);
	}

	int reps = BENCH_DEFAULT_REPS;
	const char *reps_env = getenv("PDUMP_BENCH_REPS");
	if (reps_env != NULL && *reps_env != '\0') {
		reps = atoi(reps_env);
		if (reps < 1 || reps > BENCH_MAX_REPS) {
			bench_die("PDUMP_BENCH_REPS must be 1..32");
		}
	}

	printf("# CPUs: writer %d reader %d; %d run(s) of %llu ms per cell\n",
	       writer_cpu,
	       reader_cpu,
	       reps,
	       BENCH_PHASE_NS / 1000000);
	fflush(stdout);

	// A minimal, hugepage-free EAL setup: just enough for rte_bpf_load's
	// mmap-based allocation and rte_malloc's heap, since this benchmark
	// never touches a NIC or a hugepage-backed mempool. It shares no EAL
	// config file, so test binaries running in parallel never contend
	// for the EAL lock.
	char prog_name[] = "pdump_bench";
	char *eal_argv[] = {
		prog_name,
		"--no-huge",
		"--no-pci",
		"-m",
		"128",
		"--iova-mode=va",
		"--no-shconf"
	};
	if (rte_eal_init(sizeof(eal_argv) / sizeof(eal_argv[0]), eal_argv) <
	    0) {
		bench_die("rte_eal_init failed");
	}
	pin_self(writer_cpu);

	yanet_error *err = NULL;
	struct rte_bpf *bpf =
		pdump_test_compile_accept_ip_filter(BENCH_SNAPLEN, &err);
	if (bpf == NULL) {
		bench_die(
			err ? yanet_error_message(err)
			    : "failed to compile the ip filter"
		);
	}

	const char *objs_to_load[] = {RING_OBJECT_TYPE};
	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 26,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.objects_to_load = objs_to_load,
		.objects_to_load_count = 1,
	};
	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
	if (ut == NULL) {
		bench_die("dataplane_ut_new failed");
	}
	if (!dataplane_ut_build_optimized()) {
		fprintf(stderr,
			"pdump_bench: this build is not optimized for "
			"benchmarking (debug or sanitized); results are not "
			"representative\n");
	}

	struct module *module = new_module_pdump();
	if (module == NULL) {
		bench_die("new_module_pdump failed");
	}

	struct pdump_test_gen gen;
	if (pdump_test_gen_init(&gen, 1) != TEST_SUCCESS) {
		bench_die("pdump_test_gen_init failed");
	}

	static const char *const bench_names[BENCH_BATCH_COUNT] = {"b8", "b64"};
	struct pdump_test_fixture fixtures[BENCH_BATCH_COUNT];
	for (size_t bi = 0; bi < BENCH_BATCH_COUNT; ++bi) {
		char agent_name[32];
		char ring_name[32];
		snprintf(
			agent_name,
			sizeof(agent_name),
			"pdump-bench-%s",
			bench_names[bi]
		);
		snprintf(
			ring_name,
			sizeof(ring_name),
			"ring-bench-%s",
			bench_names[bi]
		);
		struct pdump_test_fixture_params params = {
			.agent_name = agent_name,
			.ring_name = ring_name,
			.capacity = BENCH_RING_CAPACITY,
			.publish_batch = bench_batches[bi],
			.snaplen = BENCH_SNAPLEN,
			.worker_idx = 0,
			.module = module,
			.bpf = bpf,
			.gen = &gen,
		};
		if (pdump_test_fixture_build(ut, &params, &fixtures[bi]) !=
		    TEST_SUCCESS) {
			bench_die("pdump_test_fixture_build failed");
		}
	}

	struct packet_front fronts[BENCH_SIZE_COUNT];
	for (size_t si = 0; si < BENCH_SIZE_COUNT; ++si) {
		build_front(ut, &fronts[si], bench_sizes[si], false);
	}
	struct packet_front half_front;
	build_front(ut, &half_front, bench_sizes[0], true);

	struct bench_case cases[BENCH_CASE_COUNT];
	size_t case_count = build_cases(cases);

	static struct bench_result results[BENCH_CASE_COUNT][BENCH_MAX_REPS];
	for (int rep = 0; rep < reps; ++rep) {
		for (size_t n = 0; n < case_count; ++n) {
			size_t idx = (rep % 2) ? case_count - 1 - n : n;
			const struct bench_case *bc = &cases[idx];
			struct packet_front *pf =
				bc->half ? &half_front : &fronts[bc->size_idx];
			results[idx][rep] = run_case(
				bc,
				&fixtures[bc->batch_idx],
				pf,
				bench_record_len(bench_sizes[bc->size_idx]),
				reader_cpu
			);
		}
	}

	uint64_t bad_total = 0;
	printf("%7s  %5s  %6s  %-4s | %8s %8s %8s %12s\n",
	       "size,B",
	       "batch",
	       "reader",
	       "filt",
	       "min",
	       "med",
	       "max",
	       "rd_records");
	for (size_t i = 0; i < case_count; ++i) {
		const struct bench_case *bc = &cases[i];
		double ns[BENCH_MAX_REPS];
		uint64_t rec[BENCH_MAX_REPS];
		for (int r = 0; r < reps; ++r) {
			ns[r] = results[i][r].ns_per_pkt;
			rec[r] = results[i][r].reader_records;
			bad_total += results[i][r].reader_bad;
		}
		double min_ns = ns[0], max_ns = ns[0];
		for (int r = 1; r < reps; ++r) {
			if (ns[r] < min_ns) {
				min_ns = ns[r];
			}
			if (ns[r] > max_ns) {
				max_ns = ns[r];
			}
		}
		double med_ns = median_double(ns, reps);
		uint64_t med_rec = bc->reader ? median_u64(rec, reps) : 0;

		printf("%7u  %5u  %6d  %-4s | %8.2f %8.2f %8.2f %12lu\n",
		       bench_sizes[bc->size_idx],
		       bench_batches[bc->batch_idx],
		       bc->reader,
		       bc->half ? "half" : "all",
		       min_ns,
		       med_ns,
		       max_ns,
		       (unsigned long)med_rec);
	}
	printf("Bad records or corrupt reads, all cells and reps (must be "
	       "0): %lu\n",
	       (unsigned long)bad_total);

	for (size_t si = 0; si < BENCH_SIZE_COUNT; ++si) {
		pdump_test_free_front(&fronts[si]);
	}
	pdump_test_free_front(&half_front);
	free(module);
	for (size_t bi = 0; bi < BENCH_BATCH_COUNT; ++bi) {
		pdump_test_fixture_destroy(&fixtures[bi]);
	}
	pdump_test_gen_destroy(&gen);
	dataplane_ut_free(ut);
	free(bpf);

	return bad_total == 0 ? 0 : 1;
}
