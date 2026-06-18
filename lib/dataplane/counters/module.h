#pragma once

#include <stdint.h>

#include "lib/counters/counters.h"
#include "lib/counters/histogram.h"

/**
 * Number of performance histogram counters per module.
 *
 * Each module instance tracks packet processing latency across 6 different
 * batch sizes:
 * - hist_0: 1 packet
 * - hist_1: 2-3 packets
 * - hist_2: 4-7 packets
 * - hist_3: 8-15 packets
 * - hist_4: 16-31 packets
 * - hist_5: 32+ packets
 */
#define MODULE_ECTX_PERF_COUNTERS 6

/**
 * Number of linear histogram buckets for performance counter latency tracking.
 *
 * Linear buckets provide fine-grained resolution for typical packet processing
 * latencies. With 24 buckets and a 100ns step size, this covers latencies from
 * 100ns to 2500ns with precise granularity.
 */
#define MODULE_ECTX_PERF_COUNTER_LINEAR_HISTS 24

/**
 * Number of exponential histogram buckets for performance counter latency
 * tracking.
 *
 * Exponential buckets efficiently cover outlier latencies beyond the linear
 * range. With 5 exponential buckets, this extends coverage to handle rare
 * high-latency events without excessive memory overhead.
 */
#define MODULE_ECTX_PERF_COUNTER_EXP_HISTS 5

/**
 * Memory layout for module performance counter data.
 *
 * This structure defines the layout of performance metrics stored in shared
 * memory for each module instance. It tracks packet processing statistics
 * including:
 * - Total accumulated latency across all batches
 * - Total packet and byte counts
 * - Histogram of batch counts distributed across latency buckets
 *
 * The histogram uses a hybrid approach with linear buckets (fine-grained) and
 * exponential buckets (outlier coverage) as defined by
 * module_ectx_perf_counter.
 */
struct module_ectx_perf_counter_layout {
	/** Total accumulated processing latency in nanoseconds */
	uint64_t summary_latency;
	/** Total number of packets processed */
	uint64_t packets;
	/** Total number of bytes processed */
	uint64_t bytes;
	/** Histogram of batch counts across all buckets: underflow, linear,
	 * exponential, overflow. */
	uint64_t batch_count
		[MODULE_ECTX_PERF_COUNTER_LINEAR_HISTS +
		 MODULE_ECTX_PERF_COUNTER_EXP_HISTS + 2];
};

#define MODULE_ECTX_PERF_COUNTER_SIZE                                          \
	(sizeof(struct module_ectx_perf_counter_layout) / sizeof(uint64_t))

// TODO: docs
static_assert(
	MODULE_ECTX_PERF_COUNTER_SIZE <= (1 << COUNTER_MAX_SIZE_EXP),
	"module_ectx_perf_counter is too large for single counter"
);
static_assert(
	sizeof(((struct module_ectx_perf_counter_layout *)0)->batch_count) /
			sizeof(uint64_t) ==
		MODULE_ECTX_PERF_COUNTER_LINEAR_HISTS +
			MODULE_ECTX_PERF_COUNTER_EXP_HISTS + 2,
	"batch_count must hold underflow + linear + exp + overflow buckets"
);

/**
 * Hybrid histogram configuration for module performance counters.
 *
 * This histogram tracks packet processing latency in nanoseconds with:
 * - Minimum value: 100 ns
 * - Linear buckets: 24 buckets with 100 ns step (covering 100-2500 ns)
 * - Exponential buckets: 5 buckets for larger latencies
 *
 * The hybrid approach provides fine-grained resolution for typical latencies
 * (linear buckets) while efficiently covering outliers (exponential buckets).
 */
static const struct counters_hybrid_histogram module_ectx_perf_counter = {
	.min_value = 100 /* ns */,
	.linear_hists = MODULE_ECTX_PERF_COUNTER_LINEAR_HISTS,
	.linear_step = 100 /* ns */,
	.exp_hists = MODULE_ECTX_PERF_COUNTER_EXP_HISTS
};

struct module_ectx;

int
build_module_ectx_perf_counters(struct module_ectx *module_ectx);
