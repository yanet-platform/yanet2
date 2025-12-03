#include <errno.h>
#include <pcap/pcap.h>

#include <bpf_impl.h>
#include <rte_bpf.h>

#include "yanet_build_config.h"

#include "config.h"
#include "controlplane.h"

#include "hacks.h"

#include "common/memory_address.h"

#include "controlplane/agent/agent.h"
#include "dataplane/config/zone.h"

const uint32_t default_snaplen = MBUF_MAX_SIZE;
const uint32_t max_ring_size = MEMORY_BLOCK_ALLOCATOR_MAX_SIZE;
const uint32_t ring_msg_magic = RING_MSG_MAGIC;

#define pdump_log(level, fmt_, ...) rte_log(level, 0, fmt_, ##__VA_ARGS__)

static struct rte_bpf_prm *
pdump_compile_filter(char *filter, size_t snaplen) {
	pcap_t *pcap = pcap_open_dead(DLT_EN10MB, snaplen);
	if (!pcap) {
		pdump_log(RTE_LOG_ERR, "failed to initialize pcap handler");
		return NULL;
	}

	struct bpf_program bf;
	if (pcap_compile(pcap, &bf, filter, 1, PCAP_NETMASK_UNKNOWN) != 0) {
		pdump_log(
			RTE_LOG_ERR,
			"failed to compile pcap filter: %s",
			pcap_geterr(pcap)
		);
		pcap_close(pcap);
		return NULL;
	}

	struct rte_bpf_prm *bpf_prm = rte_bpf_convert(&bf);
	if (bpf_prm == NULL) {
		pdump_log(
			RTE_LOG_ERR, "failed to convert pcap BPF to dpdk eBPF"
		);
		pcap_freecode(&bf);
		pcap_close(pcap);
		return NULL;
	}

	pcap_freecode(&bf);
	pcap_close(pcap);
	return bpf_prm;
}

static int
pdump_module_config_update_filter_str(struct cp_module *module, char *filter) {
	struct pdump_module_config *config =
		container_of(module, struct pdump_module_config, cp_module);
	struct agent *agent = ADDR_OF(&config->cp_module.agent);

	char *old_filter = ADDR_OF(&config->filter);
	if (old_filter != filter) {
		if (old_filter != NULL) {
			memory_bfree(
				&agent->memory_context,
				old_filter,
				strlen(old_filter) + 1
			);
		}
		pdump_log(RTE_LOG_INFO, "update filter string");
		uint64_t filter_len = strlen(filter) + 1; // +1 for '\0'
		char *filter_buf =
			memory_balloc(&agent->memory_context, filter_len);
		if (filter_buf == NULL) {
			errno = ENOMEM;
			return -1;
		}
		memcpy(filter_buf, filter, filter_len);
		SET_OFFSET_OF(&config->filter, filter_buf);
	}
	return 0;
}

int
pdump_module_config_data_init(
	struct pdump_module_config *config,
	struct memory_context *memory_context
) {
	(void)memory_context;

	config->filter = NULL;
	config->ebpf_program = NULL;
	config->mode = PDUMP_INPUT;
	config->snaplen = default_snaplen;
	config->rings = NULL;
	return 0;
}

struct cp_module *
pdump_module_config_create(struct agent *agent, const char *name) {
	struct pdump_module_config *config =
		(struct pdump_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct pdump_module_config)
		);
	if (config == NULL) {
		errno = ENOMEM;
		return NULL;
	}

	if (cp_module_init(&config->cp_module, agent, "pdump", name)) {
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct pdump_module_config)
		);
	}

	// Initialize the module data
	if (pdump_module_config_data_init(
		    config, &config->cp_module.memory_context
	    )) {
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct pdump_module_config)
		);
		return NULL;
	}

	return &config->cp_module;
}

void
pdump_module_config_free(struct cp_module *module) {
	struct pdump_module_config *config =
		container_of(module, struct pdump_module_config, cp_module);

	struct agent *agent = ADDR_OF(&module->agent);
	char *filter = ADDR_OF(&config->filter);
	if (filter != NULL) {
		memory_bfree(
			&agent->memory_context, filter, strlen(filter) + 1
		);
	}

	struct rte_bpf *ebpf = ADDR_OF(&config->ebpf_program);
	if (ebpf != NULL) {
		memory_bfree(&agent->memory_context, ebpf, ebpf->sz);
	}

	struct ring_buffer *rings = ADDR_OF(&config->rings);
	if (rings != NULL) {
		struct dp_config *dp_config = ADDR_OF(&agent->dp_config);
		uint32_t wc = dp_config->worker_count;

		for (uint32_t idx = 0; idx < wc; idx++) {
			struct ring_buffer *ring = rings + idx;
			uint8_t *data = ADDR_OF(&ring->data);
			if (data != NULL) {
				memory_bfree(
					&agent->memory_context, data, ring->size
				);
			}
		}
		memory_bfree(
			&agent->memory_context,
			rings,
			sizeof(struct ring_buffer) * wc
		);
	}
	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct pdump_module_config)
	);
};

int
pdump_module_config_set_filter(
	struct cp_module *module, char *filter, uintptr_t cb
) {
	callback_handle = cb;
	struct pdump_module_config *config =
		container_of(module, struct pdump_module_config, cp_module);
	struct agent *agent = ADDR_OF(&config->cp_module.agent);

	size_t snaplen =
		config->snaplen > 0 ? config->snaplen : default_snaplen;
	struct rte_bpf_prm *params = pdump_compile_filter(filter, snaplen);
	if (params == NULL) {
		errno = per_lcore__rte_errno;
		return -1;
	}
	pdump_log(
		RTE_LOG_INFO,
		"filter '%s' compiles to %d instructions, with %d xsym",
		filter,
		params->nb_ins,
		params->nb_xsym
	);
	if (params->nb_xsym != 0) {
		// params are allocated in contiguous memory for the struct
		// and for the instructions
		free(params);
		pdump_log(
			RTE_LOG_ERR, "eBPF external symbols are not supported"
		);
		errno = EPERM;
		return -1;
	}

	struct rte_bpf *bpf_on_heap = rte_bpf_load(params);
	free(params); // we do not need it anymore
	if (bpf_on_heap == NULL) {
		pdump_log(RTE_LOG_ERR, "failed to load bpf");
		errno = per_lcore__rte_errno;
		return -1;
	}

	// Allocate space in shared memory for struct rte_bpf and EBPF code
	uint64_t buf_sz = bpf_on_heap->sz;
	uint8_t *buf = memory_balloc(&agent->memory_context, buf_sz);
	if (buf == NULL) {
		pdump_log(
			RTE_LOG_ERR, "failed to ballocate memory for eBPF code"
		);
		rte_bpf_destroy(bpf_on_heap);
		errno = ENOMEM;
		return -1;
	}

	// Copy struct rte_bpf and instructions which lie in memory immediately
	// following the struct
	memcpy(buf, bpf_on_heap, buf_sz);
	rte_bpf_destroy(bpf_on_heap); // We do not need it anymore

	if (pdump_module_config_update_filter_str(module, filter) == -1) {
		memory_bfree(&agent->memory_context, buf, buf_sz);
		pdump_log(
			RTE_LOG_ERR, "failed to ballocate memory for filter str"
		);
		errno = ENOMEM;
		return -1;
	};

	struct rte_bpf *bpf = (struct rte_bpf *)buf;
	// Currently not supported
	bpf->jit.func = NULL;
	bpf->jit.sz = 0;

	struct ebpf_insn *ins;
	struct rte_bpf_xsym *xsyms;

	size_t bsz = sizeof(bpf[0]);
	size_t xsz = bpf->prm.nb_xsym * sizeof(xsyms[0]);

	xsyms = (struct rte_bpf_xsym *)(buf + bsz);
	SET_OFFSET_OF(&bpf->prm.xsym, xsyms);

	ins = (struct ebpf_insn *)(buf + bsz + xsz);
	SET_OFFSET_OF(&bpf->prm.ins, ins);

	SET_OFFSET_OF(&config->ebpf_program, bpf);

	return 0;
}

int
pdump_module_config_set_mode(struct cp_module *module, enum pdump_mode mode) {
	struct pdump_module_config *config =
		container_of(module, struct pdump_module_config, cp_module);

	config->mode = mode;

	return 0;
}

int
pdump_module_config_set_snaplen(
	struct cp_module *module, uint32_t snaplen, uintptr_t cb
) {
	struct pdump_module_config *config =
		container_of(module, struct pdump_module_config, cp_module);

	if (snaplen == 0) {
		snaplen = default_snaplen;
	}

	config->snaplen = snaplen;

	if (config->filter != NULL) {
		char *filter = ADDR_OF(&config->filter);
		return pdump_module_config_set_filter(module, filter, cb);
	}

	return 0;
}

struct ring_buffer *
pdump_module_config_set_per_worker_ring(
	struct cp_module *module, uint32_t size, uint64_t *worker_count
) {

	if (__builtin_popcount(size) > 1) {
		pdump_log(RTE_LOG_ERR, "ring size must be a power of two");
		errno = EINVAL;
		return NULL;
	}
	if (size > max_ring_size) {
		pdump_log(
			RTE_LOG_ERR,
			"ring size exceeds maximum: %u > %u",
			size,
			max_ring_size
		);
		errno = E2BIG;
		return NULL;
	}

	struct pdump_module_config *config =
		container_of(module, struct pdump_module_config, cp_module);

	struct agent *agent = ADDR_OF(&config->cp_module.agent);
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);

	uint64_t rings_meta_size =
		sizeof(struct ring_buffer) * dp_config->worker_count;

	struct ring_buffer *rings =
		memory_balloc(&agent->memory_context, rings_meta_size);
	if (rings == NULL) {
		pdump_log(
			RTE_LOG_INFO,
			"failed to ballocate %lu bytes for rings metadata",
			rings_meta_size
		);
		errno = ENOMEM;
		return NULL;
	}
	memset(rings, 0, rings_meta_size);

	for (size_t idx = 0; idx < dp_config->worker_count; idx++) {
		struct ring_buffer *ring = rings + idx;
		uint8_t *ring_data =
			memory_balloc(&agent->memory_context, size);
		if (ring_data == NULL) {
			pdump_log(
				RTE_LOG_ERR,
				"failed to ballocate data for ring %lu",
				idx
			);

			for (size_t j = 0; j < idx; j++) {
				ring = rings + j;
				ring_data = ADDR_OF(&ring->data);
				memory_bfree(
					&agent->memory_context,
					ring_data,
					ring->size
				);
			}
			memory_bfree(
				&agent->memory_context, rings, rings_meta_size
			);
			errno = ENOMEM;
			return NULL;
		}
		ring->size = size;
		ring->mask = size - 1;
		SET_OFFSET_OF(&ring->data, ring_data);
	}

	*worker_count = dp_config->worker_count;
	SET_OFFSET_OF(&config->rings, rings);

	return rings;
}

uint8_t *
pdump_module_config_addr_of(uint8_t **offset) {
	return ADDR_OF(offset);
}
