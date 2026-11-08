//! The control-plane side of a classification: the compiles, the joins and
//! the decodes a build composes, each the C routine of `lib/classify`.
//!
//! A compile or a join makes a [`Branch`], a classifier with the stage of its
//! classes. A branch remembers the size of the ruleset it was compiled or
//! joined over, and a join or a decode refuses branches and projections of
//! other sizes: the identity of a ruleset is its length only, so the caller
//! keeps one ruleset per composition.

use core::{
    ffi::{CStr, c_int},
    fmt,
    marker::PhantomData,
    mem::size_of,
};

use super::{Build, BuildError, Kind, MemoryContext, Owned, Owner, Refusal, Scope, refused};
use crate::{
    bindings,
    classify::{Attribute, Decode, Join, Node, Paths, Rules, link},
};

/// The classes of a stage of a composition over a ruleset of build `'b`: the
/// registry of its class space and the group of each rule.
///
/// Held by a [`Branch`] and read by the joins and the decodes of the same
/// build and ruleset; it is scratch of the build in its memory, which it
/// returns when dropped.
struct Stage<'b, W: Owner> {
    addr: usize,
    rules: u32,
    memory: &'b MemoryContext<'b, W>,
    _brand: PhantomData<fn(&'b ()) -> &'b ()>,
}

impl<W: Owner> fmt::Debug for Stage<'_, W> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.debug_struct("Stage")
            .field("addr", &self.addr)
            .field("rules", &self.rules)
            .finish()
    }
}

impl<W: Owner> Stage<'_, W> {
    fn raw(&self) -> *const bindings::classifier {
        self.memory.root().ptr(self.addr)
    }
}

impl<W: Owner> Drop for Stage<'_, W> {
    fn drop(&mut self) {
        let root = self.memory.root();
        // SAFETY: the stage was filled whole over its rule count in the
        // build's context, which is live, and its block, allocated with the
        // size of one stage, is referenced by nothing else.
        unsafe {
            bindings::yanet_sys_classify_stage_free(
                root.ptr(self.addr),
                root.ptr(self.memory.context.get()),
                self.rules,
            );
            self.memory.free(self.addr, size_of::<bindings::classifier>());
        }
    }
}

/// The rule count of a ruleset as C takes it.
fn rule_count(len: usize) -> Result<u32, Refusal> {
    u32::try_from(len).map_err(|_| Refusal::TooManyItems)
}

/// Refuses inputs of a join or a decode over rulesets of different sizes.
fn same_ruleset(sizes: &[usize]) -> Result<(), Refusal> {
    match sizes {
        [first, rest @ ..] if rest.iter().any(|size| size != first) => Err(Refusal::RuleSetMismatch),
        _ => Ok(()),
    }
}

/// A path of [`Scope::join_paths`]: the projection of its rules that
/// restrict a port, which its rule table joins, and of those that do not,
/// which its rule map decodes, with the names of both in the memory tree.
#[derive(Debug)]
pub struct PathRules<'p, R> {
    pub table: &'p CStr,
    pub ported: &'p [Option<&'p R>],
    pub map: &'p CStr,
    pub plain: &'p [Option<&'p R>],
}

/// A classifier of a tree a build wrote, with the stage of its classes.
///
/// Made by [`Scope::compile`] and [`Scope::join`] only, and taken whole by the
/// joins and the decode, so a node always reads the classes of the classifiers
/// it holds. Dropped, it returns the classifier and the stage.
///
/// ```compile_fail,E0451
/// use yanet_sys::{
///     classify::Net4Attr,
///     cp::{Branch, Owned, Owner},
/// };
/// fn forge<'b, W: Owner>(value: Owned<'b, Net4Attr, W>) -> Branch<'b, Net4Attr, W> {
///     Branch { value, stage: unimplemented!() }
/// }
/// ```
#[must_use]
pub struct Branch<'b, T: Build, W: Owner> {
    value: Owned<'b, T, W>,
    stage: Stage<'b, W>,
}

impl<T: Build, W: Owner> fmt::Debug for Branch<'_, T, W> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.debug_struct("Branch").field("stage", &self.stage).finish()
    }
}

impl<'b, W: Owner, E: From<BuildError<W::Error>>, K: Kind> Scope<'b, '_, W, E, K> {
    /// Joins two branches of the same ruleset into a branch that holds them.
    ///
    /// # Errors
    ///
    /// The branches are over rulesets of different sizes, or the memory is
    /// exhausted; nothing of the join or the branches is left allocated.
    pub fn join<L: Node, R: Node>(
        self,
        name: &CStr,
        left: Branch<'b, L, W>,
        right: Branch<'b, R, W>,
    ) -> Result<Branch<'b, Join<L, R>, W>, E> {
        same_ruleset(&[left.stage.rules as usize, right.stage.rules as usize]).map_err(refused::<W, E>)?;
        let memory = self.memory;
        let (Branch { value: left, stage: left_stage }, Branch { value: right, stage: right_stage }) = (left, right);
        self.staged(left_stage.rules, |context, stage, join: *mut Join<L, R>| {
            // SAFETY: both stages were filled over the rule count, and the
            // joint leads the zeroed node.
            let status = unsafe {
                bindings::yanet_sys_classify_join(
                    context,
                    name.as_ptr(),
                    left_stage.raw(),
                    right_stage.raw(),
                    left_stage.rules,
                    join.cast(),
                    stage,
                )
            };
            if status == 0 {
                let (left_edge, right_edge) = Join::<L, R>::EDGES;
                // SAFETY: the node's joint is written whole and its edges are
                // not; the classifiers are of this build.
                unsafe {
                    link(memory, join.addr() + left_edge, left);
                    link(memory, join.addr() + right_edge, right);
                }
            }
            status
        })
    }

    /// Joins two branches into the first rule of a projection of their
    /// ruleset each pair of their classes matches, holding the branches.
    ///
    /// A rule is in the projection when its slot is `Some`, whatever it
    /// holds.
    ///
    /// # Errors
    ///
    /// The branches and the projection are over rulesets of different sizes,
    /// or the memory is exhausted; nothing of the join or the branches is
    /// left allocated.
    pub fn join_rules<L: Node, R: Node, P>(
        self,
        name: &CStr,
        left: Branch<'b, L, W>,
        right: Branch<'b, R, W>,
        rules: &[Option<&P>],
    ) -> Result<Owned<'b, Rules<L, R>, W>, E> {
        same_ruleset(&[left.stage.rules as usize, right.stage.rules as usize, rules.len()]).map_err(refused::<W, E>)?;
        let memory = self.memory;
        let (Branch { value: left, stage: left_stage }, Branch { value: right, stage: right_stage }) = (left, right);
        self.written(|context, table: *mut Rules<L, R>| {
            // SAFETY: both stages were filled over the rule count of the
            // projection, whose slots C only tests for null; the table leads
            // the zeroed node.
            let status = unsafe {
                bindings::yanet_sys_classify_join_rules(
                    context,
                    name.as_ptr(),
                    left_stage.raw(),
                    right_stage.raw(),
                    rules.as_ptr().cast(),
                    left_stage.rules,
                    table.cast(),
                )
            };
            if status == 0 {
                let (left_edge, right_edge) = Rules::<L, R>::EDGES;
                // SAFETY: as for a joint.
                unsafe {
                    link(memory, table.addr() + left_edge, left);
                    link(memory, table.addr() + right_edge, right);
                }
            }
            status
        })
    }

    /// Decodes a branch into the first rule of a projection of its ruleset
    /// each of its classes matches, holding the branch.
    ///
    /// A rule is in the projection when its slot is `Some`, whatever it
    /// holds.
    ///
    /// # Errors
    ///
    /// The branch and the projection are over rulesets of different sizes, or
    /// the memory is exhausted; nothing of the decode or the branch is left
    /// allocated.
    pub fn decode<N: Node, P>(
        self,
        name: &CStr,
        input: Branch<'b, N, W>,
        rules: &[Option<&P>],
    ) -> Result<Owned<'b, Decode<N>, W>, E> {
        same_ruleset(&[input.stage.rules as usize, rules.len()]).map_err(refused::<W, E>)?;
        let memory = self.memory;
        let Branch { value: input, stage } = input;
        self.written(|context, map: *mut Decode<N>| {
            // SAFETY: the stage was filled over the rule count of the
            // projection, whose slots C only tests for null; the map leads
            // the zeroed node.
            let status = unsafe {
                bindings::yanet_sys_classify_decode(
                    context,
                    name.as_ptr(),
                    stage.raw(),
                    rules.as_ptr().cast(),
                    stage.rules,
                    map.cast(),
                )
            };
            if status == 0 {
                // SAFETY: as for a joint.
                unsafe { link(memory, map.addr() + Decode::<N>::EDGE, input) };
            }
            status
        })
    }

    /// Joins a core and the ports into the first rule of each path of a
    /// ruleset, holding both: for each path a projection of its rules that
    /// restrict a port, which the ports were compiled over, and one of those
    /// that do not, each named in the memory tree.
    ///
    /// A rule is in a projection when its slot is `Some`, whatever it holds.
    ///
    /// # Errors
    ///
    /// The branches and the projections are over rulesets of different
    /// sizes, or the memory is exhausted; nothing of the join or the branches
    /// is left allocated.
    pub fn join_paths<C: Node, P: Node, R, const N: usize>(
        self,
        core: Branch<'b, C, W>,
        ports: Branch<'b, P, W>,
        paths: [PathRules<'_, R>; N],
    ) -> Result<Owned<'b, Paths<C, P, N>, W>, E> {
        const { assert!(N > 0, "paths of no path") };
        let count = core.stage.rules as usize;
        let sizes = paths.iter().flat_map(|path| [path.ported.len(), path.plain.len()]);
        if ports.stage.rules as usize != count || sizes.into_iter().any(|size| size != count) {
            return Err(refused::<W, E>(Refusal::RuleSetMismatch));
        }
        let memory = self.memory;
        let (Branch { value: core, stage: core_stage }, Branch { value: ports, stage: ports_stage }) = (core, ports);
        self.written(|context, node: *mut Paths<C, P, N>| {
            let root = memory.root();
            let status = paths.iter().enumerate().try_for_each(|(idx, path)| {
                let (table, map) = (
                    Paths::<C, P, N>::table(node.addr(), idx),
                    Paths::<C, P, N>::map(node.addr(), idx),
                );
                // SAFETY: both stages were filled over the rule count of the
                // projections, whose slots C only tests for null; the table
                // and the map lie in the zeroed node.
                let status = unsafe {
                    match bindings::yanet_sys_classify_join_rules(
                        context,
                        path.table.as_ptr(),
                        core_stage.raw(),
                        ports_stage.raw(),
                        path.ported.as_ptr().cast(),
                        core_stage.rules,
                        root.ptr(table),
                    ) {
                        0 => bindings::yanet_sys_classify_decode(
                            context,
                            path.map.as_ptr(),
                            core_stage.raw(),
                            path.plain.as_ptr().cast(),
                            core_stage.rules,
                            root.ptr(map),
                        ),
                        status => status,
                    }
                };
                if status == 0 { Ok(()) } else { Err(status) }
            });
            if let Err(status) = status {
                // Newest first, as a release frees them.
                for path in (0..N).rev() {
                    // SAFETY: each table and map is written whole, emptied by
                    // the C routine that failed on it, or left zeroed; C
                    // frees an empty one as a no-op.
                    unsafe {
                        bindings::yanet_sys_classify_rule_map_free(root.ptr(Paths::<C, P, N>::map(node.addr(), path)));
                        bindings::yanet_sys_classify_rule_table_free(
                            root.ptr(Paths::<C, P, N>::table(node.addr(), path)),
                        );
                    }
                }
                return status;
            }
            let (core_edge, ports_edge) = Paths::<C, P, N>::EDGES;
            // SAFETY: as for a joint.
            unsafe {
                link(memory, node.addr() + core_edge, core);
                link(memory, node.addr() + ports_edge, ports);
            }
            0
        })
    }

    /// Compiles the classifier of an attribute over a ruleset, one view of
    /// the attribute per rule, into a branch.
    ///
    /// The name titles the memory contexts of the compile in the memory tree.
    ///
    /// # Errors
    ///
    /// The rules are more than C counts, or the memory is exhausted; nothing
    /// of the compile is left allocated.
    pub fn compile<A: Attribute>(self, name: &CStr, rules: &[Option<&A>]) -> Result<Branch<'b, A::Classifier, W>, E> {
        let count = rule_count(rules.len()).map_err(refused::<W, E>)?;
        self.staged(count, |memory, stage, classifier| {
            // SAFETY: a rule slot is a nullable pointer to a view the
            // attribute's compile reads as its C view, borrowed for the call.
            unsafe { A::compile(memory, name.as_ptr(), rules.as_ptr().cast(), count, classifier, stage) }
        })
    }

    /// Allocates the block of a `T` for a C routine of the build's memory
    /// context that writes it whole, returning zero, or leaves it with
    /// nothing allocated.
    ///
    /// The block is zeroed first, as C callers zero what they hand a compile:
    /// a routine that fails part way frees every field of its output, those
    /// it never reached included, which only a zeroed field makes a no-op.
    fn written<T: Build>(
        self,
        write: impl FnOnce(*mut bindings::memory_context, *mut T) -> c_int,
    ) -> Result<Owned<'b, T, W>, E> {
        let memory = self.memory;
        let addr = self.alloc(size_of::<T>())?;
        let root = memory.root();
        // SAFETY: the block is ours, of the size of one `T`.
        unsafe { root.ptr::<u8>(addr).write_bytes(0, size_of::<T>()) };
        if write(root.ptr(memory.context.get()), root.ptr(addr)) != 0 {
            // SAFETY: the block is ours, and the failed routine left nothing
            // in it.
            unsafe { memory.free(addr, size_of::<T>()) };
            // Memory is C's only failure for the routines of this module.
            return Err(refused::<W, E>(Refusal::OutOfMemory));
        }
        // The block of one `T` was allocated from the context and written
        // whole.
        Ok(Owned {
            addr,
            memory,
            _brand: PhantomData,
            _value: PhantomData,
        })
    }

    /// As [`Scope::written`], for a routine that also fills a stage over the
    /// rule count.
    ///
    /// The stage is zeroed first and finalized when the routine fails: a
    /// failed compile may leave part of it allocated, which its C callers
    /// release the same way.
    fn staged<T: Build>(
        self,
        rules: u32,
        write: impl FnOnce(*mut bindings::memory_context, *mut bindings::classifier, *mut T) -> c_int,
    ) -> Result<Branch<'b, T, W>, E> {
        let memory = self.memory;
        let addr = self.alloc(size_of::<bindings::classifier>())?;
        let root = memory.root();
        let stage = root.ptr::<bindings::classifier>(addr);
        // SAFETY: the block is ours, of the size of one stage.
        unsafe { stage.write_bytes(0, 1) };
        match self.written(|context, value| write(context, stage, value)) {
            Ok(value) => Ok(Branch {
                value,
                stage: Stage {
                    addr,
                    rules,
                    memory,
                    _brand: PhantomData,
                },
            }),
            Err(err) => {
                // SAFETY: the stage was zeroed and then, if the routine ran,
                // left by it in a state its C finalizer releases, as it does
                // a zeroed one, in the build's context, which is live; its
                // block is ours.
                unsafe {
                    bindings::yanet_sys_classify_stage_free(stage, root.ptr(memory.context.get()), rules);
                    memory.free(addr, size_of::<bindings::classifier>());
                }
                Err(err)
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::{Refusal, rule_count, same_ruleset};

    /// verifies that inputs over one ruleset size pass and any other size
    /// among them is refused, whichever input differs.
    #[test]
    fn test_same_ruleset_refuses_any_other_size() {
        for (sizes, result) in [
            (&[3, 3][..], Ok(())),
            (&[3, 3, 3], Ok(())),
            (&[0, 0], Ok(())),
            (&[3, 2], Err(Refusal::RuleSetMismatch)),
            (&[3, 3, 4], Err(Refusal::RuleSetMismatch)),
            (&[4, 3, 3], Err(Refusal::RuleSetMismatch)),
        ] {
            assert_eq!(result, same_ruleset(sizes), "{sizes:?}");
        }
    }

    /// verifies that a ruleset of up to `u32::MAX` rules is counted and a
    /// larger one refused.
    #[test]
    fn test_rule_count_refuses_more_rules_than_c_counts() {
        assert_eq!(Ok(u32::MAX), rule_count(u32::MAX as usize));
        assert_eq!(Err(Refusal::TooManyItems), rule_count(u32::MAX as usize + 1));
    }
}
