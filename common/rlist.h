#pragma once

// Intrusive circular doubly-linked list, list_head style.
//
// A member embeds a node and is reached back through the node; a head is
// a plain node that points at itself when empty. Insertion appends at
// the tail, so first-out order matches insertion order.
struct rlist {
	struct rlist *prev;
	struct rlist *next;
};

static inline void
rlist_init(struct rlist *head) {
	head->prev = head;
	head->next = head;
}

static inline void
rlist_remove(struct rlist *node) {
	node->prev->next = node->next;
	node->next->prev = node->prev;
}

static inline void
rlist_add(struct rlist *head, struct rlist *node) {
	node->prev = head->prev;
	node->next = head;
	head->prev->next = node;
	head->prev = node;
}

static inline struct rlist *
rlist_first(struct rlist *head) {
	return head->next;
}

static inline int
rlist_empty(struct rlist *head) {
	return head->next == head;
}
