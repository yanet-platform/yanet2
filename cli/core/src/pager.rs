//! Pages long human output through an external pager, the way git does.

use core::{
    mem, ptr,
    sync::atomic::{AtomicUsize, Ordering},
};
use std::{
    env,
    io::{self, Write},
    os::fd::AsRawFd,
    process::{Child, Command, Stdio},
};

use crate::display;

/// The pager used when neither `YANET_PAGER` nor `PAGER` names one.
const DEFAULT_PAGER: &str = "less";

/// The `less` options used when `LESS` is unset: quit on a single screen,
/// pass colours, chop long lines and keep the screen on exit.
const DEFAULT_LESS: &str = "FRSX";

/// Width of the terminal a running pager took stdout from, zero without one.
static WIDTH: AtomicUsize = AtomicUsize::new(0);

/// Returns the width of the terminal a running pager took stdout from.
pub fn width() -> Option<usize> {
    match WIDTH.load(Ordering::Relaxed) {
        0 => None,
        cols => Some(cols),
    }
}

/// A running pager reading the process stdout, which a drop hands back
/// before waiting for the pager to exit.
pub struct Pager {
    child: Child,
    saved_stdout: libc::c_int,
    saved_sigint: Option<libc::sigaction>,
    saved_sigquit: Option<libc::sigaction>,
}

impl Pager {
    /// Starts the configured pager on stdout, `None` with stdout untouched when
    /// paging is off or the pager fails to start.
    pub fn start() -> Option<Self> {
        let mut pager = command()?;

        io::stdout().flush().ok()?;

        pager.stdin(Stdio::piped());
        if env::var_os("LESS").is_none() {
            pager.env("LESS", DEFAULT_LESS);
        }
        if env::var_os("LV").is_none() {
            pager.env("LV", "-c");
        }

        let mut child = pager.spawn().ok()?;
        let stdin = child.stdin.take()?;

        // SAFETY: stdout stays open for the life of the process.
        let saved_stdout = unsafe { libc::fcntl(libc::STDOUT_FILENO, libc::F_DUPFD_CLOEXEC, 0) };
        if saved_stdout < 0 {
            drop(stdin);
            let _ = child.wait();
            return None;
        }

        let cols = display::terminal_width().unwrap_or(0);

        // SAFETY: both descriptors are open, the pipe end lives until this
        // block ends.
        let redirected = unsafe { libc::dup2(stdin.as_raw_fd(), libc::STDOUT_FILENO) };
        drop(stdin);
        if redirected < 0 {
            // SAFETY: closes the duplicate made above.
            unsafe { libc::close(saved_stdout) };
            let _ = child.wait();
            return None;
        }

        WIDTH.store(cols, Ordering::Relaxed);

        // The pager handles Ctrl-C itself, and this process must outlive it.
        Some(Self {
            child,
            saved_stdout,
            saved_sigint: ignore(libc::SIGINT),
            saved_sigquit: ignore(libc::SIGQUIT),
        })
    }
}

impl Drop for Pager {
    fn drop(&mut self) {
        let _ = io::stdout().flush();

        // SAFETY: restores the stdout duplicated at start, which closes the
        // last write end of the pipe and ends the pager input.
        unsafe {
            libc::dup2(self.saved_stdout, libc::STDOUT_FILENO);
            libc::close(self.saved_stdout);
        }
        WIDTH.store(0, Ordering::Relaxed);

        let _ = self.child.wait();

        restore(libc::SIGINT, self.saved_sigint);
        restore(libc::SIGQUIT, self.saved_sigquit);
    }
}

/// Ignores a signal, returning the action it had, `None` when that fails.
fn ignore(signal: libc::c_int) -> Option<libc::sigaction> {
    // SAFETY: an all-zero action with an empty mask is valid, and both
    // pointers refer to live locals.
    unsafe {
        let mut action: libc::sigaction = mem::zeroed();
        action.sa_sigaction = libc::SIG_IGN;
        let mut previous: libc::sigaction = mem::zeroed();

        (libc::sigaction(signal, &action, &mut previous) == 0).then_some(previous)
    }
}

/// Puts back an action saved when the signal was ignored.
fn restore(signal: libc::c_int, previous: Option<libc::sigaction>) {
    let Some(previous) = previous else {
        return;
    };

    // SAFETY: the action came from the same call, with its flags intact.
    unsafe {
        libc::sigaction(signal, &previous, ptr::null_mut());
    }
}

/// Returns `YANET_PAGER` or `PAGER` run through the shell, or `less` run
/// directly so that a missing one fails to start, `None` for empty or `cat`.
fn command() -> Option<Command> {
    let Ok(command) = env::var("YANET_PAGER").or_else(|_| env::var("PAGER")) else {
        return Some(Command::new(DEFAULT_PAGER));
    };
    let command = command.trim();
    if command.is_empty() || command == "cat" {
        return None;
    }

    let mut shell = Command::new("sh");
    shell.arg("-c").arg(command);

    Some(shell)
}
