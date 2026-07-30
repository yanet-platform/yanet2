// Smoke test for the region-based filter library.
//
// Declares compile and query signatures that include the ipfrag attribute so
// its per-attribute vtable handlers (static inline in the headers) are
// instantiated and compile-checked on both the compile and query paths.
// device and vlan round out the compile signature to exercise the
// multi-attribute merge path. This does not run classification; that needs
// the shared-memory test harness and is ported separately.

#include "lib/filter2/compiler.h"
#include "lib/filter2/filter.h"
#include "lib/filter2/query.h"

FILTER_COMPILER_DECLARE(sign_compile, device, vlan, ipfrag);
FILTER_QUERY_DECLARE(sign_query, ipfrag);

int
main(void) {
	(void)sign_compile;
	(void)sign_query;
	return 0;
}
