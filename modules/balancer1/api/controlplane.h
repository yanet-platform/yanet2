#include <stddef.h>
#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

// Table of the established sessions
struct balancer_session_table;

// Responsible for the module configuration
struct agent;

// Config of the virtual service
struct balancer_vs_config;

// Instance of module configuration
struct cp_module;

////////////////////////////////////////////////////////////////////////////////

// Allocate new session table
struct balancer_session_table *
balancer_session_table_create(size_t size);

////////////////////////////////////////////////////////////////////////////////

// Allocate new config for the balancer module
struct cp_module *
balancer_module_config_create(
	struct agent *agent,
	const char *name,
	struct balancer_session_table *sessions
);

// Free balancer module config
void
balancer_module_config_free(struct cp_module *cp_module);

// Set timeouts for records of different kinds in the session table
void
balancer_module_config_set_timeouts(
	struct cp_module *module,
	uint32_t tcp_syn_ack_timeout,
	uint32_t tcp_syn_timeout,
	uint32_t tcp_fin_timeout,
	uint32_t tcp_timeout,
	uint32_t udp_timeout,
	uint32_t default_timeout
);

// Add virtual services
int
balancer_module_config_add_vs(
	struct cp_module *module, struct balancer_vs_config **vs, size_t count
);

////////////////////////////////////////////////////////////////////////////////

// Create new virtual service config
struct balancer_vs_config *
balancer_vs_config_create(
	uint8_t flags,
	uint8_t *ip,
	uint16_t port,
	uint8_t proto,
	size_t real_count,
	size_t prefixes_count
);

// Allows to free virtual service config
void
balancer_vs_config_free(struct balancer_vs_config *vs_config);

// Allows to setup one real of the virtual service
void
balancer_vs_config_set_real(
	struct balancer_vs_config *config,
	size_t index,
	uint8_t flags,
	uint16_t weight,
	uint8_t *dst_addr,
	uint8_t *src_addr,
	uint8_t *src_mask
);

// Allows to set one source address of the incoming packet, allowed by virtual
// service
void
balancer_vs_config_set_src_prefix(
	struct balancer_vs_config *service_config,
	size_t index,
	uint8_t *start_addr,
	uint8_t *end_addr
);
