use ynpb::pb::MemoryNode;

pub fn node_live(node: &MemoryNode) -> u64 {
    node.balloc_size.saturating_sub(node.bfree_size)
}

/// Parents precede children in the snapshot, so reverse traversal includes
/// every descendant once without consuming stack space for nested contexts.
pub fn subtree_totals(nodes: &[MemoryNode]) -> Vec<u64> {
    let mut totals: Vec<u64> = nodes.iter().map(node_live).collect();
    for (idx, node) in nodes.iter().enumerate().rev() {
        let parent = node.parent_idx as usize;
        if parent < idx {
            totals[parent] = totals[parent].saturating_add(totals[idx]);
        }
    }
    totals
}

/// Groups contexts by parent, with larger sibling subtrees first.
///
/// Invalid parent links remain separate roots, keeping every context visible
/// and preventing cycles during traversal.
pub struct MemoryTree {
    pub roots: Vec<usize>,
    pub children: Vec<Vec<usize>>,
    pub totals: Vec<u64>,
}

impl MemoryTree {
    pub fn new(nodes: &[MemoryNode]) -> Self {
        let totals = subtree_totals(nodes);
        let mut roots = Vec::new();
        let mut children = vec![Vec::new(); nodes.len()];
        for (idx, node) in nodes.iter().enumerate() {
            let parent = node.parent_idx as usize;
            if parent < idx {
                children[parent].push(idx);
            } else {
                roots.push(idx);
            }
        }
        for siblings in core::iter::once(&mut roots).chain(children.iter_mut()) {
            siblings.sort_by(|&left, &right| {
                totals[right]
                    .cmp(&totals[left])
                    .then_with(|| nodes[left].name.cmp(&nodes[right].name))
                    .then_with(|| left.cmp(&right))
            });
        }
        Self { roots, children, totals }
    }
}

#[cfg(test)]
mod test {
    use ynpb::pb::MemoryNode;

    use super::{MemoryTree, subtree_totals};

    /// Returns a context with byte accounting and no allocation counts.
    fn node(name: &str, parent_idx: u32, balloc_size: u64, bfree_size: u64) -> MemoryNode {
        MemoryNode {
            name: name.to_string(),
            parent_idx,
            balloc_count: 0,
            bfree_count: 0,
            balloc_size,
            bfree_size,
        }
    }

    #[test]
    fn test_subtree_totals_root_equals_sum_of_own_bytes() {
        let nodes = vec![
            node("root", u32::MAX, 100, 0),
            node("filter", 0, 50, 0),
            node("lpm", 0, 30, 10),
            node("value_table", 2, 15, 5),
        ];
        let totals = subtree_totals(&nodes);
        assert_eq!(vec![180, 50, 30, 10], totals);
    }

    #[test]
    fn test_memory_tree_groups_interleaved_descendants_by_subtree_size() {
        let nodes = vec![
            node("root", u32::MAX, 1, 0),
            node("alpha", 0, 10, 0),
            node("gamma", 0, 30, 0),
            node("leaf", 1, 40, 0),
            node("beta", 0, 30, 0),
        ];
        let tree = MemoryTree::new(&nodes);
        assert_eq!(vec![0], tree.roots);
        assert_eq!(vec![vec![1, 4, 2], vec![3], vec![], vec![], vec![]], tree.children);
    }

    #[test]
    fn test_memory_tree_invalid_links_keep_all_contexts_reachable() {
        let nodes = vec![
            node("forward", 2, 10, 0),
            node("self", 1, 20, 0),
            node("child", 0, 30, 0),
            node("missing", 99, 5, 0),
        ];
        let tree = MemoryTree::new(&nodes);
        assert_eq!(vec![0, 1, 3], tree.roots);
        assert_eq!(vec![vec![2], vec![], vec![], vec![]], tree.children);
    }

    #[test]
    fn test_subtree_totals_invalid_parents_remain_separate_roots() {
        let nodes = vec![
            node("root", u32::MAX, 10, 0),
            node("self", 1, 20, 0),
            node("missing", 99, 30, 0),
            node("child", 2, 40, 0),
        ];
        assert_eq!(vec![10, 20, 70, 40], subtree_totals(&nodes));
    }
}
