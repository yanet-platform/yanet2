#include <limits.h>
#include <stddef.h>
#include <stdint.h>

#include <rte_acl.h>
#include <rte_config.h>

////////////////////////////////////////////////////////////////////////////////

struct ipv4_5tuple {
	uint8_t proto;
	uint32_t src_ip;
	uint32_t dst_ip;
	uint16_t src_port;
	uint16_t dst_port;
};

////////////////////////////////////////////////////////////////////////////////

struct rte_acl_field_def defs[1] = {
	{
		.type = RTE_ACL_FIELD_TYPE_BITMASK,
		.size = sizeof(uint8_t),
		.field_index = 0,
		.input_index = 0,
		.offset = offsetof(struct ipv4_5tuple, proto),
	},
};

RTE_ACL_RULE_DEF(acl_ipv4_rule, RTE_DIM(defs));

struct rte_acl_param dpdk_acl_params = {
	.name = "ACL_example",
	.socket_id = SOCKET_ID_ANY,
	.rule_size = RTE_ACL_RULE_SZ(RTE_DIM(defs)),

	/* number of fields per rule. */
	.max_rule_num = 8, /* maximum number of rules in the AC context. */
};

int
main(int argc, char **argv) {
	int ret = rte_eal_init(argc, argv);
	if (ret < 0) {
		printf("ret=%d\n", ret);
		rte_panic("Cannot init EAL\n");
	}

	const struct acl_ipv4_rule acl_rules[] = {
		{.data = {.userdata = 1, .category_mask = 3, .priority = 1},

		 .field[0] = {.value.u8 = 15, .mask_range.u8 = 0b1010}}
	};

	struct rte_acl_ctx *acx;
	struct rte_acl_config cfg;

	/* create an empty AC context  */
	if ((acx = rte_acl_create(&dpdk_acl_params)) == NULL) {
		printf("ret=%d\n", ret);
		rte_panic("Cannot create ACL\n");
	}

	/* add rules to the context */
	ret = rte_acl_add_rules(
		acx, (const struct rte_acl_rule *)&acl_rules, RTE_DIM(acl_rules)
	);
	if (ret != 0) {
		printf("ret=%d\n", ret);
		rte_panic("Cannot add ACL rules\n");
	}

	cfg.num_categories = 2;
	cfg.num_fields = RTE_DIM(defs);

	memcpy(cfg.defs, defs, sizeof(defs));

	/* build the runtime structures for added rules, with 2 categories. */
	ret = rte_acl_build(acx, &cfg);
	if (ret != 0) {
		printf("ret=%d\n", ret);
		rte_panic("Cannot build ACL\n");
	}

	uint32_t results[4];
	struct ipv4_5tuple data = {.proto = 15};
	const uint8_t *datas = (const uint8_t *)&data;
	ret = rte_acl_classify(acx, &datas, results, 1, 4);
	if (ret != 0) {
		printf("ret=%d\n", ret);
		rte_panic("Cannot classify\n");
	}

	puts("result=");
	for (size_t i = 0; i < 4; ++i) {
		printf("%u ", results[i]);
	}
	puts("");

	puts("OK");
	return 0;
}