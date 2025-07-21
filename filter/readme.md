# Generic filter

## Overview

Generic filter allows to find subset of predefined rules the packet satisfies to. 

For example, suppose the following rules are defined:
1. - Source port is in [10, 30]
   - Destination port is in [20, 30]
2. - Source port is in [25, 40]
   - Destination port is in [10, 35]

Then packet with source port equals to 15 and destination port equals to 25 satisfies to the first rule only. The packet with source port equals to 27 and destination port equals to 25 satisfies both rules.

Rules can be composed from restrictions on IP source/destination addresses, ports, VLAN and protocol types and flags. Also, rules are associated with actions. To be more precise, given packet, filter finds suitable rule with the smallest number and returns its action. If the action is not terminative, actions for the further suitable rules will be returned also, until the terminative action is found (**TODO** tests).

## Filter attributes

Attributes correspond to packet features which can be used by filter. User specifies attributes for the filter configuration. Every attribute should classify packet based on predefined rules. For rules from the previous example, source port ranges are [10, 30] for rule 1 and [25, 40] for rule 2. It means range [10, 24] corresponds to the rule 1, range [25, 30] corresponds to rules 1 and 2, and range [31, 40] corresponds to rule 2 only. Also, ranges [0, 9] and [41, 65536] correspond to no rules. User-define attribute for source port can map each range to its classifier. Also, this attribute must store list of rules which correspond to each classifier. 

An attribute is composed of three user-defined functions.

### Initialization

Initialization function allows to initialize attribute data:
```C
typedef int (*attr_init_func)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *rules,
	size_t rule_count,
	struct memory_context *memory_context
);
```

### Query

Function for query attribute classifier based on the provided packet:
```C
typedef uint32_t (*attr_query_func)(
   struct packet *packet,
   void *data
);
```

### Free

This function allows to free filter attribute data.
```C
typedef void (*attr_free_func)(
   void *data, 
   struct memory_context *memory_context
);
```

## Examples

```C
FILTER_DECLARE(filter, &attribute_port_src);

FILTER_INIT(filter, &rule1, 1, &memory_context, &res);
assert(res == 0);

uint32_t *actions;
uint32_t actions_count;
FILTER_QUERY(filter, &packet, &actions, &actions_count);
assert(actions_count == 1);
assert(actions[0] == 1);

free_packet(&packet);
FILTER_FREE(filter);
```