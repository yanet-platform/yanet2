// Smoke test for the region-based filter library.
//
// Declares a compile signature and the matching module authored lookup
// so the ipfrag attribute is instantiated and compile-checked on both
// the compile and the query path. device and vlan round out the
// compile signature to exercise the multi-attribute merge path. This
// does not run classification; that needs the shared-memory test
// harness and is ported separately.

#include "common/container_of.h"
#include "common/value.h"

#include "lib/dataplane/packet/packet.h"

#include "lib/filter2/classifiers/ipfrag.h"
#include "lib/filter2/compiler.h"
#include "lib/filter2/filter.h"
#include "lib/filter2/query.h"

FILTER_COMPILER_DECLARE(sign_compile, device, vlan, ip_frag);

static inline void
sign_lookup_ip_frag(
	const struct filter_query_attr *attr,
	const struct filter_query_attr_handlers *handlers,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	(void)handlers;

	const struct filter_query_attr_ip_frag *ipfrag_attr =
		container_of(attr, struct filter_query_attr_ip_frag, attr);

	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const uint32_t id = packets[idx]->fragment_offset > 0 ? 1 : 0;
		results[idx] = vline_get(&ipfrag_attr->line, id);
	}
}

FILTER_QUERY_ATTR(sign_attr_ip_frag, sign_lookup_ip_frag)

static const struct filter_query_attr_handlers *sign_query[] = {
	&sign_attr_ip_frag,
};

int
main(void) {
	(void)sign_compile;
	(void)sign_query;
	return 0;
}
