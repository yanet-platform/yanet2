#include "util.h"
#include "dpdk/def.h"
#include "rte_bitops.h"
#include "rte_common.h"

////////////////////////////////////////////////////////////////////////////////

int
dpdk_init(int argc, char **argv) {
	return rte_eal_init(argc, argv);
}

////////////////////////////////////////////////////////////////////////////////

static struct dpdk_acl_rule
transform_rule(const struct filter_rule *rule, int32_t prior) {
	return (struct dpdk_acl_rule
	){// user data
	  .data =
		  {.userdata = rule->action,
		   .category_mask = 0b1,
		   .priority = prior},

	  // protocol
	  .field[0] =
		  {.value.u8 = rule->transport.proto.proto,
		   .mask_range.u8 = 0xff},

	  // src ip
	  .field[1] =
		  {.value.u32 = rule->net4.srcs[0].addr,
		   .mask_range.u32 = rte_popcount32(rule->net4.srcs[0].mask)},

	  // dst ip
	  .field[2] =
		  {.value.u32 = rule->net4.dsts[0].addr,
		   .mask_range.u32 = rte_popcount32(rule->net4.dsts[0].mask)},

	  // src port
	  .field[3] =
		  {.value.u32 = rule->transport.srcs[0].from,
		   .mask_range.u32 = rule->transport.srcs[0].to},

	  // dst port
	  .field[4] =
		  {.value.u32 = rule->transport.dsts[0].from,
		   .mask_range.u32 = rule->transport.dsts[0].to}
	};
}

////////////////////////////////////////////////////////////////////////////////

int
dpdk_acl_init(
	struct dpdk_acl *acl,
	const struct filter_rule *rules,
	uint32_t rule_count
) {
	if ((acl->ctx = rte_acl_create(&dpdk_acl_params)) == NULL) {
		puts("failed to create DPDK ACL context");
		return -1;
	}

	for (uint32_t i = 0; i < rule_count; ++i) {
		acl->rules[i] = transform_rule(&rules[i], rule_count - i);
	}

	int ret = rte_acl_add_rules(
		acl->ctx, (const struct rte_acl_rule *)acl->rules, rule_count
	);
	if (ret != 0) {
		puts("failed to add rules into the DPDK ACL context");
		return ret;
	}

	acl->cfg.num_categories = 1;
	acl->cfg.num_fields = RTE_DIM(dpdk_acl_field_defs);
	acl->cfg.max_size = 0;
	memcpy(acl->cfg.defs, dpdk_acl_field_defs, sizeof(dpdk_acl_field_defs));

	ret = rte_acl_build(acl->ctx, &acl->cfg);
	if (ret != 0) {
		puts("failed to build DPDK ACL");
		return ret;
	}

	return 0;
}

////////////////////////////////////////////////////////////////////////////////

void
dpdk_acl_debug_fields(const uint8_t *data) {
	for (size_t i = 0; i < RTE_DIM(dpdk_acl_field_defs); ++i) {
		const struct rte_acl_field_def *field = &dpdk_acl_field_defs[i];
		printf("field_index=%u\n", field->field_index);
		printf("input_index=%u\n", field->input_index);
		printf("bytes: [");
		for (uint32_t b = 0; b < field->size; ++b) {
			uint8_t byte = data[field->offset + b];
			printf("%x", byte);
			if (b + 1 < field->size) {
				printf(" ");
			}
		}
		printf("]\n\n");
	}
}