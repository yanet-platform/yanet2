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
/// `command` is the caller's own `Cmd::command`, exactly the factory passed
/// to `CompleteEnv::with_factory` in `main` — it recovers the connection
/// flags (`--endpoint`, `--auth`, …) the user has actually typed so far, so
/// completion talks to the gateway the user is targeting rather than
/// silently falling back to the default one. `build` receives the layered
/// channel and returns the caller's tonic-generated client, exactly like
/// the `build` parameter of [`Service::new`](crate::client::Service::new) —
/// compression is configured there, since it is an inherent client method.
/// `lookup` issues the RPC on that client and returns the config names.
///
/// Strictly best-effort: a gateway that is down, slow, or refusing auth
/// yields an empty list, never an error or a hang. Connect and lookup share
/// one [`DISCOVERY_TIMEOUT`] budget, and the whole call runs on its own
/// single-threaded runtime built and torn down here — nothing may reach
/// stdout, since `clap_complete` owns it.
///
/// The caller must invoke `CompleteEnv::complete` from a sync `main`, before
/// any runtime is entered, because the runtime built here is blocked on
/// directly, which panics if it is reached from inside another runtime's
/// context.
///
/// `lookup` must be a genuine async closure, `async move |client| …`, not a
/// plain closure returning an `async move` block — the latter cannot infer
/// `client`'s type and fails to compile.
///
/// Do not attach these candidates to an argument that uses clap's
/// `value_delimiter`, such as a comma-separated list like pipeline's
/// `--functions acl,route`. `ArgValueCandidates` completes the whole raw
/// token before delimiter splitting, so a candidate would try to replace the
/// entire typed token rather than just its trailing element, producing a
/// wrong insertion — completing a delimited list needs delimiter-aware
/// candidates, which this helper does not provide.
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

    fn recovered_endpoint(argv: &[&str]) -> String {
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

        assert_eq!("grpc://example:1", endpoint);
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

        assert_eq!("grpc://example:1", endpoint);
    }

    #[test]
    fn falls_back_to_defaults_without_a_separator() {
        let fallback = default_connection_args().endpoint;

        assert_eq!(fallback, recovered_endpoint(&["completer", "show", "--name", ""]));
    }
}
