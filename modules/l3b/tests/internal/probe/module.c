#include "oom_shim.h"
#include "rename.h"

#include "modules/l3b/api/controlplane.c"

#include "probe.h"

// The zeroed probe state is what makes the decoder-call count restart here;
// the witness is copied out before the state leaves scope.
int
l3b_probe_module_update(
	struct cp_module *module,
	const struct l3b_destination_filter_rule *rules,
	uint32_t rule_count,
	int fail_ip6_decode,
	struct l3b_probe_witness *witness,
	yanet_error **err
) {
	struct l3b_probe_state state = {
		.fail_ip6_decode = fail_ip6_decode, .decode_calls = 0
	};
	l3b_probe_active = &state;
	int rc = l3b_probe_module_update_impl(module, rules, rule_count, err);
	l3b_probe_active = NULL;
	*witness = (struct l3b_probe_witness){
		.ip4_decode_succeeded = state.ip4_decode_succeeded,
		.ip6_decode_entered = state.ip6_decode_entered,
		.injected_allocations = state.injected_allocations,
	};
	return rc;
}
