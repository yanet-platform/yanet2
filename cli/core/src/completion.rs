//! Best-effort shell completion for config-name arguments.
//!
//! A module CLI's config-name argument (`--name`, `--config`, …) is
//! completed from the module's own `list` RPC. The caller supplies its own
//! generated client and its own generated method through two closures, so a
//! proto rename breaks the build instead of silently degrading completion
//! into a no-op — this is the whole point of [`candidates`]'s shape.

use std::ffi::OsString;

use clap::{Args, Command, FromArgMatches};
use clap_complete::engine::CompletionCandidate;
use tonic::Status;

use crate::{
    client::{self, ConnectionArgs, LayeredChannel},
    discovery::DISCOVERY_TIMEOUT,
};

/// Extracts the words the user has typed so far from the completer's raw
/// argv.
///
/// `clap_complete` invokes the completer as `<completer> -- <bin>
/// <word>...`; this mirrors `clap_complete::CompleteEnv::try_complete`'s own
/// extraction verbatim. Drops `argv[0]` — the completer's own path — then
/// drops everything through the FIRST `--`. What remains is `[bin_name,
/// ...user words...]`, exactly the argv clap itself parses. Returns an
/// empty vector if `args` is empty or contains no `--`, since neither shape
/// carries any words to recover.
fn user_words<I>(args: I) -> Vec<OsString>
where
    I: IntoIterator<Item = OsString>,
{
    let mut args: Vec<OsString> = args.into_iter().collect();
    if args.is_empty() {
        return args;
    }

    args.remove(0);
    let escape_index = args
        .iter()
        .position(|a| *a == "--")
        .map(|i| i + 1)
        .unwrap_or(args.len());
    args.drain(0..escape_index);

    args
}

/// Recovers the connection flags from the given completer argv.
///
/// Parses `user_words(args)` against the caller's own `command`, with parse
/// errors ignored — the line is mid-typing and usually not yet valid.
/// Falls back to [`default_connection_args`] whenever recovery is
/// impossible: no `--` in `args`, an unparsable line, or a `Cmd` that does
/// not flatten [`ConnectionArgs`] at all.
fn recover_connection_args<I>(command: impl FnOnce() -> Command, args: I) -> ConnectionArgs
where
    I: IntoIterator<Item = OsString>,
{
    command()
        .ignore_errors(true)
        .try_get_matches_from(user_words(args))
        .ok()
        .and_then(|matches| ConnectionArgs::from_arg_matches(&matches).ok())
        .unwrap_or_else(default_connection_args)
}

/// [`ConnectionArgs`]' own defaults, independent of any `Cmd` — the
/// fallback when recovery from the caller's command tree is impossible.
///
/// `YANET_ENDPOINT` is still honored, since it is the `endpoint` field's
/// `env` attribute rather than anything specific to the caller's `Cmd`.
fn default_connection_args() -> ConnectionArgs {
    let matches = ConnectionArgs::augment_args(Command::new("connection")).get_matches_from(Vec::<OsString>::new());

    ConnectionArgs::from_arg_matches(&matches).expect("ConnectionArgs must parse from its own defaults")
}

/// Recovers the connection flags a completer's user has typed so far.
///
/// A completer probing something other than a config-name list — a service
/// registry, say — still needs to reach the gateway the user is targeting,
/// not the default one. It supplies its own `Cmd::command`, and the flags
/// are parsed out of the real completer argv, falling back to their defaults
/// (`YANET_ENDPOINT` included) when the mid-typed line cannot be parsed.
pub fn connection_args(command: impl FnOnce() -> Command) -> ConnectionArgs {
    recover_connection_args(command, std::env::args_os())
}

/// Best-effort completion candidates for a config-name argument.
///
/// The parser factory recovers connection flags from the partially typed
/// command line, including its target endpoint. A slow or unavailable gateway
/// yields an empty list within one fixed budget and writes nothing to stdout.
/// Lookup runs on a temporary single-threaded runtime, so completion must
/// execute before any command runtime. [`crate::entrypoint`] guarantees that
/// ordering; direct lifecycle callers must preserve it. The lookup callback
/// must be a direct async closure for type inference. Delimited arguments are
/// unsupported because completion replaces the entire raw token.
pub fn candidates<C, B, F>(command: impl FnOnce() -> Command, build: B, lookup: F) -> Vec<CompletionCandidate>
where
    B: FnOnce(LayeredChannel) -> C,
    F: AsyncFnOnce(C) -> Result<Vec<String>, Status>,
{
    let connection = connection_args(command);

    let Ok(runtime) = tokio::runtime::Builder::new_current_thread().enable_all().build() else {
        return Vec::new();
    };

    let attempt = async move {
        let channel = client::connect(&connection).await.ok()?;

        lookup(build(channel)).await.ok()
    };

    // `tokio::time::timeout` captures the current runtime's timer handle as
    // soon as it is constructed, not once polled, so it must be built inside
    // the `async` block handed to `block_on` rather than before it.
    runtime
        .block_on(async move { tokio::time::timeout(DISCOVERY_TIMEOUT, attempt).await })
        .ok()
        .flatten()
        .unwrap_or_default()
        .into_iter()
        .map(CompletionCandidate::new)
        .collect()
}

#[cfg(test)]
mod test {
    use clap::{CommandFactory, Parser};

    use super::*;

    fn words(raw: &[&str]) -> Vec<OsString> {
        raw.iter().map(OsString::from).collect()
    }

    #[test]
    fn user_words_is_empty_for_empty_argv() {
        assert!(user_words(Vec::new()).is_empty());
    }

    #[test]
    fn user_words_is_empty_without_a_separator() {
        let argv = words(&["completer", "show", "--name", ""]);

        assert!(user_words(argv).is_empty());
    }

    #[test]
    fn user_words_strips_through_the_first_separator() {
        let argv = words(&["completer", "--", "bin", "show", "--name", ""]);

        assert_eq!(words(&["bin", "show", "--name", ""]), user_words(argv));
    }

    #[test]
    fn user_words_uses_the_first_separator_when_user_words_also_contain_one() {
        let argv = words(&["completer", "--", "bin", "run", "--", "--name", ""]);

        assert_eq!(words(&["bin", "run", "--", "--name", ""]), user_words(argv));
    }

    /// A faithful replica of a module CLI's `Cmd`: a required subcommand
    /// plus globally flattened [`ConnectionArgs`], the exact shape that
    /// made a `ConnectionArgs`-only wrapper miss a global flag typed after
    /// the subcommand.
    #[derive(Parser)]
    struct TestCmd {
        #[command(subcommand)]
        mode: TestModeCmd,
        #[command(flatten)]
        connection: ConnectionArgs,
    }

    #[derive(Parser)]
    enum TestModeCmd {
        Show(TestShowCmd),
    }

    #[derive(Parser)]
    struct TestShowCmd {
        #[arg(long = "name", short = 'n')]
        name: String,
    }

    fn recovered_endpoint(argv: &[&str]) -> Option<String> {
        recover_connection_args(TestCmd::command, words(argv)).endpoint
    }

    #[test]
    fn recovers_endpoint_given_before_the_subcommand() {
        let endpoint = recovered_endpoint(&[
            "completer",
            "--",
            "bin",
            "--endpoint",
            "grpc://example:1",
            "show",
            "--name",
            "",
        ]);

        assert_eq!(Some("grpc://example:1".to_owned()), endpoint);
    }

    #[test]
    fn recovers_endpoint_given_after_the_subcommand() {
        let endpoint = recovered_endpoint(&[
            "completer",
            "--",
            "bin",
            "show",
            "--endpoint",
            "grpc://example:1",
            "--name",
            "",
        ]);

        assert_eq!(Some("grpc://example:1".to_owned()), endpoint);
    }

    #[test]
    fn falls_back_to_defaults_without_a_separator() {
        // No `--` escape means there is no real argv to recover flags
        // from, so a typed `--endpoint` here is simply never seen — unlike
        // the same words placed after a separator, covered above. The
        // fallback is `default_connection_args()`, not `None`, since it
        // honours `YANET_ENDPOINT` when the process happens to export one.
        let recovered = recovered_endpoint(&["completer", "--endpoint", "grpc://typed:1", "show", "--name", ""]);

        assert_eq!(default_connection_args().endpoint, recovered);
        assert_ne!(Some("grpc://typed:1".to_owned()), recovered);
    }
}
