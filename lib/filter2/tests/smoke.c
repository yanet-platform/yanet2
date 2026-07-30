// Smoke test for the region-based filter compile library.
//
// Declares a two-attribute compile signature so the per-attribute compile
// vtable handlers (static inline in the headers) are instantiated and
// compile-checked against the current common/ headers, and the multi-
// attribute merge path in filter_compile is exercised at link time. Only
// device and vlan carry a vtable instance in this snapshot; port,
// proto_range, net4 and net6 are ported as headers but not yet
// instantiable, and the query (classification) side is ported as headers
// but needs const-correctness reconciliation against common/value.h before
// a consumer can include lib/filter2/query.h.

#include "lib/filter2/compiler.h"
#include "lib/filter2/filter.h"

FILTER_COMPILER_DECLARE(sign_compile, device, vlan);

int
main(void) {
	(void)sign_compile;
	return 0;
}
