#include "rule.h"
#include "action.h"

////////////////////////////////////////////////////////////////////////////////

int
acl_rule_fill(
	acl_rule_t *rule,
	struct filter_net4 net4,
	struct filter_net6 net6,
	struct filter_transport transport,
	size_t device_count,
	const char **devices,
	enum acl_action action,
	uint8_t action_flags
) {
	int result = filter_action_pack(
		action, action_flags, device_count, devices, &rule->action
	);
	if (result != 0) {
		return -1;
	}
	rule->net4 = net4;
	rule->net6 = net6;
	rule->transport = transport;
	return 0;
}