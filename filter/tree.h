#pragma once

#include <stddef.h>

static inline size_t tree_vertex_left(size_t vertex) {
    return vertex * 2;
}

static inline size_t tree_vertex_right(size_t vertex) {
    return vertex * 2 + 1;
}

static inline size_t tree_vertex_parent(size_t vertex) {
    return vertex / 2;
}

static inline size_t tree_vertex_attr(size_t attr, size_t attributes_count) {
   return attr + attributes_count;
}

static inline size_t tree_vertex_idx(size_t vertex) {
    return vertex;
}

static inline size_t tree_leaf_idx(size_t leaf, size_t attributes_count) {
    return leaf - attributes_count;
}

static inline int tree_is_vertex_leaf(size_t vertex, size_t attributes_count) {
    return vertex >= attributes_count;
}