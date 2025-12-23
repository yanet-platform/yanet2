#pragma once

#include "filter/query/attribute.h"
#include "filter/rule.h"

////////////////////////////////////////////////////////////////////////////////

static inline uint32_t
filter_actions_with_category(
	uint32_t *actions, uint32_t count, uint16_t category
) {
	uint32_t count_category = 0;

	for (uint32_t i = 0; i < count; ++i) {
		uint32_t action = actions[i];
		uint16_t cat = FILTER_ACTION_CATEGORY_MASK(action);

		if (cat == 0 || (cat & (1 << category))) {
			actions[count_category++] = action;
		} else {
			continue;
		}

		if (!(action & ACTION_NON_TERMINATE)) {
			break;
		}
	}

	return count_category;
}

////////////////////////////////////////////////////////////////////////////////

#define FILTER_QUERY(                                                          \
	filter_ptr, tag, packet_ptr, actions_out_ptr, count_out_ptr            \
)                                                                              \
	__extension__({                                                        \
		struct filter *_flt_ = (filter_ptr);                           \
		struct packet *_pkt_ = (packet_ptr);                           \
		const size_t _n_ = sizeof(__filter_attrs_query_##tag) /     \
				   sizeof(__filter_attrs_query_##tag[0]);   \
		struct filter_slots _slots_;                                   \
		/* compute classifiers for leaf attributes */                  \
		for (size_t _ai_ = 0; _ai_ < _n_; ++_ai_) {                    \
			size_t _vtx_ = _n_ + _ai_;                             \
			struct filter_vertex *_v_ = &(_flt_)->v[_vtx_];        \
			filter_slots_put_value(                                \
				&_slots_,                                      \
				_vtx_,                                         \
				__filter_attrs_query_##tag[_ai_].query(        \
					_pkt_, ADDR_OF(&_v_->data)             \
				)                                              \
			);                                                     \
		}                                                              \
		/* compute inner vertices except root */                       \
		for (size_t _vtx_ = _n_ - 1; _vtx_ >= 2; --_vtx_) {            \
			struct filter_vertex *_v_ = &(_flt_)->v[_vtx_];        \
			uint32_t _c_ = value_table_get(                        \
				&_v_->table,                                   \
				filter_vertex_left_slot(&_slots_, _vtx_),      \
				filter_vertex_right_slot(&_slots_, _vtx_)      \
			);                                                     \
			filter_slots_put_value(&_slots_, _vtx_, _c_);          \
		}                                                              \
		/* root (1 when n>1, else 0) */                                \
		const size_t _root_ = _n_ > 1;                                 \
		struct filter_vertex *_r_ = &(_flt_)->v[_root_];               \
		uint32_t _res_ = value_table_get(                              \
			&_r_->table,                                           \
			_root_ == 0                                            \
				? 0                                            \
				: filter_vertex_left_slot(&_slots_, _root_),   \
			filter_vertex_right_slot(&_slots_, _root_)             \
		);                                                             \
		struct value_range *_range_ =                                  \
			ADDR_OF(&_r_->registry.ranges) + _res_;                \
		*(actions_out_ptr) = ADDR_OF(&_range_->values);                \
		*(count_out_ptr) = _range_->count;                             \
	})
