#pragma once

#include "controlplane/config/cp_module.h"

#include "mode.h"
#include "ring.h"

struct rte_bpf;

struct pdump_module_config {
	struct cp_module cp_module;

	char *filter;
	struct rte_bpf *ebpf_program;
	enum pdump_mode mode;
	uint32_t snaplen;

	struct ring_buffer *rings;
};
