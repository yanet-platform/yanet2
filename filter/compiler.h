#pragma once

#include "filter/compiler/attribute.h"
#include "filter/compiler/declare.h"
#include "filter/compiler/helper.h"
#include "filter/filter.h"

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#define FILTER_INIT(filter, tag, rules, rule_count, ctx)                       \
	__extension__({                                                        \
		__label__ init_failed;                                         \
		__label__ init_finish;                                         \
		int _res_;                                                     \
		if (sizeof(__filter_attrs_compiler_##tag) == 0) {              \
			_res_ = -1;                                            \
			goto init_failed;                                      \
		}                                                              \
		_res_ = memory_context_init_from(                              \
			&((filter)->memory_context), (ctx), "filter"           \
		);                                                             \
		if (_res_ < 0) {                                               \
			goto init_failed;                                      \
		}                                                              \
		const size_t _n_ = sizeof(__filter_attrs_compiler_##tag) /     \
				   sizeof(__filter_attrs_compiler_##tag[0]);   \
		/* init leaves */                                              \
		for (size_t _i_ = 0; _i_ < _n_; ++_i_) {                       \
			struct filter_vertex *_v_ = &((filter)->v[_n_ + _i_]); \
			_res_ = value_registry_init(                           \
				&_v_->registry, &((filter)->memory_context)    \
			);                                                     \
			if (_res_ < 0) {                                       \
				goto init_failed;                              \
			}                                                      \
			_v_->data = NULL;                                      \
			_res_ = __filter_attrs_compiler_##tag[_i_].init(       \
				&_v_->registry,                                \
				&_v_->data,                                    \
				(rules),                                       \
				(rule_count),                                  \
				&((filter)->memory_context)                    \
			);                                                     \
			if (_res_ < 0) {                                       \
				goto init_failed;                              \
			}                                                      \
		}                                                              \
		if (_n_ == 1) {                                                \
			struct value_registry _dummy_;                         \
			_res_ = init_dummy_registry(                           \
				&((filter)->memory_context),                   \
				(rule_count),                                  \
				&_dummy_                                       \
			);                                                     \
			if (_res_ < 0) {                                       \
				value_registry_free(&_dummy_);                 \
				goto init_failed;                              \
			}                                                      \
			_res_ = merge_and_set_registry_values(                 \
				&((filter)->memory_context),                   \
				(rules),                                       \
				&_dummy_,                                      \
				&((filter)->v[1].registry),                    \
				&((filter)->v[0].table),                       \
				&((filter)->v[0].registry)                     \
			);                                                     \
			if (_res_ < 0) {                                       \
				value_registry_free(&_dummy_);                 \
				goto init_failed;                              \
			}                                                      \
			goto init_finish;                                      \
		}                                                              \
		for (size_t _idx_ = _n_ - 1; _idx_ >= 2; --_idx_) {            \
			_res_ = merge_and_collect_registry(                    \
				&((filter)->memory_context),                   \
				&((filter)->v[2 * _idx_].registry),            \
				&((filter)->v[2 * _idx_ + 1].registry),        \
				&((filter)->v[_idx_].table),                   \
				&((filter)->v[_idx_].registry)                 \
			);                                                     \
			if (_res_ < 0) {                                       \
				goto init_failed;                              \
			}                                                      \
		}                                                              \
		_res_ = merge_and_set_registry_values(                         \
			&((filter)->memory_context),                           \
			(rules),                                               \
			&((filter)->v[2 * 1].registry),                        \
			&((filter)->v[2 * 1 + 1].registry),                    \
			&((filter)->v[1].table),                               \
			&((filter)->v[1].registry)                             \
		);                                                             \
	init_failed:                                                           \
	init_finish:                                                           \
		_res_;                                                         \
	})

#define FILTER_FREE(filter, tag)                                               \
	__extension__({                                                        \
		const size_t _n_ = sizeof(__filter_attrs_compiler_##tag) /     \
				   sizeof(__filter_attrs_compiler_##tag[0]);   \
		for (size_t _i_ = 0; _i_ < _n_; ++_i_) {                       \
			struct filter_vertex *_v_ = &((filter)->v[_n_ + _i_]); \
			__filter_attrs_compiler_##tag[_i_].free(               \
				ADDR_OF(&_v_->data),                           \
				&((filter)->memory_context)                    \
			);                                                     \
			SET_OFFSET_OF(&_v_->data, NULL);                       \
		}                                                              \
		for (size_t _i_ = 1; _i_ < 2 * _n_; ++_i_) {                   \
			value_registry_free(&((filter)->v[_i_].registry));     \
		}                                                              \
		for (size_t _i_ = 1; _i_ < _n_; ++_i_) {                       \
			value_table_free(&((filter)->v[_i_].table));           \
		}                                                              \
		if (_n_ == 1) {                                                \
			struct filter_vertex *_v0_ = &((filter)->v[0]);        \
			value_registry_free(&_v0_->registry);                  \
			value_table_free(&_v0_->table);                        \
		}                                                              \
	})
