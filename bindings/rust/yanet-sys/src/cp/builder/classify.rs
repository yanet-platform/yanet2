//! The control-plane side of a classification: the compiles, the joins and
//! the decodes a build composes, each the C routine of `lib/classify`.
//!
//! A stage remembers the size of the ruleset it was compiled or joined over,
//! and a join or a decode refuses stages and projections of other sizes: the
//! identity of a ruleset is its length only, so the caller keeps one ruleset
//! per composition.

use core::{
    ffi::{CStr, c_int},
    fmt,
    marker::PhantomData,
    mem::size_of,
};

use super::{Build, BuildError, Kind, MemoryContext, Owned, Owner, Refusal, Scope, refused};
use crate::{
    bindings,
    classify::{Attribute, Decode, Join, Joint, Node, RuleMap, RuleTable, Rules, link},
};

/// The classes of a stage of a composition over a ruleset of build `'b`: the
/// registry of its class space and the group of each rule.
///
/// Made by [`Scope::compile`] and [`Scope::join`], and read by the joins and
/// the decodes of the same build and ruleset; it is scratch of the build in
/// its memory, which it returns when dropped.
#[must_use]
pub struct Stage<'b, W: Owner> {
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

/// A classifier or a joint a build wrote, with the stage of its classes.
pub type Staged<'b, T, W> = (Owned<'b, T, W>, Stage<'b, W>);

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

/// A classifier of a tree with the stage of its classes, which only the joins
/// and the decodes of the tree take, so a node always reads the classes of the
/// classifiers it holds.
///
/// Made by [`Scope::branch`] and [`Scope::join_branches`]; dropped, it
/// returns the classifier and the stage.
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
    /// Compiles the classifier of an attribute over a ruleset as a branch of
    /// a tree, as [`Scope::compile`] does.
    ///
    /// # Errors
    ///
    /// As for [`Scope::compile`].
    pub fn branch<A>(self, name: &CStr, rules: &[Option<&A>]) -> Result<Branch<'b, A::Classifier, W>, E>
    where
        A: Attribute,
        A::Classifier: Node,
    {
        let (value, stage) = self.compile(name, rules)?;
        Ok(Branch { value, stage })
    }

    /// Joins two branches of the same ruleset into a branch that holds them.
    ///
    /// # Errors
    ///
    /// The branches are over rulesets of different sizes, or the memory is
    /// exhausted; nothing of the join or the branches is left allocated.
    pub fn join_branches<L: Node, R: Node>(
        self,
        name: &CStr,
        left: Branch<'b, L, W>,
        right: Branch<'b, R, W>,
    ) -> Result<Branch<'b, Join<L, R>, W>, E> {
        same_ruleset(&[left.stage.rules as usize, right.stage.rules as usize]).map_err(refused::<W, E>)?;
        let memory = self.memory;
        let (Branch { value: left, stage: left_stage }, Branch { value: right, stage: right_stage }) = (left, right);
        let (value, stage) = self.staged(left_stage.rules, |context, stage, join: *mut Join<L, R>| {
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
        })?;
        Ok(Branch { value, stage })
    }

    /// Joins two branches into the first rule of a projection of their
    /// ruleset each pair of their classes matches, holding the branches.
    ///
    /// A rule is in the projection when its slot is `Some`, whatever it
    /// holds.
    ///
    /// # Errors
    ///
    /// As for [`Scope::join_rules`].
    pub fn branch_rules<L: Node, R: Node, P>(
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
    /// As for [`Scope::decode`].
    pub fn decode_branch<N: Node, P>(
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

    /// Compiles the classifier of an attribute over a ruleset, one view of
    /// the attribute per rule, and returns it with the stage of its classes.
    ///
    /// The name titles the memory contexts of the compile in the memory tree.
    ///
    /// # Errors
    ///
    /// The rules are more than C counts, or the memory is exhausted; nothing
    /// of the compile is left allocated.
    pub fn compile<A: Attribute>(self, name: &CStr, rules: &[Option<&A>]) -> Result<Staged<'b, A::Classifier, W>, E> {
        let count = rule_count(rules.len()).map_err(refused::<W, E>)?;
        self.staged(count, |memory, stage, classifier| {
            // SAFETY: a rule slot is a nullable pointer to a view the
            // attribute's compile reads as its C view, borrowed for the call.
            unsafe { A::compile(memory, name.as_ptr(), rules.as_ptr().cast(), count, classifier, stage) }
        })
    }

    /// Joins the classes of two stages of the same ruleset into a joint and
    /// the stage of its classes.
    ///
    /// # Errors
    ///
    /// The stages are over rulesets of different sizes, or the memory is
    /// exhausted; nothing of the join is left allocated.
    pub fn join(self, name: &CStr, left: &Stage<'b, W>, right: &Stage<'b, W>) -> Result<Staged<'b, Joint, W>, E> {
        same_ruleset(&[left.rules as usize, right.rules as usize]).map_err(refused::<W, E>)?;
        self.staged(left.rules, |memory, stage, joint: *mut Joint| {
            // SAFETY: both stages were filled over the rule count.
            unsafe {
                bindings::yanet_sys_classify_join(
                    memory,
                    name.as_ptr(),
                    left.raw(),
                    right.raw(),
                    left.rules,
                    joint.cast(),
                    stage,
                )
            }
        })
    }

    /// Joins the classes of two stages into the first rule of a projection
    /// of their ruleset each pair matches.
    ///
    /// A rule is in the projection when its slot is `Some`, whatever it
    /// holds.
    ///
    /// # Errors
    ///
    /// The stages and the projection are over rulesets of different sizes,
    /// or the memory is exhausted; nothing of the join is left allocated.
    pub fn join_rules<R>(
        self,
        name: &CStr,
        left: &Stage<'b, W>,
        right: &Stage<'b, W>,
        rules: &[Option<&R>],
    ) -> Result<Owned<'b, RuleTable, W>, E> {
        same_ruleset(&[left.rules as usize, right.rules as usize, rules.len()]).map_err(refused::<W, E>)?;
        self.written(|memory, table: *mut RuleTable| {
            // SAFETY: both stages were filled over the rule count of the
            // projection, whose slots C only tests for null.
            unsafe {
                bindings::yanet_sys_classify_join_rules(
                    memory,
                    name.as_ptr(),
                    left.raw(),
                    right.raw(),
                    rules.as_ptr().cast(),
                    left.rules,
                    table.cast(),
                )
            }
        })
    }

    /// Decodes the classes of a stage into the first rule of a projection of
    /// its ruleset each class matches.
    ///
    /// A rule is in the projection when its slot is `Some`, whatever it
    /// holds.
    ///
    /// # Errors
    ///
    /// The stage and the projection are over rulesets of different sizes, or
    /// the memory is exhausted; nothing of the decode is left allocated.
    pub fn decode<R>(
        self,
        name: &CStr,
        stage: &Stage<'b, W>,
        rules: &[Option<&R>],
    ) -> Result<Owned<'b, RuleMap, W>, E> {
        same_ruleset(&[stage.rules as usize, rules.len()]).map_err(refused::<W, E>)?;
        self.written(|memory, map: *mut RuleMap| {
            // SAFETY: the stage was filled over the rule count of the
            // projection, whose slots C only tests for null.
            unsafe {
                bindings::yanet_sys_classify_decode(
                    memory,
                    name.as_ptr(),
                    stage.raw(),
                    rules.as_ptr().cast(),
                    stage.rules,
                    map.cast(),
                )
            }
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
    ) -> Result<Staged<'b, T, W>, E> {
        let memory = self.memory;
        let addr = self.alloc(size_of::<bindings::classifier>())?;
        let root = memory.root();
        let stage = root.ptr::<bindings::classifier>(addr);
        // SAFETY: the block is ours, of the size of one stage.
        unsafe { stage.write_bytes(0, 1) };
        match self.written(|context, value| write(context, stage, value)) {
            Ok(value) => Ok((
                value,
                Stage {
                    addr,
                    rules,
                    memory,
                    _brand: PhantomData,
                },
            )),
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
