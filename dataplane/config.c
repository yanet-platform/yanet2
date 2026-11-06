#include "config.h"

#include <ctype.h>
#include <errno.h>
#include <inttypes.h>
#include <stdlib.h>
#include <yaml.h>

#include "common/strutils.h"
#include "lib/dataplane/packet/packet.h"

static void
print_scalar(const char *value, size_t length) {
	fputc('\'', stderr);
	for (size_t idx = 0; idx < length; ++idx) {
		unsigned char character = (unsigned char)value[idx];
		switch (character) {
		case '\\':
			fputs("\\\\", stderr);
			break;
		case '\'':
			fputs("\\'", stderr);
			break;
		case '\0':
			fputs("\\0", stderr);
			break;
		default:
			if (isprint(character)) {
				fputc(character, stderr);
			} else {
				fprintf(stderr, "\\x%02x", character);
			}
			break;
		}
	}
	fputc('\'', stderr);
}

static int
parse_unsigned(
	const char *field,
	const char *value,
	size_t length,
	uint64_t min,
	uint64_t max,
	uint64_t *result
) {
	char *end = NULL;
	const char *first = value;
	while (isspace((unsigned char)*first)) {
		++first;
	}

	errno = 0;
	uintmax_t parsed = strtoumax(value, &end, 10);

	if (*first == '-' || value == end || end != value + length ||
	    errno == ERANGE || parsed < min || parsed > max) {
		fprintf(stderr, "invalid %s value ", field);
		print_scalar(value, length);
		fprintf(stderr,
			" (length %zu): expected an unsigned integer in range "
			"%" PRIu64 "..%" PRIu64 "\n",
			length,
			min,
			max);
		return -1;
	}

	*result = (uint64_t)parsed;
	return 0;
}

static int
parse_bool(
	const char *field, const char *value, size_t length, uint8_t *result
) {
	if (length == 4 && !strncmp(value, "true", length)) {
		*result = 1;
		return 0;
	}
	if (length == 5 && !strncmp(value, "false", length)) {
		*result = 0;
		return 0;
	}
	fprintf(stderr, "invalid %s value ", field);
	print_scalar(value, length);
	fprintf(stderr, ": expected true or false\n");
	return -1;
}

// A memory size is bytes with an optional binary unit: "1073741824",
// "1 GiB" and "1GiB" spell the same size.
static int
parse_memory_size(
	const char *field, const char *value, size_t length, uint64_t *result
) {
	static const char *const units[] = {"KiB", "MiB", "GiB", "TiB"};

	unsigned shift = 0;
	size_t number_length = length;
	for (size_t idx = 0; idx < sizeof(units) / sizeof(units[0]); ++idx) {
		if (length >= 3 && !memcmp(value + length - 3, units[idx], 3)) {
			shift = 10 * (unsigned)(idx + 1);
			number_length = length - 3;
			break;
		}
	}
	while (shift != 0 && number_length > 0 &&
	       isspace((unsigned char)value[number_length - 1])) {
		--number_length;
	}

	char *end = NULL;
	errno = 0;
	uintmax_t number = strtoumax(value, &end, 10);
	if (number_length == 0 || !isdigit((unsigned char)*value) ||
	    end != value + number_length || errno == ERANGE ||
	    number > (UINT64_MAX >> shift)) {
		fprintf(stderr, "invalid %s value ", field);
		print_scalar(value, length);
		fprintf(stderr,
			" (length %zu): expected bytes with an optional "
			"KiB, MiB, GiB or TiB unit\n",
			length);
		return -1;
	}

	*result = (uint64_t)number << shift;
	return 0;
}

// Finds a device by name: its index, -1 when no device carries the name,
// -2 when several do.
static int64_t
lookup_device(const struct dataplane_config *config, const char *name) {
	int64_t found = -1;
	for (uint64_t dev_idx = 0; dev_idx < config->device_count; ++dev_idx) {
		if (!strcmp(config->devices[dev_idx].device_name, name)) {
			if (found >= 0) {
				return -2;
			}
			found = (int64_t)dev_idx;
		}
	}
	return found;
}

static int
resolve_connections(struct dataplane_config *config) {
	for (uint64_t conn_idx = 0; conn_idx < config->connection_count;
	     ++conn_idx) {
		struct dataplane_connection_config *conn =
			config->connections + conn_idx;

		if (conn->src_device[0] == '\0' ||
		    conn->dst_device[0] == '\0') {
			return -1;
		}

		int64_t src_id = lookup_device(config, conn->src_device);
		int64_t dst_id = lookup_device(config, conn->dst_device);
		if (src_id < 0 || dst_id < 0) {
			return -1;
		}

		conn->src_device_id = (uint64_t)src_id;
		conn->device_id = (uint64_t)dst_id;
	}
	return 0;
}

static int
is_virtual_port(const char *port_name) {
	return !strncmp(port_name, "virtio_user_", strlen("virtio_user_")) ||
	       !strncmp(port_name, "net_ring_", strlen("net_ring_"));
}

int
dataplane_device_config_has_roles(const struct dataplane_device_config *device
) {
	for (uint32_t idx = 0; idx < device->queue_count; ++idx) {
		const struct dataplane_queue_role *role =
			&device->queues[idx].role;
		if (!role->rss || role->dscp != 0 || role->arp || role->nd ||
		    role->l2_control) {
			return 1;
		}
	}
	return 0;
}

// Rejects queue roles a device cannot carry.
//
// Unmatched traffic lands on queue 0 when the port hashes nothing, so
// queue 0 stays in the RSS table, and a queue out of the table must steer
// some class or it would receive nothing. An ND packet may carry any DSCP
// value and the NIC's choice between overlapping rules is undefined, so ND
// and the steered DSCP values must target the same queues.
static int
validate_device_steering(const struct dataplane_device_config *device) {
	if (!dataplane_device_config_has_roles(device)) {
		return 0;
	}
	if (is_virtual_port(device->port_name)) {
		fprintf(stderr,
			"device %s: virtio_user and ring ports have no "
			"queue roles\n",
			device->device_name);
		return -1;
	}
	if (!device->queues[0].role.rss) {
		fprintf(stderr,
			"device %s: queue 0 must stay in the RSS table\n",
			device->device_name);
		return -1;
	}

	uint64_t dscp = 0;
	int nd = 0;
	for (uint32_t idx = 0; idx < device->queue_count; ++idx) {
		const struct dataplane_queue_role *role =
			&device->queues[idx].role;
		if (!role->rss && role->dscp == 0 && !role->arp && !role->nd &&
		    !role->l2_control) {
			fprintf(stderr,
				"device %s: queue %u is out of the RSS table "
				"but steers no class\n",
				device->device_name,
				idx);
			return -1;
		}
		dscp |= role->dscp;
		nd |= role->nd;
	}

	// With ND steered, each queue lists either ND and every steered DSCP
	// value, or neither.
	for (uint32_t idx = 0; nd && idx < device->queue_count; ++idx) {
		const struct dataplane_queue_role *role =
			&device->queues[idx].role;
		if (role->dscp != (role->nd ? dscp : 0)) {
			fprintf(stderr,
				"device %s: queue %u: nd and the steered dscp "
				"values can match one packet and must go to "
				"the same queues\n",
				device->device_name,
				idx);
			return -1;
		}
	}
	return 0;
}

// Rejects a device declaring its queues through more than one YAML shape,
// an empty queues list, and the two worker shapes mixed in one config.
static int
check_queue_declarations(const struct dataplane_config *config) {
	for (uint64_t dev_idx = 0; dev_idx < config->device_count; ++dev_idx) {
		const struct dataplane_device_config *device =
			config->devices + dev_idx;
		uint8_t declarations = device->queue_declarations;
		if (declarations & (declarations - 1)) {
			fprintf(stderr,
				"device %s: declare queues through exactly one "
				"of queue_count, queues and workers\n",
				device->device_name);
			return -1;
		}
		if (declarations == dataplane_queue_list &&
		    device->queue_count == 0) {
			fprintf(stderr,
				"device %s: the queues list is empty\n",
				device->device_name);
			return -1;
		}
	}

	if (config->workers == NULL) {
		return 0;
	}
	for (uint64_t dev_idx = 0; dev_idx < config->device_count; ++dev_idx) {
		const struct dataplane_device_config *device =
			config->devices + dev_idx;
		if (device->queue_declarations & dataplane_queue_nested) {
			fprintf(stderr,
				"workers are declared both at the top level "
				"and "
				"inside device %s\n",
				device->device_name);
			return -1;
		}
	}
	return 0;
}

// Expand the nested devices[].workers[] declarations into the device's
// queues and the flat top-level worker list.
//
// Worker i declared inside device d becomes a queue — carrying the entry's
// sizing and role — polled by a flat worker with a single assignment to
// queue i of d. The queue ids are therefore the worker indices inside the
// device, exactly the numbering the device init used to apply implicitly.
static int
expand_device_workers(struct dataplane_config *config) {
	uint64_t nested_count = 0;
	for (uint64_t dev_idx = 0; dev_idx < config->device_count; ++dev_idx) {
		nested_count += config->devices[dev_idx].worker_count;
	}

	if (nested_count == 0) {
		return 0;
	}

	struct dataplane_worker_config *workers =
		(struct dataplane_worker_config *)realloc(
			config->workers,
			sizeof(struct dataplane_worker_config) *
				(config->worker_count + nested_count)
		);
	if (workers == NULL) {
		return -1;
	}
	config->workers = workers;

	uint64_t wrk_idx = config->worker_count;
	for (uint64_t dev_idx = 0; dev_idx < config->device_count; ++dev_idx) {
		struct dataplane_device_config *device =
			config->devices + dev_idx;
		if (device->worker_count == 0) {
			continue;
		}

		device->queues = (struct dataplane_queue_config *)calloc(
			device->worker_count,
			sizeof(struct dataplane_queue_config)
		);
		if (device->queues == NULL) {
			config->worker_count = wrk_idx;
			return -1;
		}
		device->queue_count = device->worker_count;

		for (uint32_t idx = 0; idx < device->worker_count; ++idx) {
			struct dataplane_device_worker_config *source =
				device->workers + idx;
			struct dataplane_queue_config *queue =
				device->queues + idx;
			queue->rx_queue_len = source->rx_queue_len;
			queue->tx_queue_len = source->tx_queue_len;
			queue->num_mbufs = source->num_mbufs;
			queue->role = source->role;

			struct dataplane_worker_config *worker =
				workers + wrk_idx;
			worker->assignments =
				(struct dataplane_assignment_config *)calloc(
					1,
					sizeof(struct
					       dataplane_assignment_config)
				);
			if (worker->assignments == NULL) {
				config->worker_count = wrk_idx;
				return -1;
			}

			worker->core_id = source->core_id;
			worker->instance_id = source->instance_id;
			worker->assignment_count = 1;
			worker->assignments->device_id = (uint32_t)dev_idx;
			worker->assignments->queue_id = idx;

			++wrk_idx;
		}
	}

	config->worker_count = wrk_idx;

	return 0;
}

// The sizing a queue takes when neither the queue nor its device declares
// one.
#define QUEUE_LEN_DEFAULT 4096
#define QUEUE_NUM_MBUFS_DEFAULT 16384

// Takes the sizing a zero field of queue falls back to.
static void
queue_sizing_default(
	const struct dataplane_queue_config *device_default,
	struct dataplane_queue_config *fallback
) {
	fallback->role = device_default->role;
	fallback->rx_queue_len = device_default->rx_queue_len != 0
					 ? device_default->rx_queue_len
					 : QUEUE_LEN_DEFAULT;
	fallback->tx_queue_len = device_default->tx_queue_len != 0
					 ? device_default->tx_queue_len
					 : QUEUE_LEN_DEFAULT;
	fallback->num_mbufs = device_default->num_mbufs != 0
				      ? device_default->num_mbufs
				      : QUEUE_NUM_MBUFS_DEFAULT;
}

// Materializes every queue of every device: the queue_count form becomes
// queue_count entries of the device defaults, and a zero sizing field of
// any queue takes the device default, which itself falls back to the
// built-in numbers. Afterwards every queue carries its effective sizing.
static int
materialize_queues(struct dataplane_config *config) {
	for (uint64_t dev_idx = 0; dev_idx < config->device_count; ++dev_idx) {
		struct dataplane_device_config *device =
			config->devices + dev_idx;

		struct dataplane_queue_config fallback;
		queue_sizing_default(&device->default_queue, &fallback);

		if (device->queue_declarations == dataplane_queue_count &&
		    device->queues == NULL) {
			device->queues = (struct dataplane_queue_config *)
				calloc(device->queue_count,
				       sizeof(struct dataplane_queue_config));
			if (device->queues == NULL) {
				return -1;
			}
			for (uint32_t idx = 0; idx < device->queue_count;
			     ++idx) {
				device->queues[idx] = fallback;
			}
		}

		for (uint32_t idx = 0; idx < device->queue_count; ++idx) {
			struct dataplane_queue_config *queue =
				device->queues + idx;
			if (queue->rx_queue_len == 0) {
				queue->rx_queue_len = fallback.rx_queue_len;
			}
			if (queue->tx_queue_len == 0) {
				queue->tx_queue_len = fallback.tx_queue_len;
			}
			if (queue->num_mbufs == 0) {
				queue->num_mbufs = fallback.num_mbufs;
			}
		}
	}
	return 0;
}

// Resolves the top-level workers' assignment device names to device
// indices and range-checks their queue ids.
static int
resolve_assignments(struct dataplane_config *config) {
	for (uint64_t wrk_idx = 0; wrk_idx < config->worker_count; ++wrk_idx) {
		struct dataplane_worker_config *worker =
			config->workers + wrk_idx;

		for (uint32_t asgn_idx = 0; asgn_idx < worker->assignment_count;
		     ++asgn_idx) {
			struct dataplane_assignment_config *assignment =
				worker->assignments + asgn_idx;
			if (assignment->device[0] == '\0') {
				// A nested-form assignment, resolved at
				// expansion.
				continue;
			}

			int64_t dev_id =
				lookup_device(config, assignment->device);
			if (dev_id == -1) {
				fprintf(stderr,
					"worker core=%u: assignment of queue "
					"%u names unknown device '%s'\n",
					worker->core_id,
					assignment->queue_id,
					assignment->device);
				return -1;
			}
			if (dev_id == -2) {
				fprintf(stderr,
					"worker core=%u: assignment of queue "
					"%u names device '%s', which is "
					"declared more than once\n",
					worker->core_id,
					assignment->queue_id,
					assignment->device);
				return -1;
			}

			const struct dataplane_device_config *device =
				config->devices + dev_id;
			if (assignment->queue_id >= device->queue_count) {
				fprintf(stderr,
					"worker core=%u: device %s declares %u "
					"queues, cannot assign queue %u\n",
					worker->core_id,
					device->device_name,
					device->queue_count,
					assignment->queue_id);
				return -1;
			}

			assignment->device_id = (uint32_t)dev_id;
		}
	}
	return 0;
}

// Offset of a device's queues in the flat queue-owner table below.
static uint64_t
device_queue_base(const struct dataplane_config *config, uint64_t device_id) {
	uint64_t base = 0;
	for (uint64_t dev_idx = 0; dev_idx < device_id; ++dev_idx) {
		base += config->devices[dev_idx].queue_count;
	}
	return base;
}

// Rejects a declared queue no worker polls, a queue polled by two
// workers, a worker with no assignment, and a ring device — a
// single-producer/single-consumer ring pair — with more than one queue.
static int
validate_assignments(struct dataplane_config *config) {
	for (uint64_t dev_idx = 0; dev_idx < config->device_count; ++dev_idx) {
		const struct dataplane_device_config *device =
			config->devices + dev_idx;
		if (!strncmp(
			    device->port_name, "net_ring_", strlen("net_ring_")
		    ) &&
		    device->queue_count != 1) {
			fprintf(stderr,
				"ring device %s requires exactly one queue, "
				"got %u\n",
				device->port_name,
				device->queue_count);
			return -1;
		}
	}

	uint64_t queue_total = 0;
	for (uint64_t dev_idx = 0; dev_idx < config->device_count; ++dev_idx) {
		queue_total += config->devices[dev_idx].queue_count;
	}

	// The queue owner table: one slot per declared queue, in device and
	// queue order, holding the index of the worker assigned to it or -1.
	int64_t *owner = (int64_t *)malloc(sizeof(int64_t) * queue_total);
	if (owner == NULL && queue_total > 0) {
		return -1;
	}
	for (uint64_t idx = 0; idx < queue_total; ++idx) {
		owner[idx] = -1;
	}

	for (uint64_t wrk_idx = 0; wrk_idx < config->worker_count; ++wrk_idx) {
		struct dataplane_worker_config *worker =
			config->workers + wrk_idx;

		if (worker->assignment_count == 0) {
			fprintf(stderr,
				"worker core=%u has no assignments\n",
				worker->core_id);
			goto error;
		}

		// The pipe wiring treats a worker polling any steering queue
		// as a steering worker as a whole, so ordinary queues beside
		// a steering one would leak into the steering path. A
		// steering queue needs a dedicated worker.
		int has_steering = 0;
		int has_rx = 0;
		const struct dataplane_device_config *steering_device = NULL;
		uint32_t steering_queue_id = 0;

		for (uint32_t asgn_idx = 0; asgn_idx < worker->assignment_count;
		     ++asgn_idx) {
			struct dataplane_assignment_config *assignment =
				worker->assignments + asgn_idx;
			const struct dataplane_device_config *device =
				config->devices + assignment->device_id;

			if (device->queues[assignment->queue_id].role.rss) {
				has_rx = 1;
			} else {
				if (!has_steering) {
					steering_device = device;
					steering_queue_id =
						assignment->queue_id;
				}
				has_steering = 1;
			}

			if (has_steering && has_rx) {
				fprintf(stderr,
					"worker core=%u: steering queue %u of "
					"device %s needs a dedicated worker\n",
					worker->core_id,
					steering_queue_id,
					steering_device->device_name);
				goto error;
			}

			uint64_t slot = device_queue_base(
						config, assignment->device_id
					) +
					assignment->queue_id;
			if (owner[slot] != -1) {
				fprintf(stderr,
					"device %s queue %u is assigned to "
					"more "
					"than one worker\n",
					config->devices[assignment->device_id]
						.device_name,
					assignment->queue_id);
				goto error;
			}
			owner[slot] = (int64_t)wrk_idx;
		}
	}

	for (uint64_t dev_idx = 0; dev_idx < config->device_count; ++dev_idx) {
		const struct dataplane_device_config *device =
			config->devices + dev_idx;
		uint64_t base = device_queue_base(config, dev_idx);
		for (uint32_t idx = 0; idx < device->queue_count; ++idx) {
			if (owner[base + idx] == -1) {
				fprintf(stderr,
					"device %s queue %u is not assigned to "
					"any worker\n",
					device->device_name,
					idx);
				goto error;
			}
		}
	}

	free(owner);
	return 0;

error:
	free(owner);
	return -1;
}

// Rejects queue roles the devices cannot carry.
static int
validate_steering(const struct dataplane_config *config) {
	for (uint64_t dev_idx = 0; dev_idx < config->device_count; ++dev_idx) {
		if (validate_device_steering(config->devices + dev_idx) != 0) {
			return -1;
		}
	}
	return 0;
}

enum state {
	state_empty,
	state_dataplane,
	state_dataplane_storage,
	state_dataplane_dpdk_memory,
	state_dataplane_packet_recirc_limit,
	state_dataplane_iova_mode,

	state_instances,
	state_instance,
	state_instance_numa_id,
	state_instance_dp_memory,
	state_instance_cp_memory,

	state_devices,
	state_device,
	state_device_name,
	state_device_port_name,
	state_device_mac_addr,
	state_device_mtu,
	state_device_max_lro_packet_size,
	state_device_rss_hash,

	state_device_queue_count,
	state_device_queues,
	state_queue,
	// The sizing value states serve both a queues[] entry and the
	// device's default_queue, through the queue cursor.
	state_queue_rx_queue_len,
	state_queue_tx_queue_len,
	state_queue_num_mbufs,
	state_default_queue,

	// The queue's role, shared by a queues[] entry and a nested worker
	// entry through the role cursor.
	state_role_rss,
	state_steer,
	state_steer_dscp,
	state_steer_flag,

	state_device_workers,
	state_device_worker,
	state_device_worker_core_id,
	state_device_worker_instance_id,
	state_device_worker_rx_queue_len,
	state_device_worker_tx_queue_len,
	state_device_worker_num_mbufs,

	state_workers,
	state_worker,
	state_worker_core_id,
	state_worker_instance_id,
	state_worker_assignments,
	state_assignment,
	state_assignment_device,
	state_assignment_queue,

	state_connections,
	state_connection,
	state_connection_src,
	state_connection_dst,

	state_loglevel,

	state_plugin_dir,
	state_modules,
	state_objects,
};

int
dataplane_config_init(FILE *file, struct dataplane_config **config) {
	enum state state = state_empty;

	yaml_parser_t parser;
	yaml_event_t event;
	int event_live = 0;
	if (!yaml_parser_initialize(&parser)) {
		return -1;
	}

	yaml_parser_set_input_file(&parser, file);

	struct dataplane_config *dataplane =
		(struct dataplane_config *)malloc(sizeof(struct dataplane_config
		));
	if (dataplane == NULL) {
		goto err_alloc_config;
	}

	memset(dataplane, 0, sizeof(*dataplane));
	dataplane->packet_recirc_limit = PACKET_RECIRC_LIMIT_DEFAULT;

	struct dataplane_instance_config *instance = NULL;
	struct dataplane_device_config *device = NULL;
	struct dataplane_device_worker_config *device_worker = NULL;
	// The queue being filled in: a queues[] entry or the device's
	// default_queue. The state to return to after a sizing value, since
	// the two are dispatched from different mapping states.
	struct dataplane_queue_config *queue = NULL;
	enum state queue_return_state = state_empty;
	// The queue whose role a rss or steer key describes: a queues[] entry
	// or a nested worker entry's implied queue, and where to return once
	// the role is complete.
	struct dataplane_queue_role *role = NULL;
	enum state role_return_state = state_empty;
	// Whether the steered DSCP values come as a list rather than a single
	// value.
	int dscp_list = 0;
	// The steered class flag a boolean scalar sets, and its field name.
	uint8_t *steer_flag = NULL;
	const char *steer_flag_name = NULL;
	struct dataplane_worker_config *worker = NULL;
	struct dataplane_assignment_config *assignment = NULL;
	struct dataplane_connection_config *connection = NULL;

	char *start = NULL;
	size_t scalar_length = 0;
	uint64_t value = 0;

	if (!yaml_parser_parse(&parser, &event)) {
		goto error;
	}
	event_live = 1;
	while (event.type != YAML_STREAM_END_EVENT) {

		switch (event.type) {
		case YAML_NO_EVENT:
			break;
		case YAML_STREAM_START_EVENT:
			break;
		case YAML_STREAM_END_EVENT:
			break;
		case YAML_DOCUMENT_START_EVENT:
			break;
		case YAML_DOCUMENT_END_EVENT:
			break;

		case YAML_ALIAS_EVENT:
			break;

		case YAML_SCALAR_EVENT:
			start = (char *)event.data.scalar.value;
			scalar_length = event.data.scalar.length;

			switch (state) {
			case state_dataplane_storage:
				strtcpy(dataplane->storage,
					start,
					sizeof(dataplane->storage));
				state = state_dataplane;
				break;
			case state_dataplane_dpdk_memory:
				if (parse_unsigned(
					    "dataplane.dpdk_memory",
					    start,
					    scalar_length,
					    0,
					    UINT64_MAX,
					    &dataplane->dpdk_memory
				    ) != 0) {
					goto error;
				}
				state = state_dataplane;
				break;
			case state_dataplane_packet_recirc_limit:
				if (parse_unsigned(
					    "dataplane.packet_recirc_limit",
					    start,
					    scalar_length,
					    PACKET_RECIRC_LIMIT_MIN,
					    PACKET_RECIRC_LIMIT_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				dataplane->packet_recirc_limit =
					(uint16_t)value;
				state = state_dataplane;
				break;
			case state_dataplane_iova_mode:
				strtcpy(dataplane->iova_mode,
					start,
					sizeof(dataplane->iova_mode));
				state = state_dataplane;
				break;
			case state_loglevel:
				strtcpy(dataplane->loglevel,
					start,
					sizeof(dataplane->loglevel));
				state = state_dataplane;
				break;
			case state_plugin_dir:
				strtcpy(dataplane->plugin_dir,
					start,
					sizeof(dataplane->plugin_dir));
				state = state_dataplane;
				break;
			case state_modules:
				if (dataplane->module_count >=
				    DATAPLANE_MAX_MODULES) {
					goto error;
				}
				strtcpy(dataplane->module_names
						[dataplane->module_count],
					start,
					DATAPLANE_MODULE_NAME_LEN);
				dataplane->module_count++;
				break;
			case state_objects:
				if (dataplane->object_count >=
				    DATAPLANE_MAX_OBJECTS) {
					goto error;
				}
				if (scalar_length == 0 ||
				    scalar_length >=
					    DATAPLANE_OBJECT_NAME_LEN) {
					goto error;
				}
				strtcpy(dataplane->object_names
						[dataplane->object_count],
					start,
					DATAPLANE_OBJECT_NAME_LEN);
				dataplane->object_count++;
				break;

			// handle new instance
			case state_instance_numa_id:
				if (parse_unsigned(
					    "instances.numa_id",
					    start,
					    scalar_length,
					    0,
					    UINT16_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				instance->numa_idx = (uint16_t)value;
				state = state_instance;
				break;
			case state_instance_dp_memory:
				if (parse_memory_size(
					    "instances.dp_memory",
					    start,
					    scalar_length,
					    &instance->dp_memory
				    ) != 0) {
					goto error;
				}
				state = state_instance;
				break;
			case state_instance_cp_memory:
				if (parse_memory_size(
					    "instances.cp_memory",
					    start,
					    scalar_length,
					    &instance->cp_memory
				    ) != 0) {
					goto error;
				}
				state = state_instance;
				break;
			case state_device_name:
				strtcpy(device->device_name,
					start,
					sizeof(device->device_name));
				state = state_device;
				break;

			case state_device_port_name:
				strtcpy(device->port_name,
					start,
					sizeof(device->port_name));
				state = state_device;
				break;
			case state_device_mac_addr:
				strtcpy(device->mac_addr,
					start,
					sizeof(device->mac_addr));
				state = state_device;
				break;
			case state_device_mtu:
				if (parse_unsigned(
					    "devices.mtu",
					    start,
					    scalar_length,
					    0,
					    UINT32_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				device->mtu = (uint32_t)value;
				state = state_device;
				break;
			case state_device_max_lro_packet_size:
				if (parse_unsigned(
					    "devices.max_lro_packet_size",
					    start,
					    scalar_length,
					    0,
					    UINT64_MAX,
					    &device->max_lro_packet_size
				    ) != 0) {
					goto error;
				}

				state = state_device;
				break;
			case state_device_rss_hash:
				if (parse_unsigned(
					    "devices.rss_hash",
					    start,
					    scalar_length,
					    0,
					    UINT64_MAX,
					    &device->rss_hash
				    ) != 0) {
					goto error;
				}

				state = state_device;
				break;

			case state_device_queue_count:
				if (parse_unsigned(
					    "devices.queue_count",
					    start,
					    scalar_length,
					    1,
					    UINT16_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				device->queue_count = (uint32_t)value;
				state = state_device;
				break;

			case state_queue_rx_queue_len:
				if (parse_unsigned(
					    "queues.rx_queue_len",
					    start,
					    scalar_length,
					    0,
					    UINT16_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				queue->rx_queue_len = (uint16_t)value;
				state = queue_return_state;
				break;
			case state_queue_tx_queue_len:
				if (parse_unsigned(
					    "queues.tx_queue_len",
					    start,
					    scalar_length,
					    0,
					    UINT16_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				queue->tx_queue_len = (uint16_t)value;
				state = queue_return_state;
				break;
			case state_queue_num_mbufs:
				if (parse_unsigned(
					    "queues.num_mbufs",
					    start,
					    scalar_length,
					    0,
					    UINT32_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				queue->num_mbufs = (uint32_t)value;
				state = queue_return_state;
				break;

			case state_role_rss:
				if (parse_bool(
					    "queues.rss",
					    start,
					    scalar_length,
					    &role->rss
				    ) != 0) {
					goto error;
				}
				state = role_return_state;
				break;
			case state_steer_dscp:
				// DSCP 0 is all unmarked traffic, so it is
				// never steered.
				if (parse_unsigned(
					    "queues.steer.dscp",
					    start,
					    scalar_length,
					    1,
					    63,
					    &value
				    ) != 0) {
					goto error;
				}
				role->dscp |= UINT64_C(1) << value;
				if (!dscp_list) {
					state = state_steer;
				}
				break;
			case state_steer_flag:
				if (parse_bool(
					    steer_flag_name,
					    start,
					    scalar_length,
					    steer_flag
				    ) != 0) {
					goto error;
				}
				state = state_steer;
				break;

			case state_device_worker_core_id:
				if (parse_unsigned(
					    "devices.workers.core_id",
					    start,
					    scalar_length,
					    0,
					    UINT16_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				device_worker->core_id = (uint16_t)value;

				state = state_device_worker;
				break;
			case state_device_worker_instance_id:
				if (parse_unsigned(
					    "devices.workers.instance_id",
					    start,
					    scalar_length,
					    0,
					    UINT16_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				device_worker->instance_id = (uint16_t)value;

				state = state_device_worker;
				break;
			case state_device_worker_rx_queue_len:
				if (parse_unsigned(
					    "devices.workers.rx_queue_len",
					    start,
					    scalar_length,
					    0,
					    UINT16_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				device_worker->rx_queue_len = (uint16_t)value;

				state = state_device_worker;
				break;
			case state_device_worker_tx_queue_len:
				if (parse_unsigned(
					    "devices.workers.tx_queue_len",
					    start,
					    scalar_length,
					    0,
					    UINT16_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				device_worker->tx_queue_len = (uint16_t)value;

				state = state_device_worker;
				break;
			case state_device_worker_num_mbufs:
				if (parse_unsigned(
					    "devices.workers.num_mbufs",
					    start,
					    scalar_length,
					    0,
					    UINT32_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				device_worker->num_mbufs = (uint32_t)value;

				state = state_device_worker;
				break;

			case state_worker_core_id:
				if (parse_unsigned(
					    "workers.core_id",
					    start,
					    scalar_length,
					    0,
					    UINT16_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				worker->core_id = (uint16_t)value;

				state = state_worker;
				break;
			case state_worker_instance_id:
				if (parse_unsigned(
					    "workers.instance_id",
					    start,
					    scalar_length,
					    0,
					    UINT16_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				worker->instance_id = (uint16_t)value;

				state = state_worker;
				break;

			case state_assignment_device:
				strtcpy(assignment->device,
					start,
					sizeof(assignment->device));
				state = state_assignment;
				break;
			case state_assignment_queue:
				if (parse_unsigned(
					    "workers.assignments.queue",
					    start,
					    scalar_length,
					    0,
					    UINT32_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				assignment->queue_id = (uint32_t)value;

				state = state_assignment;
				break;

			case state_connection_src:
				strtcpy(connection->src_device,
					start,
					sizeof(connection->src_device));
				state = state_connection;
				break;
			case state_connection_dst:
				strtcpy(connection->dst_device,
					start,
					sizeof(connection->dst_device));
				state = state_connection;
				break;

			case state_empty:
				if (!strcmp("dataplane", start)) {
					state = state_dataplane;
				} else {
					goto error;
				}
				break;
			case state_dataplane:
				if (!strcmp("storage", start)) {
					state = state_dataplane_storage;
				} else if (!strcmp("dpdk_memory", start)) {
					state = state_dataplane_dpdk_memory;
				} else if (!strcmp("packet_recirc_limit",
						   start)) {
					state = state_dataplane_packet_recirc_limit;
				} else if (!strcmp("iova_mode", start)) {
					state = state_dataplane_iova_mode;
				} else if (!strcmp("instances", start)) {
					state = state_instances;
				} else if (!strcmp("devices", start)) {
					state = state_devices;
				} else if (!strcmp("workers", start)) {
					state = state_workers;
				} else if (!strcmp("connections", start)) {
					state = state_connections;
				} else if (!strcmp("loglevel", start)) {
					state = state_loglevel;
				} else if (!strcmp("plugin_dir", start)) {
					state = state_plugin_dir;
				} else if (!strcmp("modules", start)) {
					state = state_modules;
				} else if (!strcmp("objects", start)) {
					state = state_objects;
				} else {
					goto error;
				}

				break;
			case state_instance:
				if (!strcmp("numa_id", start)) {
					state = state_instance_numa_id;
				} else if (!strcmp("dp_memory", start)) {
					state = state_instance_dp_memory;
				} else if (!strcmp("cp_memory", start)) {
					state = state_instance_cp_memory;
				} else {
					goto error;
				}

				break;
			case state_device:
				if (!strcmp("device_name", start)) {
					state = state_device_name;
				} else if (!strcmp("port_name", start)) {
					state = state_device_port_name;
				} else if (!strcmp("mac_addr", start)) {
					state = state_device_mac_addr;
				} else if (!strcmp("mtu", start)) {
					state = state_device_mtu;
				} else if (!strcmp("max_lro_packet_size",
						   start)) {
					state = state_device_max_lro_packet_size;
				} else if (!strcmp("rss_hash", start)) {
					state = state_device_rss_hash;
				} else if (!strcmp("queue_count", start)) {
					device->queue_declarations |=
						dataplane_queue_count;
					state = state_device_queue_count;
				} else if (!strcmp("queues", start)) {
					state = state_device_queues;
				} else if (!strcmp("default_queue", start)) {
					state = state_default_queue;
				} else if (!strcmp("workers", start)) {
					state = state_device_workers;
				} else {
					goto error;
				}

				break;
			// A queues[] entry: the sizing keys are shared with
			// default_queue, the role keys with a nested worker
			// entry.
			case state_queue:
				queue_return_state = state_queue;
				if (!strcmp("rx_queue_len", start)) {
					state = state_queue_rx_queue_len;
				} else if (!strcmp("tx_queue_len", start)) {
					state = state_queue_tx_queue_len;
				} else if (!strcmp("num_mbufs", start)) {
					state = state_queue_num_mbufs;
				} else if (!strcmp("rss", start)) {
					role = &queue->role;
					role_return_state = state_queue;
					state = state_role_rss;
				} else if (!strcmp("steer", start)) {
					role = &queue->role;
					role_return_state = state_queue;
					state = state_steer;
				} else {
					goto error;
				}

				break;
			// The device-wide queue sizing defaults; a role
			// belongs to a single queue, not to every one of them.
			case state_default_queue:
				queue_return_state = state_default_queue;
				if (!strcmp("rx_queue_len", start)) {
					state = state_queue_rx_queue_len;
				} else if (!strcmp("tx_queue_len", start)) {
					state = state_queue_tx_queue_len;
				} else if (!strcmp("num_mbufs", start)) {
					state = state_queue_num_mbufs;
				} else {
					goto error;
				}

				break;
			case state_device_worker:
				if (!strcmp("core_id", start)) {
					state = state_device_worker_core_id;
				} else if (!strcmp("instance_id", start)) {
					state = state_device_worker_instance_id;
				} else if (!strcmp("rx_queue_len", start)) {
					state = state_device_worker_rx_queue_len;
				} else if (!strcmp("tx_queue_len", start)) {
					state = state_device_worker_tx_queue_len;
				} else if (!strcmp("num_mbufs", start)) {
					state = state_device_worker_num_mbufs;
				} else if (!strcmp("rss", start)) {
					role = &device_worker->role;
					role_return_state = state_device_worker;
					state = state_role_rss;
				} else if (!strcmp("steer", start)) {
					role = &device_worker->role;
					role_return_state = state_device_worker;
					state = state_steer;
				} else {
					goto error;
				}

				break;
			case state_steer:
				if (!strcmp("dscp", start)) {
					state = state_steer_dscp;
				} else if (!strcmp("arp", start)) {
					steer_flag = &role->arp;
					steer_flag_name = "queues.steer.arp";
					state = state_steer_flag;
				} else if (!strcmp("nd", start)) {
					steer_flag = &role->nd;
					steer_flag_name = "queues.steer.nd";
					state = state_steer_flag;
				} else if (!strcmp("l2_control", start)) {
					steer_flag = &role->l2_control;
					steer_flag_name =
						"queues.steer.l2_control";
					state = state_steer_flag;
				} else {
					goto error;
				}
				break;
			case state_worker:
				if (!strcmp("core_id", start)) {
					state = state_worker_core_id;
				} else if (!strcmp("instance_id", start)) {
					state = state_worker_instance_id;
				} else if (!strcmp("assignments", start)) {
					state = state_worker_assignments;
				} else {
					goto error;
				}

				break;
			case state_assignment: {
				if (!strcmp("device", start)) {
					state = state_assignment_device;
				} else if (!strcmp("queue", start)) {
					state = state_assignment_queue;
				} else {
					goto error;
				}
				break;
			}
			case state_connection: {
				if (!strcmp("src_device", start)) {
					state = state_connection_src;
				} else if (!strcmp("dst_device", start)) {
					state = state_connection_dst;
				} else {
					goto error;
				}
				break;
			}

			default:
				goto error;
			}

			break;

		case YAML_SEQUENCE_START_EVENT:
			switch (state) {
			case state_instances:
				break;
			case state_devices:
				break;
			case state_device_queues:
				device->queue_declarations |=
					dataplane_queue_list;
				break;
			case state_device_workers:
				break;
			case state_steer_dscp:
				dscp_list = 1;
				break;
			case state_connections:
				break;
			case state_workers:
				break;
			case state_worker_assignments:
				break;
			case state_modules:
				break;
			case state_objects:
				break;
			default:
				goto error;
			}
			break;
		case YAML_SEQUENCE_END_EVENT:
			switch (state) {
			case state_instances:
				state = state_dataplane;
				break;
			case state_devices:
				state = state_dataplane;
				break;
			case state_device_queues:
				state = state_device;
				break;
			case state_device_workers:
				state = state_device;
				break;
			case state_steer_dscp:
				dscp_list = 0;
				state = state_steer;
				break;
			case state_connections:
				state = state_dataplane;
				break;
			case state_workers:
				state = state_dataplane;
				break;
			case state_worker_assignments:
				state = state_worker;
				break;
			case state_modules:
				state = state_dataplane;
				break;
			case state_objects:
				state = state_dataplane;
				break;
			default:
				goto error;
			}
			break;

		case YAML_MAPPING_START_EVENT:
			switch (state) {
			case state_empty:
				break;
			case state_dataplane:
				break;
			case state_instances: {
				++dataplane->instance_count;

				void *mem = realloc(
					dataplane->instances,
					sizeof(struct dataplane_instance_config
					) * dataplane->instance_count
				);
				if (mem == NULL) {
					goto error;
				}
				dataplane->instances =
					(struct dataplane_instance_config *)mem;

				instance = dataplane->instances +
					   dataplane->instance_count - 1;
				memset(instance, 0, sizeof(*instance));
				state = state_instance;
				break;
			}
			case state_devices: {
				void *mem = realloc(
					dataplane->devices,
					sizeof(struct dataplane_device_config) *
						(dataplane->device_count + 1)
				);
				if (mem == NULL) {
					goto error;
				}
				dataplane->devices =
					(struct dataplane_device_config *)mem;
				device = dataplane->devices +
					 dataplane->device_count;
				memset(device, 0, sizeof(*device));
				device->default_queue.role.rss = 1;
				++dataplane->device_count;
				state = state_device;
				break;
			}
			case state_device_queues: {
				// A port addresses its queues in 16 bits; the
				// queue_count form range-checks the same bound.
				if (device->queue_count >= UINT16_MAX) {
					fprintf(stderr,
						"device %s: a queues list "
						"declares at most %u queues\n",
						device->port_name,
						UINT16_MAX);
					goto error;
				}
				void *mem = realloc(
					device->queues,
					sizeof(struct dataplane_queue_config) *
						(device->queue_count + 1)
				);
				if (mem == NULL) {
					goto error;
				}
				device->queues =
					(struct dataplane_queue_config *)mem;
				queue = device->queues + device->queue_count;
				memset(queue, 0, sizeof(*queue));
				queue->role.rss = 1;
				++device->queue_count;

				state = state_queue;
				break;
			}
			case state_default_queue:
				// The defaults live inside the device; the
				// mapping only selects the cursor.
				queue = &device->default_queue;
				break;
			case state_device_workers: {
				// The entries become the device's queues, so
				// the list obeys the same 16-bit port width
				// the queue_count and queues forms check.
				if (device->worker_count >= UINT16_MAX) {
					fprintf(stderr,
						"device %s: a workers list "
						"declares at most %u queues\n",
						device->port_name,
						UINT16_MAX);
					goto error;
				}
				device->worker_count++;
				device->queue_declarations |=
					dataplane_queue_nested;
				void *mem = realloc(
					device->workers,
					sizeof(struct
					       dataplane_device_worker_config
					) * device->worker_count
				);
				if (mem == NULL) {
					goto error;
				}
				device->workers =
					(struct dataplane_device_worker_config
						 *)mem;
				device_worker = device->workers +
						device->worker_count - 1;
				memset(device_worker, 0, sizeof(*device_worker)
				);
				device_worker->role.rss = 1;

				state = state_device_worker;
				break;
			}
			case state_workers: {
				// The count follows the allocation: the error
				// path frees per worker up to the count, past
				// which it must not read.
				void *mem = realloc(
					dataplane->workers,
					sizeof(struct dataplane_worker_config) *
						(dataplane->worker_count + 1)
				);
				if (mem == NULL) {
					goto error;
				}
				dataplane->workers =
					(struct dataplane_worker_config *)mem;
				worker = dataplane->workers +
					 dataplane->worker_count;
				memset(worker, 0, sizeof(*worker));
				++dataplane->worker_count;

				state = state_worker;
				break;
			}
			case state_worker_assignments: {
				void *mem = realloc(
					worker->assignments,
					sizeof(struct
					       dataplane_assignment_config
					) * (worker->assignment_count + 1)
				);
				if (mem == NULL) {
					goto error;
				}
				worker->assignments =
					(struct dataplane_assignment_config *)
						mem;
				assignment = worker->assignments +
					     worker->assignment_count;
				memset(assignment, 0, sizeof(*assignment));
				++worker->assignment_count;

				state = state_assignment;
				break;
			}
			case state_steer:
				break;
			case state_connections: {
				dataplane->connection_count++;
				void *mem = realloc(
					dataplane->connections,
					sizeof(struct
					       dataplane_connection_config
					) * dataplane->connection_count
				);
				if (mem == NULL) {
					goto error;
				}
				dataplane->connections =
					(struct dataplane_connection_config *)
						mem;
				connection = dataplane->connections +
					     dataplane->connection_count - 1;
				memset(connection, 0, sizeof(*connection));

				state = state_connection;
				break;
			}
			default:
				goto error;
			}
			break;
		case YAML_MAPPING_END_EVENT:
			switch (state) {
			case state_empty:
				break;
			case state_dataplane:
				state = state_empty;
				break;
			case state_instance:
				state = state_instances;
				break;
			case state_device:
				// Default device_name to port_name when the
				// YAML did not provide an explicit device_name.
				if (device->device_name[0] == '\0') {
					strtcpy(device->device_name,
						device->port_name,
						sizeof(device->device_name));
				}
				state = state_devices;
				break;
			case state_queue:
				state = state_device_queues;
				break;
			case state_default_queue:
				state = state_device;
				break;
			case state_device_worker:
				state = state_device_workers;
				break;
			case state_steer:
				state = role_return_state;
				break;
			case state_worker:
				state = state_workers;
				break;
			case state_assignment:
				// The device cannot default: a nameless
				// assignment would silently bind device 0.
				if (assignment->device[0] == '\0') {
					goto error;
				}
				state = state_worker_assignments;
				break;
			case state_connection:
				state = state_connections;
				break;
			default:
				goto error;
			}
			break;

		default:
			break;
		}

		yaml_event_delete(&event);
		event_live = 0;
		if (!yaml_parser_parse(&parser, &event)) {
			goto error;
		}
		event_live = 1;
	}
	yaml_event_delete(&event);
	event_live = 0;

	if (resolve_connections(dataplane) != 0) {
		goto error;
	}

	if (check_queue_declarations(dataplane) != 0) {
		goto error;
	}

	if (expand_device_workers(dataplane) != 0) {
		goto error;
	}

	if (materialize_queues(dataplane) != 0) {
		goto error;
	}

	if (resolve_assignments(dataplane) != 0) {
		goto error;
	}

	if (validate_assignments(dataplane) != 0) {
		goto error;
	}

	if (validate_steering(dataplane) != 0) {
		goto error;
	}

	yaml_parser_delete(&parser);

	*config = dataplane;

	return 0;

error:
	if (event_live) {
		yaml_event_delete(&event);
	}
	dataplane_config_free(dataplane);

err_alloc_config:
	yaml_parser_delete(&parser);
	return -1;
}

void
dataplane_config_free(struct dataplane_config *config) {
	free(config->instances);

	for (uint64_t dev_idx = 0; dev_idx < config->device_count; ++dev_idx) {
		struct dataplane_device_config *device =
			config->devices + dev_idx;
		free(device->queues);
		free(device->workers);
	}

	for (uint64_t wrk_idx = 0; wrk_idx < config->worker_count; ++wrk_idx) {
		free(config->workers[wrk_idx].assignments);
	}

	free(config->workers);
	free(config->devices);
	free(config->connections);
	free(config);
}
