#include "oom_shim.h"
#include "rename.h"

#include "objects/l3b/api/l3b_virtual_service_object.c"

#include "lib/l3state/l3state.h"
#include "probe.h"

// The zeroed probe state is what makes the decoder-call count restart here;
// the witness is copied out before the state leaves scope.
struct cp_object *
l3b_probe_service_create(
	const struct l3b_virtual_service_create_config *config,
	int fail_ip6_decode,
	struct l3b_probe_witness *witness,
	yanet_error **err
) {
	struct l3b_probe_state state = {
		.fail_ip6_decode = fail_ip6_decode, .decode_calls = 0
	};
	l3b_probe_active = &state;
	struct cp_object *object = l3b_probe_service_create_impl(config, err);
	l3b_probe_active = NULL;
	*witness = (struct l3b_probe_witness){
		.ip4_decode_succeeded = state.ip4_decode_succeeded,
		.ip6_decode_entered = state.ip6_decode_entered,
		.injected_allocations = state.injected_allocations,
	};
	return object;
}

struct cp_object *
l3b_probe_table_create(
	struct agent *agent, const char *name, yanet_error **err
) {
	return l3b_session_table_object_create(agent, name, 1, 64, 8, err);
}

int
l3b_probe_table_free(struct cp_object *object, yanet_error **err) {
	return l3b_session_table_object_free(object, err);
}

int
l3b_probe_table_seed(struct cp_object *object) {
	struct l3b_session_table_object *table = container_of(
		object, struct l3b_session_table_object, cp_object
	);
	const struct l3s_key key = {
		.family = 4, .src_port = 12345, .src_addr = {192, 0, 2, 1}
	};
	const struct l3s_value value = {
		.family = 4, .destination = {198, 51, 100, 7}
	};
	return l3s_table_insert(&table->table, 0, 1, 1000, &key, &value);
}

// Reads the live flow without inserting, so the pinned backend and deadline
// stay exactly as the seed left them.
int
l3b_probe_table_has_sentinel(struct cp_object *object) {
	struct l3b_session_table_object *table = container_of(
		object, struct l3b_session_table_object, cp_object
	);
	const struct l3s_key key = {
		.family = 4, .src_port = 12345, .src_addr = {192, 0, 2, 1}
	};
	const struct l3s_value expected = {
		.family = 4,
		.expires_at = 1001,
		.destination = {198, 51, 100, 7}
	};
	struct l3s_value actual = {0};
	if (l3s_table_lookup(&table->table, 2, &key, &actual) != 0) {
		return 0;
	}
	return actual.family == expected.family &&
	       actual.reserved == expected.reserved &&
	       actual.expires_at == expected.expires_at &&
	       memcmp(actual.destination,
		      expected.destination,
		      sizeof(actual.destination)) == 0;
}
