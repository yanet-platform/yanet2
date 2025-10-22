#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

typedef uint8_t balancer_real_flags_t;

struct real {
	balancer_real_flags_t flags;
	uint16_t weight;
	uint8_t dst_addr[16];
	uint8_t src_addr[16];
	uint8_t src_mask[16];
};