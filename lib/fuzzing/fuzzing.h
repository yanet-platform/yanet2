#pragma once

#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

#include "yanet_build_config.h" // MBUF_MAX_SIZE
#include <rte_build_config.h>	// RTE_PKTMBUF_HEADROOM
#include <rte_mbuf.h>
#include <rte_mempool.h>

#include "common/lpm.h"
#include "common/memory.h"
#include "controlplane/config/econtext.h"
#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"
#include "lib/logging/log.h"
#include "mock/worker_mempool.h"

#define FUZZING_ARENA_SIZE (1 << 20)

/**
 * Common fuzzing parameters structure
 * Used across all module fuzzing targets
 */
struct fuzzing_params {
	struct module *module;	     /**< Pointer to the module being tested */
	struct cp_module *cp_module; /**< Module configuration */

	void *arena;		    /**< Memory arena for allocations */
	struct block_allocator ba;  /**< Block allocator */
	struct memory_context mctx; /**< Memory context */

	struct rte_mempool *mempool; /**< DPDK mempool for mbufs */
	struct dp_worker *
		worker; /**< Optional worker context for modules that need it */

	// Module execution context - can be customized per module
	struct module_ectx module_ectx;

	// Stubs for route module (to avoid -Werror=address warnings)
	uint64_t mc_index_stub; /**< Stub mc_index for route module */
	struct config_gen_ectx config_gen_ectx_stub; /**< Stub config_gen_ectx
							for route module */
};

/**
 * Initialize fuzzing parameters with memory arenas and mempool
 *
 * @param params Fuzzing parameters structure to initialize
 * @param name Name for the memory context
 * @param module_loader Function that creates and returns the module instance
 * @return 0 on success, negative error code on failure
 */
static inline int
fuzzing_params_init(
	struct fuzzing_params *params,
	const char *name,
	module_load_handler module_loader
) {
	params->arena = malloc(FUZZING_ARENA_SIZE);
	if (params->arena == NULL) {
		return -ENOMEM;
	}

	block_allocator_init(&params->ba);
	block_allocator_put_arena(
		&params->ba, params->arena, FUZZING_ARENA_SIZE
	);

	memory_context_init(&params->mctx, name, &params->ba);

	LOG(INFO, "Creating mock mempool for fuzzing");
	params->mempool = mock_mempool_create();
	if (params->mempool == NULL) {
		LOG(ERROR, "Failed to create mock mempool");
		return -ENOMEM;
	}

	// Load module using provided loader function
	LOG(INFO, "Loading module for fuzzing: %s", name);
	params->module = module_loader();
	if (params->module == NULL) {
		LOG(ERROR, "Failed to load module");
		return -ENOMEM;
	}

	params->cp_module = NULL;
	params->worker = NULL;

	// Initialize module_ectx to zero
	memset(&params->module_ectx, 0, sizeof(params->module_ectx));

	// Initialize stubs for route module
	// Route module uses module_ectx_encode_device which accesses mc_index
	// We provide a stub that returns LPM_VALUE_INVALID to drop packets
	params->mc_index_stub = LPM_VALUE_INVALID;

	// Route module uses config_gen_ectx_get_device to get device context
	// We provide a stub with device_count=0 so all packets are dropped
	params->config_gen_ectx_stub.device_count = 0;

	LOG(INFO, "Fuzzing parameters initialized for: %s", name);
	return 0;
}

/**
 * Process a single packet through the fuzzing target
 *
 * Allocates mbuf from mock mempool, copies fuzzer input data into it,
 * converts to packet structure, and processes through the module handler.
 *
 * Note: This function is called sequentially from a single thread by libFuzzer.
 * See: https://llvm.org/docs/LibFuzzer.html#parallel-fuzzing
 *
 * @param params Fuzzing parameters containing module and configuration
 * @param data Raw packet data from fuzzer
 * @param size Size of packet data in bytes
 * @return 0 on success, negative error code on failure
 */
static inline int
fuzzing_process_packet(
	struct fuzzing_params *params, const uint8_t *data, size_t size
) {
	if (size > (MBUF_MAX_SIZE - RTE_PKTMBUF_HEADROOM)) {
		LOG_TRACE(
			"Packet size %zu exceeds maximum %d",
			size,
			MBUF_MAX_SIZE - RTE_PKTMBUF_HEADROOM
		);
		return -EINVAL;
	}

	// Allocate mbuf from mempool
	LOG_TRACE("Processing packet of size %zu", size);
	struct rte_mbuf *mbuf = rte_pktmbuf_alloc(params->mempool);
	if (mbuf == NULL) {
		LOG_TRACE("Failed to allocate mbuf from mempool");
		return -ENOMEM;
	}

	// Copy data into mbuf
	char *pkt_data = rte_pktmbuf_mtod(mbuf, char *);
	rte_memcpy(pkt_data, data, size);
	mbuf->data_len = size;
	mbuf->pkt_len = size;

	// Get packet structure from mbuf's buf_addr
	// mbuf_to_packet returns (struct packet *)mbuf->buf_addr
	// which was set by rte_pktmbuf_init in mock_pool_dequeue
	struct packet *packet = mbuf_to_packet(mbuf);
	// Initialize packet structure
	memset(packet, 0, sizeof(struct packet));
	packet->mbuf = mbuf;

	// Create packet front and add packet to input list
	struct packet_front pf;
	packet_front_init(&pf);
	packet_list_add(&pf.input, packet);

	// Parse packet
	if (parse_packet(packet)) {
		LOG_TRACE("Failed to parse packet");
		rte_pktmbuf_free(mbuf);
		return 0;
	}

	// Use module_ectx from params (can be customized per module)
	// Always set cp_module pointer
	SET_OFFSET_OF(&params->module_ectx.cp_module, params->cp_module);

	// Process packet through module
	// Some modules (like fwstate) need a worker context
	params->module->handler(params->worker, &params->module_ectx, &pf);

	// Clean up ALL packet lists to prevent memory leaks
	// The module may have moved packets between lists or left them in input
	struct packet *cleanup_packet;

	// Clean up input packets (in case module didn't process them)
	while ((cleanup_packet = packet_list_pop(&pf.input)) != NULL) {
		struct rte_mbuf *cleanup_mbuf = packet_to_mbuf(cleanup_packet);
		rte_pktmbuf_free(cleanup_mbuf);
	}

	// Clean up output packets if any were generated
	// (e.g., fwstate may generate sync packets)
	while ((cleanup_packet = packet_list_pop(&pf.output)) != NULL) {
		struct rte_mbuf *cleanup_mbuf = packet_to_mbuf(cleanup_packet);
		rte_pktmbuf_free(cleanup_mbuf);
	}

	// Clean up drop packets
	while ((cleanup_packet = packet_list_pop(&pf.drop)) != NULL) {
		struct rte_mbuf *cleanup_mbuf = packet_to_mbuf(cleanup_packet);
		rte_pktmbuf_free(cleanup_mbuf);
	}

	// Clean up pending packets
	while ((cleanup_packet = packet_list_pop(&pf.pending)) != NULL) {
		struct rte_mbuf *cleanup_mbuf = packet_to_mbuf(cleanup_packet);
		rte_pktmbuf_free(cleanup_mbuf);
	}

	return 0;
}
