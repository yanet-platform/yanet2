#include "diag.h"
#include <assert.h>
#include <stdio.h>
#include <string.h>

int
main() {
	NEW_ERROR("error!");
	PUSH_ERROR("failed to do something %d %d", 1, 2);
	PUSH_ERROR("very failed");

	struct diag diag;
	diag_fill(&diag);

	const char *msg = diag_msg(&diag);

	char buffer[1024];
	sprintf(buffer, "%s", msg);
	int cmp_result =
		memcmp(buffer,
		       "very failed: failed to do something 1 2: error!",
		       strlen(buffer));
	assert(cmp_result == 0);
	diag_reset(&diag);

	// one more error

	NEW_ERROR("123");
	PUSH_ERROR("%d%d%d", 4, 5, 6);
	PUSH_ERROR("789");

	diag_fill(&diag);
	msg = diag_msg(&diag);
	cmp_result = memcmp("789: 456: 123", msg, strlen(msg));
	diag_reset(&diag);
	return 0;
}