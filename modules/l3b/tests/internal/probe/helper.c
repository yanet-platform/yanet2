#include "oom_shim.h"
#include "rename.h"

// The included production decoder must stay callable by the observing wrapper.
//
// Renaming its definition again leaves the declared export for the wrapper.
#undef classify_decode
#define classify_decode l3b_probe_classify_decode_impl
#include "lib/classify/compiler/helper.c"
#undef classify_decode

_Thread_local struct l3b_probe_state *l3b_probe_active;

// Decoder call order identifies the address family within each construction.
//
// Both construction paths issue at most two decoder calls, IPv4 before IPv6,
// and short-circuit so IPv6 is skipped when IPv4 fails. Each construction
// wrapper installs fresh probe state before delegating, resetting the call
// count for that construction. The decoder wrapper observes progress and
// scopes optional IPv6 decoder allocation failure.
int
l3b_probe_classify_decode(
	struct memory_context *memory_context,
	const char *name,
	const struct classifier *cls,
	const struct classifier_rule *const *rules,
	uint32_t rule_count,
	struct vline *rule_map
) {
	struct l3b_probe_state *state = l3b_probe_active;
	int is_ip6 = state != NULL && state->decode_calls == 1;
	if (is_ip6) {
		state->ip6_decode_entered = 1;
		state->in_ip6_decode = 1;
	}

	int rc = l3b_probe_classify_decode_impl(
		memory_context, name, cls, rules, rule_count, rule_map
	);

	if (state != NULL) {
		state->decode_calls += 1;
		if (is_ip6) {
			state->in_ip6_decode = 0;
		} else if (rc == 0 && state->decode_calls == 1) {
			state->ip4_decode_succeeded = 1;
		}
	}
	return rc;
}
