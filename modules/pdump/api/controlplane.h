#pragma once

#include <stdint.h>

#include "lib/errors/errors.h"
#include "mode.h"
#include "record.h"

struct agent;
struct cp_module;

extern const uint32_t default_snaplen;
extern const uint32_t pdump_record_magic;

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
pdump_module_config_new(
	struct agent *agent, const char *name, yanet_error **err
);

// Destroy the module when it is dangling, per cp_module_try_destroy.
//
// Returns -1 with errno EAGAIN while a live generation still references
// the module; the caller must keep its handle and retry later.
int
pdump_module_config_free(struct cp_module *cp_module, yanet_error **err);

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

// Link the module's capture ring by name.
//
// Records an object link on the module config; the dataplane handler finds
// the ring through this link when the execution contexts are built.
// Whether a ring by this name exists is resolved later, not by this call.
// Returns 0 on success, or -1 with err set when the link cannot be
// recorded.
int
pdump_module_config_link_ring(
	struct cp_module *module, const char *ring_name, yanet_error **err
);
