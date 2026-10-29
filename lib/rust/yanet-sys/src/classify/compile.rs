//! The control-plane side of a classification: the compiles, the joins and
//! the decodes a build composes, each the C routine of `lib/classify`.

use core::{
    ffi::{CStr, c_int},
    fmt,
    marker::PhantomData,
    mem::size_of,
};

use super::{Attribute, Joint, RuleMap, RuleTable};
use crate::{
    bindings,
    cp::{Build, BuildError, MemoryContext, Owned, Owner, Refusal, Scope, refused},
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
                root.ptr(self.memory.context().get()),
                self.rules,
            );
            self.memory.free(self.addr, size_of::<bindings::classifier>());
        }
    }
}

/// A classifier or a joint a build wrote, with the stage of its classes.
pub type Staged<'b, T, W> = (Owned<'b, T, W>, Stage<'b, W>);

/// The rule count of a ruleset as C takes it.
fn rule_count<W: Owner, E: From<BuildError<W::Error>>>(len: usize) -> Result<u32, E> {
    // The rule groups of more rules than C counts exceed the largest block.
    u32::try_from(len).map_err(|_| refused::<W, E>(Refusal::BlockTooLarge))
}

impl<'b, W: Owner, E: From<BuildError<W::Error>>> Scope<'b, '_, W, E> {
    /// Compiles the classifier of an attribute over a ruleset, one view of
    /// the attribute per rule, and returns it with the stage of its classes.
    ///
    /// The name titles the memory contexts of the compile in the memory tree.
    ///
    /// # Errors
    ///
    /// The memory is exhausted, or the rules are more than C counts; nothing
    /// of the compile is left allocated.
    pub fn compile<A: Attribute>(self, name: &CStr, rules: &[Option<&A>]) -> Result<Staged<'b, A::Classifier, W>, E> {
        let count = rule_count::<W, E>(rules.len())?;
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
    /// The memory is exhausted; nothing of the join is left allocated.
    ///
    /// # Panics
    ///
    /// The stages are over rulesets of different sizes.
    pub fn join(self, name: &CStr, left: &Stage<'b, W>, right: &Stage<'b, W>) -> Result<Staged<'b, Joint, W>, E> {
        assert_eq!(left.rules, right.rules, "the stages of a join are over one ruleset");
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
    /// The memory is exhausted; nothing of the join is left allocated.
    ///
    /// # Panics
    ///
    /// The stages and the projection are over rulesets of different sizes.
    pub fn join_rules<R>(
        self,
        name: &CStr,
        left: &Stage<'b, W>,
        right: &Stage<'b, W>,
        rules: &[Option<&R>],
    ) -> Result<Owned<'b, RuleTable, W>, E> {
        assert_eq!(left.rules, right.rules, "the stages of a join are over one ruleset");
        assert_eq!(
            left.rules as usize,
            rules.len(),
            "the projection is of the stages' ruleset"
        );
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
    /// The memory is exhausted; nothing of the decode is left allocated.
    ///
    /// # Panics
    ///
    /// The stage and the projection are over rulesets of different sizes.
    pub fn decode<R>(
        self,
        name: &CStr,
        stage: &Stage<'b, W>,
        rules: &[Option<&R>],
    ) -> Result<Owned<'b, RuleMap, W>, E> {
        assert_eq!(
            stage.rules as usize,
            rules.len(),
            "the projection is of the stage's ruleset"
        );
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
    fn written<T: Build>(
        self,
        write: impl FnOnce(*mut bindings::memory_context, *mut T) -> c_int,
    ) -> Result<Owned<'b, T, W>, E> {
        let memory = self.memory();
        let addr = self.alloc(size_of::<T>())?;
        let root = memory.root();
        if write(root.ptr(memory.context().get()), root.ptr(addr)) != 0 {
            // SAFETY: the block is ours, and the failed routine left nothing
            // in it.
            unsafe { memory.free(addr, size_of::<T>()) };
            // Memory is C's only failure for the routines of this module.
            return Err(refused::<W, E>(Refusal::OutOfMemory));
        }
        // SAFETY: the block of one `T` was allocated from the context and
        // written whole.
        Ok(unsafe { Owned::from_raw(addr, memory) })
    }

    /// As [`Scope::written`], for a routine that also fills a stage over the
    /// rule count.
    fn staged<T: Build>(
        self,
        rules: u32,
        write: impl FnOnce(*mut bindings::memory_context, *mut bindings::classifier, *mut T) -> c_int,
    ) -> Result<Staged<'b, T, W>, E> {
        let memory = self.memory();
        let addr = self.alloc(size_of::<bindings::classifier>())?;
        let stage = memory.root().ptr::<bindings::classifier>(addr);
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
                // SAFETY: the stage owns nothing: the routine either did not
                // run, or failed and left it zeroed; its block is ours, and
                // freeing it reads none of its bytes.
                unsafe { memory.free(addr, size_of::<bindings::classifier>()) };
                Err(err)
            }
        }
    }
}
