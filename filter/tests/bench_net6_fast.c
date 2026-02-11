/**
 * @file bench_net6_fast.c
 * @brief Performance benchmark for net6_fast filter
 *
 * This benchmark uses hugepages for optimal memory performance.
 *
 * Memory requirements (approximate):
 *   - Arena: 2GB
 *   - Rules: ~100KB (for 100 rules)
 *   - Packets: batch_size × num_batches × sizeof(packet)
 *     Default: 32 × 1000000 × ~200 bytes = ~6.4GB
 *   - Total: ~8.5GB
 *
 * Prerequisites:
 *   sudo sysctl -w vm.nr_hugepages=4500  # ~9GB (4500 × 2MB pages)
 *
 * Note: This benchmark should be run WITHOUT AddressSanitizer as ASan
 * does not support hugepage allocations. Compile without -fsanitize=address.
 */

#include "common/memory.h"
#include "common/memory_block.h"
#include "common/network.h"
#include "common/registry.h"
#include "common/rng.h"
#include "dataplane/packet/packet.h"
#include "filter/compiler.h"
#include "filter/filter.h"
#include "filter/query.h"
#include "filter/tests/helpers.h"
#include "lib/utils/packet.h"
#include "logging/log.h"

#include <assert.h>
#include <getopt.h>
#include <netinet/in.h>
#include <rte_byteorder.h>
#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>
#include <rte_udp.h>
#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <time.h>
#include <unistd.h>

////////////////////////////////////////////////////////////////////////////////
// Filter signature declarations

FILTER_COMPILER_DECLARE(bench_dst, net6_fast_dst);
FILTER_QUERY_DECLARE(bench_dst, net6_fast_dst);

FILTER_COMPILER_DECLARE(bench_dst_port, net6_fast_dst, port_dst);
FILTER_QUERY_DECLARE(bench_dst_port, net6_fast_dst, port_dst);

FILTER_COMPILER_DECLARE(bench_dst_port_proto, net6_fast_dst, port_dst, proto);
FILTER_QUERY_DECLARE(bench_dst_port_proto, net6_fast_dst, port_dst, proto);

////////////////////////////////////////////////////////////////////////////////
// Configuration and types

enum signature_type {
	sig_net6_dst = 0,
	sig_net6_dst_port = 1,
	sig_net6_dst_port_proto = 2,
};

struct bench_config {
	enum signature_type sig_type;
	size_t num_rules;
	size_t batch_size;
	size_t num_batches;
};

struct bench_stats {
	uint64_t total_packets;
	uint64_t total_time_ns;
};

////////////////////////////////////////////////////////////////////////////////
// Hugepage allocator (similar to balancer bench)

struct hugepage_allocator {
	void *arena;
	size_t size;
	size_t allocated;
};

static void
hugepage_allocator_init(
	struct hugepage_allocator *alloc, void *arena, size_t size
) {
	alloc->arena = arena;
	alloc->size = size;
	alloc->allocated = 0;
}

static uint8_t *
hugepage_alloc(void *alloc_ptr, size_t align, size_t size) {
	struct hugepage_allocator *alloc =
		(struct hugepage_allocator *)alloc_ptr;

	size_t shift = 0;
	uintptr_t start = (uintptr_t)alloc->arena + alloc->allocated;
	if (start % align != 0) {
		shift = align - start % align;
	}
	size += shift;
	if (alloc->allocated + size > alloc->size) {
		return NULL;
	}
	uint8_t *ptr = (uint8_t *)alloc->arena + alloc->allocated;
	alloc->allocated += size;
	return ptr + shift;
}

////////////////////////////////////////////////////////////////////////////////
// Helper functions

/**
 * Allocate memory using hugepages
 */
static void *
allocate_hugepage_memory(size_t size) {
	void *mem =
		mmap(NULL,
		     size,
		     PROT_READ | PROT_WRITE,
		     MAP_PRIVATE | MAP_ANONYMOUS | MAP_HUGETLB | MAP_POPULATE,
		     -1,
		     0);

	if (mem == MAP_FAILED) {
		fprintf(stderr,
			"Failed to allocate %zu bytes from hugepages\n",
			size);
		fprintf(stderr,
			"Make sure hugepages are configured: sudo sysctl -w "
			"vm.nr_hugepages=5200\n");
		return NULL;
	}

	return mem;
}

static const char *
signature_type_to_string(enum signature_type type) {
	switch (type) {
	case sig_net6_dst:
		return "net6_dst";
	case sig_net6_dst_port:
		return "net6_dst_port";
	case sig_net6_dst_port_proto:
		return "net6_dst_port_proto";
	}
	return "unknown";
}

/**
 * Create IPv6 prefix mask from prefix length
 */
static void
ipv6_prefix_mask(uint8_t prefix_len, uint8_t mask[16]) {
	memset(mask, 0, 16);
	for (uint8_t i = 0; i < prefix_len / 8; i++) {
		mask[i] = 0xff;
	}
	if (prefix_len % 8) {
		mask[prefix_len / 8] = (0xff << (8 - (prefix_len % 8))) & 0xff;
	}
}

////////////////////////////////////////////////////////////////////////////////
// Rule generation with high match probability

static void
generate_rules(
	struct filter_rule *rules,
	struct filter_rule_builder *builders,
	size_t num_rules,
	enum signature_type sig_type,
	uint64_t *rng
) {
	// Use IPv6 range 2001:db8::/32 for high match probability
	// Generate rules with /48 to /64 prefixes for good coverage

	for (size_t i = 0; i < num_rules; i++) {
		builder_init(&builders[i]);

		// Generate destination IPv6 with prefix 48-64
		uint8_t prefix_len = 48 + (rng_next(rng) % 17); // 48-64
		struct net6 net;

		// 2001:db8:xxxx:xxxx::/48-64
		net.addr[0] = 0x20;
		net.addr[1] = 0x01;
		net.addr[2] = 0x0d;
		net.addr[3] = 0xb8;

		// Random middle bytes
		for (int j = 4; j < 16; j++) {
			net.addr[j] = (uint8_t)(rng_next(rng) % 256);
		}

		// Create mask
		ipv6_prefix_mask(prefix_len, net.mask);

		// Apply mask to address
		for (int j = 0; j < 16; j++) {
			net.addr[j] &= net.mask[j];
		}

		builder_add_net6_dst(&builders[i], net);

		// Add port range if needed
		if (sig_type == sig_net6_dst_port ||
		    sig_type == sig_net6_dst_port_proto) {
			// Use common port ranges for high match probability
			uint16_t port_ranges[][2] = {
				{80, 80},
				{443, 443},
				{8080, 8080},
				{3000, 3100},
				{5000, 5100},
				{9000, 9100},
			};
			size_t range_idx =
				rng_next(rng) %
				(sizeof(port_ranges) / sizeof(port_ranges[0]));
			builder_add_port_dst_range(
				&builders[i],
				port_ranges[range_idx][0],
				port_ranges[range_idx][1]
			);
		}

		// Add protocol if needed
		if (sig_type == sig_net6_dst_port_proto) {
			// Alternate between TCP and UDP
			uint8_t proto =
				(i % 2 == 0) ? IPPROTO_TCP : IPPROTO_UDP;
			builder_set_proto(&builders[i], proto, 0, 0);
		}

		rules[i] = build_rule(
			&builders[i], (i + 1) | ACTION_NON_TERMINATE
		);
	}
}

////////////////////////////////////////////////////////////////////////////////
// Packet generation with high match probability

static void
generate_packet_data(
	struct packet_data *packets,
	size_t num_packets,
	enum signature_type sig_type __attribute__((unused)),
	uint64_t *rng,
	void *packet_buffers
) {
	// Each packet needs space for ethernet + IPv6 + TCP/UDP headers
	const size_t packet_size = 128; // Enough for headers

	for (size_t i = 0; i < num_packets; i++) {
		uint8_t *buf = (uint8_t *)packet_buffers + i * packet_size;

		// Build packet in buffer
		struct rte_ether_hdr *eth = (struct rte_ether_hdr *)buf;
		struct rte_ipv6_hdr *ip6 = (struct rte_ipv6_hdr *)(eth + 1);

		// Generate source IPv6 from fe80::/10 range (link-local)
		uint8_t src_ip[16] = {
			0xfe,
			0x80,
			0,
			0,
			0,
			0,
			0,
			0,
			(uint8_t)(rng_next(rng) % 256),
			(uint8_t)(rng_next(rng) % 256),
			(uint8_t)(rng_next(rng) % 256),
			(uint8_t)(rng_next(rng) % 256),
			(uint8_t)(rng_next(rng) % 256),
			(uint8_t)(rng_next(rng) % 256),
			(uint8_t)(rng_next(rng) % 256),
			(uint8_t)(rng_next(rng) % 256)
		};

		// Generate destination IPv6 from 2001:db8::/32 range to match
		// rules
		uint8_t dst_ip[16] = {
			0x20,
			0x01,
			0x0d,
			0xb8,
			(uint8_t)(rng_next(rng) % 256),
			(uint8_t)(rng_next(rng) % 256),
			(uint8_t)(rng_next(rng) % 256),
			(uint8_t)(rng_next(rng) % 256),
			(uint8_t)(rng_next(rng) % 256),
			(uint8_t)(rng_next(rng) % 256),
			(uint8_t)(rng_next(rng) % 256),
			(uint8_t)(rng_next(rng) % 256),
			(uint8_t)(rng_next(rng) % 256),
			(uint8_t)(rng_next(rng) % 256),
			(uint8_t)(rng_next(rng) % 256),
			(uint8_t)(rng_next(rng) % 256)
		};

		// Generate ports from common ranges
		uint16_t src_port = 10000 + (rng_next(rng) % 50000);
		uint16_t dst_port;
		uint16_t common_ports[] = {80, 443, 8080, 3050, 5050, 9050};
		dst_port = common_ports[rng_next(rng) % 6];

		// Alternate between TCP and UDP
		uint8_t proto = (i % 2 == 0) ? IPPROTO_TCP : IPPROTO_UDP;

		// Fill ethernet header
		eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);

		// Fill IPv6 header
		ip6->vtc_flow = rte_cpu_to_be_32(0x60000000); // Version 6
		ip6->payload_len = rte_cpu_to_be_16(
			proto == IPPROTO_UDP ? sizeof(struct rte_udp_hdr)
					     : sizeof(struct rte_tcp_hdr)
		);
		ip6->proto = proto;
		ip6->hop_limits = 64;
		memcpy(ip6->src_addr, src_ip, 16);
		memcpy(ip6->dst_addr, dst_ip, 16);

		// Fill L4 header
		if (proto == IPPROTO_UDP) {
			struct rte_udp_hdr *udp =
				(struct rte_udp_hdr *)(ip6 + 1);
			udp->src_port = rte_cpu_to_be_16(src_port);
			udp->dst_port = rte_cpu_to_be_16(dst_port);
			udp->dgram_len = rte_cpu_to_be_16(sizeof(*udp));
			udp->dgram_cksum = 0;
		} else {
			struct rte_tcp_hdr *tcp =
				(struct rte_tcp_hdr *)(ip6 + 1);
			tcp->src_port = rte_cpu_to_be_16(src_port);
			tcp->dst_port = rte_cpu_to_be_16(dst_port);
			tcp->tcp_flags = 0;
		}

		size_t total_size =
			sizeof(*eth) + sizeof(*ip6) +
			(proto == IPPROTO_UDP ? sizeof(struct rte_udp_hdr)
					      : sizeof(struct rte_tcp_hdr));

		packets[i] = (struct packet_data){
			.data = buf,
			.size = total_size,
			.tx_device_id = 0,
			.rx_device_id = 0,
		};
	}
}

////////////////////////////////////////////////////////////////////////////////
// Benchmark execution

static int
run_benchmark(
	struct filter *filter,
	enum signature_type sig_type,
	struct packet **packets,
	size_t batch_size,
	size_t num_batches,
	struct bench_stats *stats
) {
	stats->total_packets = batch_size * num_batches;

	// Allocate ranges using hugepages
	size_t ranges_size = sizeof(struct value_range *) * batch_size;
	struct value_range **ranges =
		(struct value_range **)allocate_hugepage_memory(ranges_size);
	if (ranges == NULL) {
		return -1;
	}

	// Wait for user input before starting benchmark
	printf("\nReady to start benchmark (PID: %d)\n", getpid());
	printf("Press ENTER to start (you can attach perf now)...\n");
	printf("Example: sudo perf record -p %d -g\n", getpid());
	getchar();

	printf("Starting benchmark...\n");

	struct timespec start_time, end_time;
	clock_gettime(CLOCK_MONOTONIC, &start_time);

	for (size_t batch_idx = 0; batch_idx < num_batches; batch_idx++) {
		// Query the filter
		switch (sig_type) {
		case sig_net6_dst:
			FILTER_QUERY(
				filter,
				bench_dst,
				packets + batch_idx * batch_size,
				ranges,
				batch_size
			);
			break;
		case sig_net6_dst_port:
			FILTER_QUERY(
				filter,
				bench_dst_port,
				packets + batch_idx * batch_size,
				ranges,
				batch_size
			);
			break;
		case sig_net6_dst_port_proto:
			FILTER_QUERY(
				filter,
				bench_dst_port_proto,
				packets + batch_idx * batch_size,
				ranges,
				batch_size
			);
			break;
		}
	}

	clock_gettime(CLOCK_MONOTONIC, &end_time);
	stats->total_time_ns =
		(end_time.tv_sec - start_time.tv_sec) * 1000000000ULL +
		(end_time.tv_nsec - start_time.tv_nsec);

	munmap(ranges, ranges_size);
	return 0;
}

////////////////////////////////////////////////////////////////////////////////
// Results reporting

static void
print_results(const struct bench_config *config, struct bench_stats *stats) {
	// Calculate throughput
	double elapsed_sec = stats->total_time_ns / 1e9;
	double pps = stats->total_packets / elapsed_sec;
	double mpps = pps / 1e6;

	printf("\n");
	printf("=== Filter Benchmark: net6_fast ===\n");
	printf("Signature: %s\n", signature_type_to_string(config->sig_type));
	printf("Rules: %zu\n", config->num_rules);
	printf("Batch Size: %zu\n", config->batch_size);
	printf("Batches: %zu\n", config->num_batches);
	printf("\n");
	printf("Results:\n");
	printf("--------\n");
	printf("Total Packets: %lu\n", stats->total_packets);
	printf("Elapsed Time: %.3f seconds\n", elapsed_sec);
	printf("Throughput: %.2f Mpps (%.0f pps)\n", mpps, pps);
	printf("\n");
}

////////////////////////////////////////////////////////////////////////////////
// Main

static void
print_usage(const char *prog_name) {
	printf("Usage: %s [OPTIONS]\n", prog_name);
	printf("\n");
	printf("Options:\n");
	printf("  -s, --signature TYPE    Signature type: net6_dst (default), "
	       "net6_dst_port, net6_dst_port_proto\n");
	printf("  -r, --rules NUM         Number of rules (default: 100)\n");
	printf("  -b, --batch-size NUM    Batch size (default: 32)\n");
	printf("  -n, --batches NUM       Number of batches (default: "
	       "1000000)\n");
	printf("  -h, --help              Show this help message\n");
	printf("\n");
}

int
main(int argc, char **argv) {
	// Default configuration
	struct bench_config config = {
		.sig_type = sig_net6_dst,
		.num_rules = 100,
		.batch_size = 32,
		.num_batches = 1000000,
	};

	// Parse command-line arguments
	static struct option long_options[] = {
		{"signature", required_argument, 0, 's'},
		{"rules", required_argument, 0, 'r'},
		{"batch-size", required_argument, 0, 'b'},
		{"batches", required_argument, 0, 'n'},
		{"help", no_argument, 0, 'h'},
		{0, 0, 0, 0}
	};

	int opt;
	while ((opt = getopt_long(argc, argv, "s:r:b:n:h", long_options, NULL)
	       ) != -1) {
		switch (opt) {
		case 's':
			if (strcmp(optarg, "net6_dst") == 0) {
				config.sig_type = sig_net6_dst;
			} else if (strcmp(optarg, "net6_dst_port") == 0) {
				config.sig_type = sig_net6_dst_port;
			} else if (strcmp(optarg, "net6_dst_port_proto") == 0) {
				config.sig_type = sig_net6_dst_port_proto;
			} else {
				fprintf(stderr,
					"Unknown signature type: %s\n",
					optarg);
				print_usage(argv[0]);
				return 1;
			}
			break;
		case 'r':
			config.num_rules = atoi(optarg);
			break;
		case 'b':
			config.batch_size = atoi(optarg);
			break;
		case 'n':
			config.num_batches = atoi(optarg);
			break;
		case 'h':
			print_usage(argv[0]);
			return 0;
		default:
			print_usage(argv[0]);
			return 1;
		}
	}

	// Validate configuration
	if (config.num_rules == 0 || config.batch_size == 0 ||
	    config.num_batches == 0) {
		fprintf(stderr, "Error: All counts must be greater than 0\n");
		return 1;
	}

	log_enable_name("info");

	// Allocate memory arena using hugepages
	const size_t arena_size = 1ull << 28; // 256MB
	void *arena = allocate_hugepage_memory(arena_size);
	if (arena == NULL) {
		return 1;
	}
	printf("Allocated %zu MB arena using hugepages\n",
	       arena_size / (1024 * 1024));

	struct block_allocator allocator;
	int res = block_allocator_init(&allocator);
	assert(res == 0);
	block_allocator_put_arena(&allocator, arena, arena_size);

	struct memory_context memory_context;
	res = memory_context_init(&memory_context, "bench", &allocator);
	assert(res == 0);

	// Generate rules using hugepages
	printf("Generating %zu rules...\n", config.num_rules);
	size_t rules_size = sizeof(struct filter_rule) * config.num_rules;
	struct filter_rule *rules =
		(struct filter_rule *)allocate_hugepage_memory(rules_size);
	if (rules == NULL) {
		munmap(arena, arena_size);
		return 1;
	}

	size_t builders_size =
		sizeof(struct filter_rule_builder) * config.num_rules;
	struct filter_rule_builder *builders = (struct filter_rule_builder *)
		allocate_hugepage_memory(builders_size);
	if (builders == NULL) {
		munmap(rules, rules_size);
		munmap(arena, arena_size);
		return 1;
	}

	uint64_t rng = 332111;
	generate_rules(
		rules, builders, config.num_rules, config.sig_type, &rng
	);

	// Initialize filter
	printf("Initializing filter...\n");
	struct filter filter;
	switch (config.sig_type) {
	case sig_net6_dst:
		res = FILTER_INIT(
			&filter,
			bench_dst,
			rules,
			config.num_rules,
			&memory_context
		);
		break;
	case sig_net6_dst_port:
		res = FILTER_INIT(
			&filter,
			bench_dst_port,
			rules,
			config.num_rules,
			&memory_context
		);
		break;
	case sig_net6_dst_port_proto:
		res = FILTER_INIT(
			&filter,
			bench_dst_port_proto,
			rules,
			config.num_rules,
			&memory_context
		);
		break;
	}
	assert(res == 0);

	// Allocate hugepage memory for packets
	size_t total_packets = config.batch_size * config.num_batches;
	printf("Generating %zu packets... (%zu x %zu)\n",
	       total_packets,
	       config.batch_size,
	       config.num_batches);

	// Allocate memory for packet data structures
	size_t packet_data_size = sizeof(struct packet_data) * total_packets;
	struct packet_data *packet_data_array =
		(struct packet_data *)allocate_hugepage_memory(packet_data_size
		);
	if (packet_data_array == NULL) {
		munmap(builders, builders_size);
		munmap(rules, rules_size);
		munmap(arena, arena_size);
		return 1;
	}

	// Allocate memory for packet buffers (128 bytes per packet)
	size_t packet_buffers_size = 128 * total_packets;
	void *packet_buffers = allocate_hugepage_memory(packet_buffers_size);
	if (packet_buffers == NULL) {
		munmap(packet_data_array, packet_data_size);
		munmap(builders, builders_size);
		munmap(rules, rules_size);
		munmap(arena, arena_size);
		return 1;
	}

	// Generate packet data
	generate_packet_data(
		packet_data_array,
		total_packets,
		config.sig_type,
		&rng,
		packet_buffers
	);

	// Allocate hugepage memory for packet allocator
	size_t packet_alloc_size = 10 * 1024 * 1024 * (size_t)1024; // 10GB
	void *packet_alloc_arena = allocate_hugepage_memory(packet_alloc_size);
	if (packet_alloc_arena == NULL) {
		munmap(packet_buffers, packet_buffers_size);
		munmap(packet_data_array, packet_data_size);
		munmap(builders, builders_size);
		munmap(rules, rules_size);
		munmap(arena, arena_size);
		return 1;
	}

	struct hugepage_allocator packet_allocator;
	hugepage_allocator_init(
		&packet_allocator, packet_alloc_arena, packet_alloc_size
	);

	// Create packet list using custom allocator
	struct packet_list packet_list;
	res = fill_packet_list_custom_alloc(
		&packet_list,
		total_packets,
		packet_data_array,
		0, // auto-calculate mbuf size
		&packet_allocator,
		hugepage_alloc
	);
	if (res != 0) {
		fprintf(stderr, "Failed to create packet list\n");
		munmap(packet_alloc_arena, packet_alloc_size);
		munmap(packet_buffers, packet_buffers_size);
		munmap(packet_data_array, packet_data_size);
		munmap(builders, builders_size);
		munmap(rules, rules_size);
		munmap(arena, arena_size);
		return 1;
	}

	// Convert packet list to array
	size_t packets_array_size = sizeof(struct packet *) * total_packets;
	struct packet **packets =
		(struct packet **)allocate_hugepage_memory(packets_array_size);
	if (packets == NULL) {
		munmap(packet_alloc_arena, packet_alloc_size);
		munmap(packet_buffers, packet_buffers_size);
		munmap(packet_data_array, packet_data_size);
		munmap(builders, builders_size);
		munmap(rules, rules_size);
		munmap(arena, arena_size);
		return 1;
	}

	size_t idx = 0;
	struct packet *p;
	while ((p = packet_list_pop(&packet_list)) != NULL) {
		packets[idx++] = p;
	}
	assert(idx == total_packets);

	// Run benchmark
	struct bench_stats stats;
	res = run_benchmark(
		&filter,
		config.sig_type,
		packets,
		config.batch_size,
		config.num_batches,
		&stats
	);
	assert(res == 0);

	// Print results
	print_results(&config, &stats);

	// Cleanup
	munmap(packets, packets_array_size);
	munmap(packet_alloc_arena, packet_alloc_size);
	munmap(packet_buffers, packet_buffers_size);
	munmap(packet_data_array, packet_data_size);
	munmap(builders, builders_size);
	munmap(rules, rules_size);
	munmap(arena, arena_size);

	return 0;
}