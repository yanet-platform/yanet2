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

## Interface

This section decribes offered interface for using filter. There are two approaches to use filter: functional approach and one using macros. The first is more flexible and the second is more effecient. 

### Functional

To initialize filter, one can use `filter_init`, which receives pointer to filter, list of filter attributes and list of rules.
```C
const struct filter_attribute
attributes[2] = {&attribute_port_src, &attribute_port_dst};

struct filter_rule rules[3] = {rule1, rule2, rule3};

struct filter filter;
int res = filter_init(
   &filter, attributes, 2, rules, 3, &memory_context
);
assert(res == 0);
```

Then one can query filter using `filter_query` method, which receives filter pointer and packet, then sets number of found actions and their values. 
```C
uint32_t *actions;
uint32_t count;
filter_query(filter, &packet, &actions, &count);
assert(count == 1);
assert(1 == actions[0]);
```

User can free filter using `filter_free` method, which receives filter pointer.
```C
filter_free(&filter);
```

### Macros

Macros interface allows to use filter more efficiently. It requires to pre-define list of filter attributes (*filter signature*) in compile-time, which allows compiler to inline calls to every filter instance with such signature.

Macros

```C
FILTER_DECLARE(signature_tag, &attr1, &attr2, ...)
```

allows to declare filter signature and link it with provided tag. It opens up into
```C
static const struct filter_attribute *__filter_attrs_signature_tag[] = {
   &attr1, &attr2, ...                                                    
};
```

As signature has been declared, filter with such signature can be initialized and used.
```C
struct filter filter;
FILTER_INIT(&filter, sign, &rule1, 1, &memory_context, &res);
assert(res == 0);
```

`FILTER_INIT` receives pointer to filter, signature tag and list of rules. 
Declaration of the signature with such tag must be visible here. 

Then, one can query filter.
```C
uint32_t *actions;
uint32_t actions_count;
FILTER_QUERY(&filter, sign, &packet, &actions, &actions_count);
```

`FILTER_QUERY` receiver pointer to filter, its signature tag and the packet to query.

To free filter, one can use `FILTER_FREE` macros, which receives point to filter and its signature tag.
```C
FILTER_FREE(&filter, sign);
```

## Examples

### Functional

```C
// main.c

#include "filter.h"

int
main() {
   // Set rules to filter packets
   struct filter_rule rules[5];
   fill_filter_rules(rules);

   // Declare filter attributes (signature)
   const struct filter_attribute *attrs[1] = {&attribute_proto};

   // Init filter
   struct filter filter;
   int res = filter_init(&filter, attrs, 1, rules, 5, &memory_context);
   assert(res == 0);

   // Make query
   uint32_t *actions;
   uint32_t actions_count;
   filter_query(&filter, &packet, &actions, &actions_count);
   assert(actions_count == 1);
   assert(actions[0] == 1);

   // Free filter
   filter_free(&filter);

   return 0;
}
```

### Macros

```C
// main.c

#include "filter.h"

// Declare filter attribute 
// with functions for init, query and free
static const struct filter_attribute
attribute_port_src = {src_port_init, src_port_query, src_port_free};

// Declare filter signature
FILTER_DECLARE(sign, &attribute_port_src);

int
main() {
   // Set rules to filter packets
   struct filter_rule rules[10];
   fill_filter_rules(rules);

   // Init filter with declared signature
   struct filter filter;
   int res;
   FILTER_INIT(&filter, sign, rules, 10, &memory_context, &res);
   assert(res == 0);

   // Make query
   uint32_t *actions;
   uint32_t actions_count;
   FILTER_QUERY(&filter, sign, &packet, &actions, &actions_count);
   assert(actions_count == 1);
   assert(actions[0] == 1);

   // Free filter
   FILTER_FREE(&filter, sign);

   return 0;
}
```