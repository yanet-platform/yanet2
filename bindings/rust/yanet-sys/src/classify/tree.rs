//! A classifier as one tree: each joint, rule table and rule map holds the
//! classifiers whose classes it reads.
//!
//! C joins two stages into a table sized by their class counts, and its
//! queries pass the table only the classes of those stages
//! (`lib/classify/query.h`). The tree keeps that promise by construction: a
//! node is written only by a [`Scope`](crate::cp::Scope), together with the
//! very nodes it was joined or decoded from, and its fields are private, so no
//! lookup takes a class from module code and none checks one. Every read is a
//! Rust port of the C one over the mirrored layouts.

use core::{
    convert::Infallible,
    mem::{offset_of, size_of},
};

use yanet_shm::{rel::ShmBox, resolve::Resolve, shm::ShmLayout};

use super::{Addr4, Joint, LineAttr, Net4Attr, Net6Attr, PortAttr, Rule, RuleMap, RuleTable, lpm_value};
use crate::{
    bindings,
    cp::{Build, MemoryContext, Owned, Owner, Refusal},
};

/// A classifier of a tree: an attribute, or a joint of two classifiers.
///
/// Sealed: every node is one of this module's, so each class it reports is a
/// class of the stage it was compiled or joined with.
///
/// ```compile_fail,E0277
/// use yanet_shm::{resolve::Resolve, shm::ShmLayout};
/// use yanet_sys::{
///     classify::Node,
///     cp::{Build, MemoryContext, Owner, Refusal},
/// };
/// #[repr(C)]
/// struct Mine(u32);
/// unsafe impl ShmLayout for Mine {}
/// unsafe impl Build for Mine {
///     type Parts<'b, W: Owner> = u32;
///     unsafe fn write<W: Owner>(_: u32, _: &MemoryContext<'_, W>, _: usize) -> Result<(), Refusal> {
///         Ok(())
///     }
///     unsafe fn free<W: Owner>(_: usize, _: &MemoryContext<'_, W>) {}
/// }
/// impl Node for Mine {
///     type Key = u32;
///     fn class<'g>(&'g self, _: &impl Resolve<'g>, key: u32) -> u32 {
///         key
///     }
/// }
/// ```
pub trait Node: Build + sealed::Sealed {
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

// SAFETY (every read below): the classifier belongs to the configuration
// published and frozen for 'g (`Resolve` contract) and was built whole by its
// C build, together with the nodes of the classes it reads, whose classes its
// dimensions were sized by.

impl Node for Net4Attr {
    type Key = Addr4;

    #[inline(always)]
    fn class<'g>(&'g self, cx: &impl Resolve<'g>, Addr4(addr): Addr4) -> u32 {
        lpm_value(&self.lpm, cx, &addr.to_ne_bytes())
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
        // SAFETY: see above; the line spans the whole port domain.
        unsafe { self.line.get(cx, u32::from(port)) }
    }
}

impl Node for LineAttr {
    type Key = u8;

    #[inline(always)]
    fn class<'g>(&'g self, cx: &impl Resolve<'g>, value: u8) -> u32 {
        // SAFETY: see above; the line spans the whole byte domain.
        unsafe { self.line.get(cx, u32::from(value)) }
    }
}

/// The joint of two classifiers: the class of each pair of their classes.
///
/// Joined only with its stage, by
/// [`Scope::join`](crate::cp::Scope::join).
#[repr(C)]
pub struct Join<L, R> {
    table: Joint,
    left: ShmBox<L>,
    right: ShmBox<R>,
}

/// The first rule each pair of classes of two classifiers matches.
///
/// Joined by [`Scope::join_rules`](crate::cp::Scope::join_rules).
#[repr(C)]
pub struct Rules<L, R> {
    table: RuleTable,
    left: ShmBox<L>,
    right: ShmBox<R>,
}

/// The first rule each class of a classifier matches.
///
/// Decoded by [`Scope::decode`](crate::cp::Scope::decode).
#[repr(C)]
pub struct Decode<N> {
    map: RuleMap,
    input: ShmBox<N>,
}

/// The first rule each key matches on one of several paths, as the ACL module
/// composes it: a core classifier and a classifier of the ports, which every
/// path shares, and on each path a rule table of their classes over the
/// path's rules that restrict a port and a rule map of the core's classes
/// over its rules that do not.
///
/// The ports are classified over the rules that restrict a port only, so a
/// rule without one stays out of the port classes, and a lookup takes the
/// earlier of the path's table and map. Joined by
/// [`Scope::join_paths`](crate::cp::Scope::join_paths).
#[repr(C)]
pub struct Paths<C, P, const N: usize> {
    tables: [RuleTable; N],
    maps: [RuleMap; N],
    core: ShmBox<C>,
    ports: ShmBox<P>,
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
        let (left, right) = sides(self.left.get(cx), self.right.get(cx), cx, items, &key);
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
        // SAFETY: see above; the classes are of the stages the joint was
        // joined from, below its counts of rows and columns.
        unsafe { table.get(cx, left, right) }
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
            let (left, right) = sides(self.left.get(cx), self.right.get(cx), cx, keys, &|&key| key);
            for ((rule, &left), &right) in rules.iter_mut().zip(&left).zip(&right) {
                *rule = self.pair(cx, left, right);
            }
        }
    }

    /// The first rule of a pair of classes of the inputs.
    #[inline(always)]
    fn pair<'g>(&'g self, cx: &impl Resolve<'g>, left: u32, right: u32) -> Rule {
        // SAFETY: see above; the classes are of the stages the table was
        // joined from.
        unsafe { self.table.rule(cx, left, right) }
    }
}

impl RuleTable {
    /// The first rule of a pair of classes.
    ///
    /// # Safety
    ///
    /// The classes are of the stages the table was joined from: the class
    /// map holds a row for each left class, and each row a rule for each
    /// right class.
    #[inline(always)]
    unsafe fn rule<'g>(&'g self, cx: &impl Resolve<'g>, left: u32, right: u32) -> Rule {
        // SAFETY: the caller's contract, and the table is frozen for 'g.
        Rule(unsafe { self.rows.get(cx, self.class_map.get(cx, left), right) })
    }
}

impl RuleMap {
    /// The first rule of a class.
    ///
    /// # Safety
    ///
    /// The class is of the stage the map was decoded from, below its size.
    #[inline(always)]
    unsafe fn rule<'g>(&'g self, cx: &impl Resolve<'g>, class: u32) -> Rule {
        // SAFETY: the caller's contract, and the map is frozen for 'g.
        Rule(unsafe { self.line.get(cx, class) })
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
        // SAFETY: see above; the class is of the stage the map was decoded
        // from.
        unsafe { self.map.rule(cx, class) }
    }
}

impl<C: Node, P: Node, const N: usize> Paths<C, P, N> {
    /// The first rule a key matches on a path, [`Rule::NONE`] for none.
    ///
    /// # Panics
    ///
    /// The path is not below `N`.
    #[inline(always)]
    pub fn lookup<'g>(&'g self, cx: &impl Resolve<'g>, path: usize, (core, ports): (C::Key, P::Key)) -> Rule {
        let (core, ports) = (self.core.get(cx).class(cx, core), self.ports.get(cx).class(cx, ports));
        self.rule(cx, path, core, ports)
    }

    /// The first rule the key of each item matches on its path, [`Rule::NONE`]
    /// for none, a burst at a time as [`Rules::lookup_burst`] does.
    ///
    /// # Panics
    ///
    /// The rules are fewer than the items, or a path is not below `N`.
    #[inline]
    pub fn lookup_burst<'g, T>(
        &'g self,
        cx: &impl Resolve<'g>,
        items: &[T],
        key: impl Fn(&T) -> (usize, (C::Key, P::Key)),
        rules: &mut [Rule],
    ) {
        let rules = &mut rules[..items.len()];
        for (items, rules) in items.chunks(BURST).zip(rules.chunks_mut(BURST)) {
            let keys = burst_keys(items, &key);
            let keys = &keys[..items.len()];
            let (core, ports) = sides(self.core.get(cx), self.ports.get(cx), cx, keys, &|&(_, key)| key);
            for (((rule, &(path, _)), &core), &ports) in rules.iter_mut().zip(keys).zip(&core).zip(&ports) {
                *rule = self.rule(cx, path, core, ports);
            }
        }
    }

    /// The earlier of the rules of the path's table and map.
    #[inline(always)]
    fn rule<'g>(&'g self, cx: &impl Resolve<'g>, path: usize, core: u32, ports: u32) -> Rule {
        let (table, map) = (&self.tables[path], &self.maps[path]);
        // SAFETY: see above; the classes are of the stages every table and
        // map of the node was joined or decoded from.
        let (Rule(ported), Rule(plain)) = unsafe { (table.rule(cx, core, ports), map.rule(cx, core)) };
        // No rule is the largest word, so the first match is the smaller.
        Rule(ported.min(plain))
    }
}

/// The key of each item of a burst of at most [`BURST`], each taken once,
/// and the default key past them.
#[inline(always)]
fn burst_keys<T, K: Copy + Default>(items: &[T], key: impl Fn(&T) -> K) -> [K; BURST] {
    let mut keys = [K::default(); BURST];
    for (slot, item) in keys.iter_mut().zip(items) {
        *slot = key(item);
    }
    keys
}

/// The classes of both inputs of a node over a burst of items: the whole
/// burst through the left input, then through the right.
#[inline(always)]
fn sides<'g, L: Node, R: Node, T>(
    left: &'g L,
    right: &'g R,
    cx: &impl Resolve<'g>,
    items: &[T],
    key: &impl Fn(&T) -> (L::Key, R::Key),
) -> (BurstClasses, BurstClasses) {
    debug_assert!(items.len() <= BURST, "a burst of {} items", items.len());
    let (mut lefts, mut rights) = (BurstClasses::new(items.len()), BurstClasses::new(items.len()));
    left.classes(cx, items, |item| key(item).0, lefts.as_mut());
    right.classes(cx, items, |item| key(item).1, rights.as_mut());
    (lefts, rights)
}

/// The classes of a burst of at most [`BURST`] items.
struct BurstClasses {
    classes: [u32; BURST],
    len: usize,
}

impl BurstClasses {
    fn new(len: usize) -> Self {
        Self { classes: [0; BURST], len }
    }

    fn as_mut(&mut self) -> &mut [u32] {
        &mut self.classes[..self.len]
    }
}

impl<'a> IntoIterator for &'a BurstClasses {
    type Item = &'a u32;
    type IntoIter = core::slice::Iter<'a, u32>;

    fn into_iter(self) -> Self::IntoIter {
        self.classes[..self.len].iter()
    }
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
    // The tables and the maps keep the alignment of the edges, so no array
    // of them leaves padding before the next field.
    assert!(size_of::<RuleTable>().is_multiple_of(size_of::<Edge>()));
    assert!(size_of::<RuleMap>().is_multiple_of(size_of::<Edge>()));
    assert!(
        size_of::<Paths<u8, u8, 3>>() == 3 * size_of::<RuleTable>() + 3 * size_of::<RuleMap>() + 2 * size_of::<Edge>()
    );
};

// SAFETY: a `#[repr(C)]` struct of arrays of opaque C objects, which accept
// every pattern, and offset words, without padding (asserted above); the C
// objects lead, so the node has their alignment, and dropping is a no-op.
unsafe impl<C: ShmLayout, P: ShmLayout, const N: usize> ShmLayout for Paths<C, P, N> {}

// SAFETY: written only by the scope's join of paths, which puts every table,
// map and edge whole; freeing returns what the C builds allocated in the
// context, then the inputs, the ports before the core.
unsafe impl<C: Build, P: Build, const N: usize> Build for Paths<C, P, N> {
    type Parts<'b, W: Owner> = Infallible;

    unsafe fn write<W: Owner>(parts: Infallible, _: &MemoryContext<'_, W>, _: usize) -> Result<(), Refusal> {
        match parts {}
    }

    unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>) {
        let root = memory.root();
        // SAFETY: the node holds a value (caller contract), which only the
        // scope's join of paths writes, whole; the newest first.
        unsafe {
            for path in (0..N).rev() {
                bindings::yanet_sys_classify_rule_map_free(root.ptr(Self::map(addr, path)));
                bindings::yanet_sys_classify_rule_table_free(root.ptr(Self::table(addr, path)));
            }
            <ShmBox<P> as Build>::free::<W>(addr + offset_of!(Self, ports), memory);
            <ShmBox<C> as Build>::free::<W>(addr + offset_of!(Self, core), memory);
        }
    }
}

/// The shared-memory impls of the nodes that hold their inputs.
macro_rules! holders {
    ($($name:ident<$($param:ident),*> { $table:ident: $raw:ty, $free:ident; $($edge:ident: $ty:ident),* })*) => {$(
        // SAFETY: a `#[repr(C)]` struct of an opaque C object, which accepts
        // every pattern, and offset words, without padding (asserted above);
        // the C object leads, so the node has its alignment, and dropping is
        // a no-op.
        unsafe impl<$($param: ShmLayout),*> ShmLayout for $name<$($param),*> {}

        // SAFETY: written only by the scope's joins and decodes, which put
        // the C object and the owned edges whole; freeing returns what the C
        // build allocated in its context, then its inputs, the right one
        // before the left.
        unsafe impl<$($param: Build),*> Build for $name<$($param),*> {
            type Parts<'b, W: Owner> = Infallible;

            unsafe fn write<W: Owner>(
                parts: Infallible,
                _: &MemoryContext<'_, W>,
                _: usize,
            ) -> Result<(), Refusal> {
                match parts {}
            }

            unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>) {
                // SAFETY: the node holds a value (caller contract), which only
                // the scope's joins and decodes write, whole: the C object at
                // its start, then the edges.
                unsafe {
                    bindings::$free(memory.root().ptr::<$raw>(addr + offset_of!(Self, $table)));
                    $(<ShmBox<$ty> as Build>::free::<W>(addr + offset_of!(Self, $edge), memory);)*
                }
            }
        }
    )*};
}

holders! {
    Join<L, R> { table: bindings::value_table, yanet_sys_classify_joint_free; right: R, left: L }
    Rules<L, R> { table: bindings::rule_table, yanet_sys_classify_rule_table_free; right: R, left: L }
    Decode<N> { map: bindings::vline, yanet_sys_classify_rule_map_free; input: N }
}

mod sealed {
    pub trait Sealed {}

    impl Sealed for super::Net4Attr {}

    impl Sealed for super::Net6Attr {}

    impl Sealed for super::PortAttr {}

    impl Sealed for super::LineAttr {}

    impl<L, R> Sealed for super::Join<L, R> {}
}

/// Writes an owned edge of a node whose C object a build has just written.
///
/// # Safety
///
/// The edge is an unwritten offset word of a node in a block of the memory
/// context's build, whose C object is written whole; the part is of the same
/// build.
#[doc(hidden)]
pub unsafe fn link<W: Owner, T: Build>(memory: &MemoryContext<'_, W>, edge: usize, part: Owned<'_, T, W>) {
    // SAFETY: the edge is an aligned, unwritten offset word of the node
    // (caller contract); a box write only stores the offset.
    let written = unsafe { <ShmBox<T> as Build>::write::<W>(part, memory, edge) };
    debug_assert!(written.is_ok(), "a box write does not fail");
}

impl<L, R> Join<L, R> {
    /// Offsets of the left and the right edge.
    #[doc(hidden)]
    pub const EDGES: (usize, usize) = (offset_of!(Self, left), offset_of!(Self, right));
}

impl<L, R> Rules<L, R> {
    /// Offsets of the left and the right edge.
    #[doc(hidden)]
    pub const EDGES: (usize, usize) = (offset_of!(Self, left), offset_of!(Self, right));
}

impl<N> Decode<N> {
    /// Offset of the edge of the input.
    #[doc(hidden)]
    pub const EDGE: usize = offset_of!(Self, input);
}

impl<C, P, const N: usize> Paths<C, P, N> {
    /// Offsets of the edges of the core and of the ports.
    #[doc(hidden)]
    pub const EDGES: (usize, usize) = (offset_of!(Self, core), offset_of!(Self, ports));

    /// Address of the rule table of a path of the node at an address.
    #[doc(hidden)]
    pub fn table(addr: usize, path: usize) -> usize {
        addr + offset_of!(Self, tables) + path * size_of::<RuleTable>()
    }

    /// Address of the rule map of a path of the node at an address.
    #[doc(hidden)]
    pub fn map(addr: usize, path: usize) -> usize {
        addr + offset_of!(Self, maps) + path * size_of::<RuleMap>()
    }
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
        let (left_edge, right_edge) = Join::<LineAttr, LineAttr>::EDGES;
        point(arena, join + left_edge, left);
        point(arena, join + right_edge, right);
        // The last joint class matches no rule.
        let tree = block(arena, size_of::<Tree>());
        lay_line(arena, tree, &[10, 11, 12, 13, 14, Rule::NONE.0]);
        point(arena, tree + Tree::EDGE, join);

        // SAFETY: laid out as C lays it out and a build links it, and frozen
        // for the test.
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
        let (left_edge, right_edge) = Tree::EDGES;
        point(arena, tree + left_edge, left);
        point(arena, tree + right_edge, right);

        // SAFETY: laid out as C lays it out and a build links it, and frozen
        // for the test.
        let (view, tree) = unsafe { (View::new(arena.root()), &*arena.root().ptr::<Tree>(tree)) };
        let rows = [[Rule(5), Rule::NONE], [Rule(2), Rule(4)]];
        let expected = keys().map(|(left, right)| rows[[1, 0, 1][usize::from(left % 3)]][usize::from(right % 2)]);
        assert_eq!(expected, keys().map(|key| tree.lookup(&view, key)));
        let mut burst = [Rule(7); 40];
        tree.lookup_burst(&view, &keys(), |&key| key, &mut burst);
        assert_eq!(expected, burst);
    }
}
