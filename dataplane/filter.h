#ifndef FILTER_H
#define FILTER_H

/*
 * NOTE: the file should be moved into filter subdirectory and be common
 * for all YANET components.
 */

/*
 * Packet filter analyzes packet contents and returns one unsigned integer
 * value corresponding to matched filter.
 * The value may denote any abstract action like ACL accept/drop/etc, IP or
 * network match or anything else.
 * Module is responsible to encode packet action/actions into unsigned value
 * while ruleset construction and compilation and then decode action/actions
 * from the value.
 *
 * Packet filtering concept consider following items:
 *  - packet classifier - a function which maps packet into some unsigned value
 *  - lookup table combining two unsigned values into third one.
 *
 * Packet filtering consist of following steps:
 *  - invoke classifier function and get initial array of unsigned values
 *    corresponding a packet
 *  - lookup sequence making a new one value from two calculated earlier
 *  - last one lookup result considered as resulting value
 *
 * So typical sequence of filter process may look as:
 *  - call 4 classifiers resulting in an array [34, 22, 14, 16]
 *  - first lookup gets first and third values and fetches 72 from the first
 *    lookup table resulting [34, 22, 14, 16, 72]
 *  - second lookup gets second and fourth values and fetches 12 from the
 *    second lookup table resulting [34, 22, 14, 16, 72, 12]
 *  - the last one lookup gets 5-th and 6-th values and fetches 7 from the
 *    third lookup table resulting [34, 22, 14, 16, 72, 12, 7]
 *  - the 7 is the answer of filtering process
 *
 * Obviously there may be as many classifiers and lookups as one wants.
 *
 * Also there are some considerations about classifiers and lookup tables:
 * - classifiers should not have gaps into returning value set
 * - lookup tables should not have gaps into returning value set
 * - basing on two items above all lookup table are rectangular without any
 *   holes and implemented as one-dimensional arrays in sake of efficiency.
 */

#include <stdint.h>
#include <stdlib.h>

#define FILTER_INVALID ((uint32_t)-1)

struct packet;
struct filter;

typedef uint32_t (*filter_classify)(
	const struct filter *filter,
	const struct packet *packet);

struct filter_lookup {
	uint8_t first_arg;
	uint8_t second_arg;
	uint16_t table_idx;
};

struct filter_table {
	uint32_t first_dim;
	uint32_t second_dim;
	uint32_t *values;
};

static inline int
filter_table_init(struct filter_table *table, uint32_t first_dim, uint32_t second_dim) {
	// zero-initialized
	table->values =
		(uint32_t *)calloc(first_dim * second_dim, sizeof(uint32_t));
	if (table->values == NULL)
		return -1;
	table->first_dim = first_dim;
	table->second_dim = second_dim;
	return 0;
}

static inline uint32_t
filter_table_lookup(
	const struct filter_table *table,
	uint32_t first,
	uint32_t second) {
	return table->values[first * table->first_dim + second];
}

struct filter {
	uint32_t classify_count;
	filter_classify *classify;

	uint32_t lookup_count;
	struct filter_lookup *lookups;

	struct filter_table *tables;
};

static inline uint32_t
filter_process(struct filter *filter, struct packet *packet)
{
	uint32_t *arguments = (uint32_t *)alloca(sizeof(uint32_t) *
						 (filter->classify_count +
						  filter->lookup_count));

	if (arguments == NULL) {
		return FILTER_INVALID;
	}

	for (uint32_t idx = 0; idx < filter->classify_count; ++idx) {
		arguments[idx] = filter->classify[idx](filter, packet);
	}

	for (uint32_t idx = 0; idx < filter->lookup_count; ++idx) {
		const struct filter_lookup *lookup = filter->lookups + idx;
		arguments[idx + filter->classify_count] =
			filter_table_lookup(filter->tables + lookup->table_idx,
					    arguments[lookup->first_arg],
					    arguments[lookup->second_arg]);
	}
}

#endif
