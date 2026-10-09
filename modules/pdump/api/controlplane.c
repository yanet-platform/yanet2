#include <errno.h>
#include <pcap/pcap.h>

#include <bpf_impl.h>
#include <rte_bpf.h>

#include "yanet_build_config.h"

#include "config.h"
#include "controlplane.h"

#include "hacks.h"

#include "common/memory_address.h"
#include "lib/errors/errors.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/dataplane/config/zone.h"
#include "objects/ring/api/ring_object.h"

const uint32_t default_snaplen = MBUF_MAX_SIZE;
const uint32_t pdump_record_magic = PDUMP_RECORD_MAGIC;

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
	config->ring_link_idx = PDUMP_RING_LINK_NONE;
	return 0;
}

static void
pdump_module_config_destroy(struct cp_module *module) {
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

	cp_module_fini(module);

	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct pdump_module_config)
	);
}

struct cp_module *
pdump_module_config_new(
	struct agent *agent, const char *name, yanet_error **err
) {
	struct pdump_module_config *config =
		(struct pdump_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct pdump_module_config)
		);
	if (config == NULL) {
		yanet_error_add(err, "failed to allocate config");
		return NULL;
	}

	if (cp_module_init(&config->cp_module, agent, "pdump", name, err)) {
		yanet_error_add(err, "failed to init module");
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct pdump_module_config)
		);
		return NULL;
	}

	// Initialize the module data
	if (pdump_module_config_data_init(
		    config, &config->cp_module.memory_context
	    )) {
		yanet_error_add(err, "failed to init config data");
		// Frees directly instead of going through the type destructor.
		//
		// A failed configuration-data setup never reaches a state its
		// own teardown could safely walk. No reference beyond the
		// caller's own has been taken, and no registry has observed
		// the module yet, so nothing is lost by freeing the block
		// here.
		cp_module_fini(&config->cp_module);
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct pdump_module_config)
		);
		return NULL;
	}

	return &config->cp_module;
}

int
pdump_module_config_free(struct cp_module *module, yanet_error **err) {
	if (cp_module_try_destroy(module, err)) {
		return -1;
	}

	pdump_module_config_destroy(module);
	return 0;
}

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

	// Free old eBPF program before setting new one
	struct rte_bpf *old_ebpf = ADDR_OF(&config->ebpf_program);
	if (old_ebpf != NULL) {
		memory_bfree(&agent->memory_context, old_ebpf, old_ebpf->sz);
	}

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

int
pdump_module_config_link_ring(
	struct cp_module *module, const char *ring_name, yanet_error **err
) {
	struct pdump_module_config *config =
		container_of(module, struct pdump_module_config, cp_module);

	return cp_module_link_object(
		module, RING_OBJECT_TYPE, ring_name, &config->ring_link_idx, err
	);
}
