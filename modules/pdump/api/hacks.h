#pragma once

#include <rte_mbuf.h>
#include <stdio.h>

#include "cgo_exports.h"

__thread uintptr_t callback_handle = 0;

// HACK: Fix undefined reference to unused functions and variables

__thread int per_lcore__rte_errno = 0;

// __rte_pktmbuf_read is used for JIT compilation
// which, in our case, SHOULD happen on the dataplane side.
// So the controlplane function pointer is meaningless.
const void *
__rte_pktmbuf_read( // NOLINT(readability-identifier-naming)
	const struct rte_mbuf *m,
	uint32_t off,
	uint32_t len,
	void *buf
) {
	(void)m, (void)off, (void)len, (void)buf;
	return NULL;
}

void
__rte_panic( // NOLINT(readability-identifier-naming)
	const char *funcname,
	const char *format,
	...
) {
	(void)funcname, (void)format;
	abort();
}

int
rte_log_register_type_and_pick_level(const char *name, uint32_t level_def) {
	(void)name, (void)level_def;
	return 0;
}

// HACK: Provide custom implementations for EAL-guarded external functions

void *
rte_zmalloc(size_t size) {
	return calloc(1, size);
}

int
rte_log(uint32_t level, uint32_t logtype, const char *format, ...) {
	(void)logtype;

	va_list ap;
	int ret;
	char *msg = NULL;

	va_start(ap, format);
	ret = vasprintf(&msg, format, ap);
	va_end(ap);
	if (ret > 0) {
		if (level <= RTE_LOG_NOTICE && callback_handle != 0) {
			goErrorCallback(callback_handle, msg);
		}
		pdumpGoControlplaneLog(level, msg);
	}
	free(msg);
	return ret;
}
