/// Restore Unix's default handling so a closed output pipe ends the CLI
/// without a printing panic.
#[cfg(unix)]
pub fn init() {
    // SAFETY: both values are valid arguments for the Unix signal API.
    let previous = unsafe { libc::signal(libc::SIGPIPE, libc::SIG_DFL) };
    assert_ne!(previous, libc::SIG_ERR, "failed to set SIGPIPE handler");
}

#[cfg(not(unix))]
pub fn init() {}
