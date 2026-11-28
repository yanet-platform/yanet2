//! A classifier as one tree: each joint, rule table and rule map holds the
//! classifiers whose classes it reads.
//!
//! C joins two stages into a table sized by their class counts, and its
//! queries pass the table only the classes of those stages
//! (`lib/classify/query.h`). The tree keeps that promise by construction: the
//! control plane writes a node together with the very nodes it was joined or
//! decoded from, and its fields are private, so no lookup takes a class from
//! module code. Private table access checks indices in debug builds.

use core::{
    array,
    mem::{offset_of, size_of},
};

use yanet_shm::{layout::Param, rel::ShmBox, resolve::Resolve, shm::ShmLayout};

use super::{Joint, LineAttr, Net4Attr, Net6Attr, PortAttr, Rule, RuleMap, RuleTable, lpm_value};

/// A classifier of a tree: an attribute, or a joint of two classifiers.
///
/// Sealed: every node is one of this module's, so each class it reports is a
/// class of the stage it was compiled or joined with.
///
/// ```compile_fail,E0277
/// use yanet_shm::{resolve::Resolve, shm::ShmLayout};
/// use yanet_sys::classify::Node;
/// #[repr(C)]
/// struct Mine(u32);
/// // SAFETY: an integer accepts every bit pattern and needs no drop.
/// unsafe impl ShmLayout for Mine {}
/// impl Node for Mine {
///     type Key = u32;
///     fn class<'g>(&'g self, _: &impl Resolve<'g>, key: u32) -> u32 {
///         key
///     }
/// }
/// ```
pub trait Node: ShmLayout + sealed::Sealed {
    /// What the node classifies: an attribute's value, or the pair of keys
    /// of a joint.
    type Key: Copy + Default;

    /// The class of a key.
    #[doc(hidden)]
    fn class<'g>(&'g self, cx: &impl Resolve<'g>, key: Self::Key) -> u32;

    /// The class of the key of each item of a burst of at most [`BURST`].
    #[doc(hidden)]
    #[inline(always)]
    fn classes<'g, T>(
        &'g self,
        cx: &impl Resolve<'g>,
        items: &[T],
        key: impl Fn(&T) -> Self::Key,
        classes: &mut [u32],
    ) {
        for (class, item) in classes.iter_mut().zip(items) {
            *class = self.class(cx, key(item));
        }
    }
}

/// The most items a burst lookup classifies a stage at a time: the burst a
/// dataplane worker hands a module.
pub const BURST: usize = 32;

impl Node for Net4Attr {
    type Key = [u8; 4];

    #[inline(always)]
    fn class<'g>(&'g self, cx: &impl Resolve<'g>, addr: [u8; 4]) -> u32 {
        lpm_value(&self.lpm, cx, &addr)
    }
}

impl Node for Net6Attr {
    type Key = [u8; 16];

    #[inline(always)]
    fn class<'g>(&'g self, cx: &impl Resolve<'g>, addr: [u8; 16]) -> u32 {
        Net6Attr::class(self, cx, &addr)
    }
}

impl Node for PortAttr {
    type Key = u16;

    #[inline(always)]
    fn class<'g>(&'g self, cx: &impl Resolve<'g>, port: u16) -> u32 {
        self.line.get(cx, u32::from(port))
    }
}

impl Node for LineAttr {
    type Key = u8;

    #[inline(always)]
    fn class<'g>(&'g self, cx: &impl Resolve<'g>, value: u8) -> u32 {
        self.line.get(cx, u32::from(value))
    }
}

/// The joint of two classifiers: the class of each pair of their classes.
#[repr(C)]
pub struct Join<L, R> {
    table: Joint,
    left: ShmBox<L>,
    right: ShmBox<R>,
}

/// The first rule each pair of classes of two classifiers matches.
#[repr(C)]
pub struct Rules<L, R> {
    table: RuleTable,
    left: ShmBox<L>,
    right: ShmBox<R>,
}

/// The first rule each class of a classifier matches.
#[repr(C)]
pub struct Decode<N> {
    map: RuleMap,
    input: ShmBox<N>,
}

impl<L: Node, R: Node> Node for Join<L, R> {
    type Key = (L::Key, R::Key);

    #[inline(always)]
    fn class<'g>(&'g self, cx: &impl Resolve<'g>, (left, right): Self::Key) -> u32 {
        let (left, right) = (self.left.get(cx).class(cx, left), self.right.get(cx).class(cx, right));
        self.pair(cx, left, right)
    }

    #[inline(always)]
    fn classes<'g, T>(
        &'g self,
        cx: &impl Resolve<'g>,
        items: &[T],
        key: impl Fn(&T) -> Self::Key,
        classes: &mut [u32],
    ) {
        let (mut left, mut right) = ([0; BURST], [0; BURST]);
        sides(
            self.left.get(cx),
            self.right.get(cx),
            cx,
            items,
            &key,
            &mut left,
            &mut right,
        );
        for ((class, &left), &right) in classes.iter_mut().zip(&left).zip(&right) {
            *class = self.pair(cx, left, right);
        }
    }
}

impl<L: Node, R: Node> Join<L, R> {
    /// The class of a pair of classes of the inputs.
    #[inline(always)]
    fn pair<'g>(&'g self, cx: &impl Resolve<'g>, left: u32, right: u32) -> u32 {
        let Joint { table } = &self.table;
        table.get(cx, left, right)
    }
}

impl<L: Node, R: Node> Rules<L, R> {
    /// The first rule a key matches, [`Rule::NONE`] for none.
    #[inline(always)]
    pub fn lookup<'g>(&'g self, cx: &impl Resolve<'g>, (left, right): (L::Key, R::Key)) -> Rule {
        let (left, right) = (self.left.get(cx).class(cx, left), self.right.get(cx).class(cx, right));
        self.pair(cx, left, right)
    }

    /// The first rule the key of each item matches, [`Rule::NONE`] for none.
    ///
    /// Classifies the items a burst of [`BURST`] at a time, each stage over
    /// the whole burst before the next: the lookups of a stage are
    /// independent, so their reads of the tables overlap. The key of each
    /// item is taken once.
    ///
    /// # Panics
    ///
    /// The rules are fewer than the items.
    #[inline]
    pub fn lookup_burst<'g, T>(
        &'g self,
        cx: &impl Resolve<'g>,
        items: &[T],
        key: impl Fn(&T) -> (L::Key, R::Key),
        rules: &mut [Rule],
    ) {
        let rules = &mut rules[..items.len()];
        for (items, rules) in items.chunks(BURST).zip(rules.chunks_mut(BURST)) {
            let keys = burst_keys(items, &key);
            let keys = &keys[..items.len()];
            let (mut left, mut right) = ([0; BURST], [0; BURST]);
            sides(
                self.left.get(cx),
                self.right.get(cx),
                cx,
                keys,
                &|&key| key,
                &mut left,
                &mut right,
            );
            for ((rule, &left), &right) in rules.iter_mut().zip(&left).zip(&right) {
                *rule = self.pair(cx, left, right);
            }
        }
    }

    /// The first rule of a pair of classes of the inputs.
    #[inline(always)]
    fn pair<'g>(&'g self, cx: &impl Resolve<'g>, left: u32, right: u32) -> Rule {
        self.table.rule(cx, left, right)
    }
}

impl RuleTable {
    #[inline(always)]
    fn rule<'g>(&'g self, cx: &impl Resolve<'g>, left: u32, right: u32) -> Rule {
        // Keep the map lookup in the row argument to preserve optimized
        // evaluation order.
        Rule(self.rows.get(
            cx,
            {
                let row = self.class_map.get(cx, left);
                debug_assert!(
                    row < self.class_count,
                    "index {row} outside a table of {}",
                    self.class_count
                );
                debug_assert!(right < self.h_dim, "index {right} outside a table of {}", self.h_dim);
                row
            },
            right,
        ))
    }
}

impl RuleMap {
    #[inline(always)]
    fn rule<'g>(&'g self, cx: &impl Resolve<'g>, class: u32) -> Rule {
        Rule(self.line.get(cx, class))
    }
}

impl<N: Node> Decode<N> {
    /// The first rule a key matches, [`Rule::NONE`] for none.
    #[inline(always)]
    pub fn lookup<'g>(&'g self, cx: &impl Resolve<'g>, key: N::Key) -> Rule {
        let class = self.input.get(cx).class(cx, key);
        self.rule(cx, class)
    }

    /// The first rule the key of each item matches, [`Rule::NONE`] for none,
    /// a burst at a time as [`Rules::lookup_burst`] does.
    ///
    /// # Panics
    ///
    /// The rules are fewer than the items.
    #[inline]
    pub fn lookup_burst<'g, T>(
        &'g self,
        cx: &impl Resolve<'g>,
        items: &[T],
        key: impl Fn(&T) -> N::Key,
        rules: &mut [Rule],
    ) {
        let rules = &mut rules[..items.len()];
        for (items, rules) in items.chunks(BURST).zip(rules.chunks_mut(BURST)) {
            let keys = burst_keys(items, &key);
            let mut classes = [0; BURST];
            let classes = &mut classes[..items.len()];
            self.input
                .get(cx)
                .classes(cx, &keys[..items.len()], |&key| key, classes);
            for (rule, &class) in rules.iter_mut().zip(&*classes) {
                *rule = self.rule(cx, class);
            }
        }
    }

    /// The first rule of a class of the input.
    #[inline(always)]
    fn rule<'g>(&'g self, cx: &impl Resolve<'g>, class: u32) -> Rule {
        self.map.rule(cx, class)
    }
}

/// The key of each item of a burst of at most [`BURST`], each taken once,
/// and the default key past them.
///
/// Each slot is written once: a slot past the items gets the default key, a
/// slot of an item only its key.
#[inline(always)]
fn burst_keys<T, K: Copy + Default>(items: &[T], key: impl Fn(&T) -> K) -> [K; BURST] {
    array::from_fn(|index| items.get(index).map_or_else(K::default, &key))
}

/// The classes of both inputs of a node over a burst of items.
///
/// Written into the caller's array of each side, the whole burst through the
/// left input, then through the right.
#[inline(always)]
fn sides<'g, L: Node, R: Node, T>(
    left: &'g L,
    right: &'g R,
    cx: &impl Resolve<'g>,
    items: &[T],
    key: &impl Fn(&T) -> (L::Key, R::Key),
    lefts: &mut [u32; BURST],
    rights: &mut [u32; BURST],
) {
    debug_assert!(items.len() <= BURST, "a burst of {} items", items.len());
    left.classes(cx, items, |item| key(item).0, &mut lefts[..items.len()]);
    right.classes(cx, items, |item| key(item).1, &mut rights[..items.len()]);
}

// The nodes leave no padding: a C table, then the offset words of the boxes.
const _: () = {
    type Edge = ShmBox<u8>;
    assert!(size_of::<Join<u8, u8>>() == size_of::<Joint>() + 2 * size_of::<Edge>());
    assert!(size_of::<Rules<u8, u8>>() == size_of::<RuleTable>() + 2 * size_of::<Edge>());
    assert!(size_of::<Decode<u8>>() == size_of::<RuleMap>() + size_of::<Edge>());
    assert!(offset_of!(Join<u8, u8>, table) == 0);
    assert!(offset_of!(Rules<u8, u8>, table) == 0);
    assert!(offset_of!(Decode<u8>, map) == 0);
};

/// The shared-memory impls of the nodes that hold their inputs.
macro_rules! holders {
    ($($name:ident<$($param:ident),*> $kind:literal $shape:ty { $table:ident; $($edge:ident),* })*) => {$(
        // SAFETY: a `#[repr(C)]` struct of an opaque C object, which accepts
        // every pattern, and offset words, without padding (asserted above);
        // the C object leads, so the node has its alignment, and dropping is
        // a no-op.
        unsafe impl<$($param: ShmLayout),*> ShmLayout for $name<$($param),*> {
            edge_layout!($kind, $shape, [$table, $($edge),*], [$($param),*]);
        }
    )*};
}

holders! {
    Join<L, R> "join" Join<Param<0>, Param<1>> { table; right, left }
    Rules<L, R> "rules" Rules<Param<0>, Param<1>> { table; right, left }
    Decode<N> "decode" Decode<Param<0>> { map; input }
}

mod sealed {
    pub trait Sealed {}

    impl Sealed for super::Net4Attr {}

    impl Sealed for super::Net6Attr {}

    impl Sealed for super::PortAttr {}

    impl Sealed for super::LineAttr {}

    impl<L, R> Sealed for super::Join<L, R> {}
}

#[cfg(test)]
mod tests {
    use core::{
        array,
        mem::{offset_of, size_of},
    };

    use yanet_sdk_testing::{BlockArena, View};

    use super::{Decode, Join, Rules};
    use crate::{
        bindings,
        classify::{
            LineAttr, Rule,
            tests::{block, lay_line, lay_rows, point, put},
        },
    };

    /// The keys of the tests: pairs of bytes over both domains, more than a
    /// burst and not a whole number of bursts.
    fn keys() -> [(u8, u8); 40] {
        array::from_fn(|idx| ((idx * 7) as u8, (idx * 13 + 1) as u8))
    }

    /// The address of a byte attribute laid out in the arena, of the class
    /// of each byte.
    fn line_attr(arena: &'static BlockArena, class: impl Fn(u8) -> u32) -> usize {
        let attr = block(arena, size_of::<LineAttr>());
        let classes: [u32; 256] = array::from_fn(|byte| class(byte as u8));
        lay_line(arena, attr + offset_of!(bindings::classify_attr_line, line), &classes);
        attr
    }

    /// verifies that a decode reads, one key at a time and a burst at a time,
    /// the rule of the joint class of the classes of both bytes of a key.
    #[test]
    fn test_decode_tree_reads_the_rule_of_the_joint_class() {
        type Tree = Decode<Join<LineAttr, LineAttr>>;
        let arena = BlockArena::new();
        let (left, right) = (
            line_attr(arena, |byte| u32::from(byte) % 3),
            line_attr(arena, |byte| u32::from(byte) % 2),
        );
        // The joint class of a pair is its row times two plus its column.
        let join = block(arena, size_of::<Join<LineAttr, LineAttr>>());
        put(arena, join + offset_of!(bindings::value_table, v_dim), 3u32);
        put(arena, join + offset_of!(bindings::value_table, h_dim), 2u32);
        lay_rows(
            arena,
            join + offset_of!(bindings::value_table, values),
            &[&[0, 1], &[2, 3], &[4, 5]],
        );
        point(arena, join + offset_of!(Join<LineAttr, LineAttr>, left), left);
        point(arena, join + offset_of!(Join<LineAttr, LineAttr>, right), right);
        // The last joint class matches no rule.
        let tree = block(arena, size_of::<Tree>());
        lay_line(arena, tree, &[10, 11, 12, 13, 14, Rule::NONE.0]);
        point(arena, tree + offset_of!(Tree, input), join);

        // SAFETY: laid out as C lays it out and the control plane links it,
        // and frozen for the test.
        let (view, tree) = unsafe { (View::new(arena.root()), &*arena.root().ptr::<Tree>(tree)) };
        let expected = keys().map(|(left, right)| match left % 3 * 2 + right % 2 {
            5 => Rule::NONE,
            class => Rule(10 + u32::from(class)),
        });
        assert_eq!(expected, keys().map(|key| tree.lookup(&view, key)));
        let mut burst = [Rule(7); 40];
        tree.lookup_burst(&view, &keys(), |&key| key, &mut burst);
        assert_eq!(expected, burst);
    }

    /// verifies that a rule table reads, one key at a time and a burst at a
    /// time, the rule of the class of the right byte in the row the class of
    /// the left byte maps to.
    #[test]
    fn test_rules_tree_reads_the_row_of_the_left_class() {
        type Tree = Rules<LineAttr, LineAttr>;
        type C = bindings::rule_table;
        let arena = BlockArena::new();
        let (left, right) = (
            line_attr(arena, |byte| u32::from(byte) % 3),
            line_attr(arena, |byte| u32::from(byte) % 2),
        );
        let tree = block(arena, size_of::<Tree>());
        put(arena, tree + offset_of!(C, v_dim), 3u32);
        put(arena, tree + offset_of!(C, h_dim), 2u32);
        lay_line(arena, tree + offset_of!(C, class_map), &[1, 0, 1]);
        lay_rows(arena, tree + offset_of!(C, rows), &[&[5, Rule::NONE.0], &[2, 4]]);
        put(arena, tree + offset_of!(C, class_count), 2u32);
        point(arena, tree + offset_of!(Tree, left), left);
        point(arena, tree + offset_of!(Tree, right), right);

        // SAFETY: laid out as C lays it out and the control plane links it,
        // and frozen for the test.
        let (view, tree) = unsafe { (View::new(arena.root()), &*arena.root().ptr::<Tree>(tree)) };
        let rows = [[Rule(5), Rule::NONE], [Rule(2), Rule(4)]];
        let expected = keys().map(|(left, right)| rows[[1, 0, 1][usize::from(left % 3)]][usize::from(right % 2)]);
        assert_eq!(expected, keys().map(|key| tree.lookup(&view, key)));
        let mut burst = [Rule(7); 40];
        tree.lookup_burst(&view, &keys(), |&key| key, &mut burst);
        assert_eq!(expected, burst);
    }

    #[test]
    #[cfg(debug_assertions)]
    #[should_panic(expected = "index 2 outside a table of 2")]
    fn test_rule_table_rejects_column_at_width() {
        use crate::classify::RuleTable;

        type C = bindings::rule_table;
        let arena = BlockArena::new();
        let addr = block(arena, size_of::<RuleTable>());
        put(arena, addr + offset_of!(C, v_dim), 3u32);
        put(arena, addr + offset_of!(C, h_dim), 2u32);
        lay_line(arena, addr + offset_of!(C, class_map), &[1, 0, 1]);
        lay_rows(arena, addr + offset_of!(C, rows), &[&[7, 8], &[9, 10]]);
        put(arena, addr + offset_of!(C, class_count), 2u32);
        // SAFETY: this initialized rule table has consistent compact geometry
        // and remains frozen. Only the private accessor's column is invalid.
        let view = unsafe { View::new(arena.root()) };
        // SAFETY: the aligned block holds the initialized mirrored rule table.
        let table = unsafe { &*arena.root().ptr::<RuleTable>(addr) };
        table.rule(&view, 0, 2);
    }
}
