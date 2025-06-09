#pragma once

#include <stdint.h>

#include "mode.h"
#include "ring.h"

struct agent;
struct cp_module;
struct ring_buffer;

extern const uint32_t default_snaplen;
extern const uint32_t max_ring_size;
extern const uint32_t ring_msg_magic;

// From rte_log.h
/* Can't use 0, as it gives compiler warnings */
#define RTE_LOG_EMERG 1U   /**< System is unusable.               */
#define RTE_LOG_ALERT 2U   /**< Action must be taken immediately. */
#define RTE_LOG_CRIT 3U	   /**< Critical conditions.              */
#define RTE_LOG_ERR 4U	   /**< Error conditions.                 */
#define RTE_LOG_WARNING 5U /**< Warning conditions.               */
#define RTE_LOG_NOTICE 6U  /**< Normal but significant condition. */
#define RTE_LOG_INFO 7U	   /**< Informational.                    */
#define RTE_LOG_DEBUG 8U   /**< Debug-level messages.             */

// make log levels visible to CGO by their names
enum pdump_log_level {
	log_emerg = RTE_LOG_EMERG,
	log_alert = RTE_LOG_ALERT,
	log_crit = RTE_LOG_CRIT,
	log_error = RTE_LOG_ERR,
	log_warn = RTE_LOG_WARNING,
	log_notice = RTE_LOG_NOTICE,
	log_info = RTE_LOG_INFO,
	log_debug = RTE_LOG_DEBUG,
};

// Create a new configuration for the pdump module
struct cp_module *
pdump_module_config_create(struct agent *agent, const char *name);

void
pdump_module_config_free(struct cp_module *module);

// Set filter compiles and sets new bpf filter for the pdump module
int
pdump_module_config_set_filter(
	struct cp_module *module, char *filter, uintptr_t cb
);

// Configures the pdump module's dump mode.
// The 'mode' parameter specifies the packet list that pdump should read from.
int
pdump_module_config_set_mode(struct cp_module *module, enum pdump_mode mode);

// Set the maximum packet length to be captured by the pdump module.
// If a filter is already set, setting the snaplen will trigger filter
// recompilation. This triggers a call to pdump_module_config_set_filter.
int
pdump_module_config_set_snaplen(
	struct cp_module *module, uint32_t snaplen, uintptr_t cb
);

// Initialize worker ring buffers for packet dumping.
struct ring_buffer *
pdump_module_config_set_per_worker_ring(
	struct cp_module *module, uint32_t size, uint64_t *worker_count
);

// Converts a shared memory offset to a direct memory address.
uint8_t *
pdump_module_config_addr_of(uint8_t **offset);
