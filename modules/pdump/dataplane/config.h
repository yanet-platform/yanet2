#pragma once

#include "lib/controlplane/config/cp_module.h"

#include "mode.h"
#include "ring.h"

struct rte_bpf;

struct pdump_module_config {
	struct cp_module cp_module;

	char *filter;
	struct rte_bpf *ebpf_program;
	enum pdump_mode mode;
	uint32_t snaplen;
	uint64_t rate_pps;
	uint64_t worker_count;

	struct ring_buffer *rings;
};
