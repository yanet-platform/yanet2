// The dispatcher shares one dependency list with the library, so the
// dependencies only the library reaches are declared for this binary too.
#![allow(unused_crate_dependencies)]

use std::{collections::HashSet, path::PathBuf, sync::LazyLock};

use clap::{ArgMatches, Args as _, Command, FromArgMatches, crate_name, parser::ValueSource};
use colored::{ColoredString, Colorize};
use serde::Serialize;
use tonic::{Status, codec::CompressionEncoding};
use yanet_cli::{
    GlobalArgs,
    auth::AuthMethod,
    client::{ConnectionArgs, Service},
    config::{self, Origin, Settings},
    dispatcher::{self, Dispatch, Namespace},
    errors::Error,
    init, output,
};
use ynpb::pb::{IntrospectTokenRequest, Principal, auth_service_client::AuthServiceClient};

static ERROR: LazyLock<ColoredString> = LazyLock::new(|| "error".bold().bright_red());

/// The fully-qualified gRPC service name used in error messages.
const AUTH_SERVICE_NAME: &str = "controlplane.ynpb.v1.AuthService";

const NAMESPACES: &[Namespace] = &[
    Namespace {
        name: "device",
        about: "YANET device command dispatcher",
    },
    Namespace {
        name: "operator",
        about: "YANET operator command dispatcher",
    },
];

/// A resolved connection key: its `clap` argument id, long flag and
/// environment variable, used to both build the `config show` command and
/// to describe where a value came from.
struct KeyInfo {
    id: &'static str,
    flag: &'static str,
    variable: &'static str,
}

const KEYS: &[KeyInfo] = &[
    KeyInfo {
        id: "endpoint",
        flag: "--endpoint",
        variable: "YANET_ENDPOINT",
    },
    KeyInfo {
        id: "auth",
        flag: "--auth",
        variable: "YANET_AUTH",
    },
    KeyInfo {
        id: "cert_tag",
        flag: "--cert-tag",
        variable: "YANET_CERT_TAG",
    },
    KeyInfo {
        id: "ca",
        flag: "--ca",
        variable: "YANET_CA",
    },
    KeyInfo {
        id: "client_cert",
        flag: "--client-cert",
        variable: "YANET_CLIENT_CERT",
    },
    KeyInfo {
        id: "client_key",
        flag: "--client-key",
        variable: "YANET_CLIENT_KEY",
    },
    KeyInfo {
        id: "timeout",
        flag: "--timeout",
        variable: "YANET_TIMEOUT",
    },
];

fn key_info(id: &str) -> &'static KeyInfo {
    KEYS.iter()
        .find(|key| key.id == id)
        .unwrap_or_else(|| panic!("no KeyInfo registered for connection key \"{id}\""))
}

fn main() {
    dispatcher::dispatch(crate_name!(), "yanet-cli-", &Dispatcher);
}

struct Dispatcher;

impl Dispatch for Dispatcher {
    fn cmd(&self, modules: &HashSet<String>) -> Command {
        let cmd = Command::new(crate_name!())
            .version(yanet_cli::version())
            .allow_external_subcommands(true)
            .subcommand(config_command())
            .subcommand(auth_command());

        dispatcher::add_subcommands(cmd, modules)
    }

    fn try_match(&self, matches: &ArgMatches) -> Option<i32> {
        if let Some(show_matches) = matches
            .subcommand_matches("config")
            .and_then(|config| config.subcommand_matches("show"))
        {
            return Some(run_config_show(show_matches));
        }

        let whoami_matches = matches.subcommand_matches("auth")?.subcommand_matches("whoami")?;

        Some(run_auth_whoami(whoami_matches))
    }

    fn on_empty_subcommand(&self, modules: &HashSet<String>) -> i32 {
        let mut all = modules.clone();
        for ns in NAMESPACES {
            all.insert(ns.name.to_string());
        }
        print_empty_message(None, &all);
        1
    }

    fn on_empty_namespace(&self, namespace: &str, modules: &HashSet<String>) -> i32 {
        print_empty_message(Some(namespace), modules);
        1
    }

    fn on_sub_binary_not_found(&self, subcommand: &str, modules: &HashSet<String>) {
        print_module_not_found_message(subcommand, modules);
    }

    fn namespaces(&self) -> &[Namespace] {
        NAMESPACES
    }

    fn reserved(&self) -> &[&'static str] {
        &["config", "auth"]
    }
}

/// Builds the `config show` subcommand tree.
///
/// `show` embeds [`GlobalArgs`] directly (rather than a dedicated `Cmd`) so
/// its flags, environment variables and completion candidates stay
/// identical to every other command's.
fn config_command() -> Command {
    const SHOW_ABOUT: &str = "Prints the effective connection settings and where each came from.";
    const SHOW_LONG_ABOUT: &str = "Prints the effective connection settings and where each came \
        from. Reads /etc/yanet2/cli.yaml merged with $XDG_CONFIG_HOME/yanet2/cli.yaml \
        (~/.config by default), or the single file named by YANET_CONFIG.";

    let show = GlobalArgs::augment_args(Command::new("show"))
        .about(SHOW_ABOUT)
        .long_about(SHOW_LONG_ABOUT);

    Command::new("config")
        .about("Configuration file inspection.")
        .subcommand_required(true)
        .subcommand(show)
}

/// Runs `config show`: resolves the connection settings and reports them
/// through the shared output backend, exactly like any other command.
fn run_config_show(matches: &ArgMatches) -> i32 {
    let globals =
        GlobalArgs::from_arg_matches(matches).expect("show's own augmented matches must parse into GlobalArgs");
    let (verbose, format) = globals.options();
    let connection = globals.connection;

    init(verbose, format);

    match config::resolve(&connection, &config::Sources::from_process_env()) {
        Ok(settings) => {
            output::data(
                || show_payload(&settings, matches),
                || print_settings(&settings, matches),
            );
            0
        }
        Err(err) => {
            let error = Error::from_config(err, "show", connection.endpoint.clone());

            output::failure(&error);
            error.exit_code()
        }
    }
}

/// Builds the `auth whoami` subcommand tree.
///
/// `whoami` embeds [`GlobalArgs`] like `config show`, so the very flags that
/// select a token or a certificate are the ones it reports on.
fn auth_command() -> Command {
    const WHOAMI_ABOUT: &str = "Prints the identity the gateway resolves for the current connection settings.";
    const WHOAMI_LONG_ABOUT: &str = "Prints the identity the gateway resolves for the current connection \
        settings: the token of --auth when one is sent, else the client certificate of --client-cert, \
        else anonymous.";

    let whoami = GlobalArgs::augment_args(Command::new("whoami"))
        .about(WHOAMI_ABOUT)
        .long_about(WHOAMI_LONG_ABOUT);

    Command::new("auth")
        .about("Authentication inspection.")
        .subcommand_required(true)
        .subcommand(whoami)
}

/// Runs `auth whoami`: asks the gateway to introspect the call's own
/// credential and reports the principal through the shared output backend.
fn run_auth_whoami(matches: &ArgMatches) -> i32 {
    let globals =
        GlobalArgs::from_arg_matches(matches).expect("whoami's own augmented matches must parse into GlobalArgs");
    let (verbose, format) = globals.options();
    let connection = globals.connection;

    init(verbose, format);

    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .expect("failed to build the CLI runtime");

    match runtime.block_on(whoami(&connection)) {
        Ok((endpoint, principal)) => {
            output::data(|| &principal, || print_principal(&endpoint, &principal));
            0
        }
        Err(err) => {
            output::failure(&err);
            err.exit_code()
        }
    }
}

/// Introspects the call's own credential: an empty token makes the gateway
/// look at what the connection itself presents. Returns the endpoint reached
/// along with the principal, since who one is depends on where one asked.
async fn whoami(connection: &ConnectionArgs) -> Result<(String, Principal), Error> {
    let mut service = Service::connect_for(connection, "whoami", AUTH_SERVICE_NAME, |channel| {
        AuthServiceClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip)
    })
    .await?;

    let response = service
        .unary(
            "whoami",
            IntrospectTokenRequest { token: String::new() },
            async |client, request| client.introspect_token(request).await,
        )
        .await?;

    let principal = response.principal.ok_or_else(|| {
        Error::from_status(
            Status::internal("the gateway returned no principal"),
            "whoami",
            service.endpoint(),
            AUTH_SERVICE_NAME,
        )
    })?;

    Ok((service.endpoint().to_owned(), principal))
}

/// Width of the widest detail label (`Auth method`) plus two separating
/// spaces.
const DETAIL_WIDTH: usize = 13;

/// Renders a verdict line and its details in the mark style of `ready`: a
/// green mark for an authenticated caller, a yellow one for anonymous, the
/// details indented under the name.
fn print_principal(endpoint: &str, principal: &Principal) {
    let colored = output::is_colored();
    let (unicode_mark, ascii_mark, color): (&str, &str, fn(&str) -> String) = if principal.is_anonymous {
        ("[~]", "[!!]", |s| s.yellow().to_string())
    } else {
        ("[✓]", "[ok]", |s| s.green().to_string())
    };
    let mark = if colored {
        color(unicode_mark)
    } else {
        ascii_mark.to_owned()
    };
    let indent = " ".repeat(if colored { 4 } else { 5 });
    let user = if colored {
        principal.user.bold().to_string()
    } else {
        principal.user.clone()
    };

    println!("{mark} {user} at {endpoint}");
    if !principal.is_anonymous {
        print_detail(&indent, "Groups", &groups_str(&principal.groups));
    }
    print_detail(&indent, "Auth method", &auth_method_str(&principal.auth_method));
    if principal.is_anonymous {
        let hint = if colored {
            "hint".bright_green().to_string()
        } else {
            "hint".to_owned()
        };

        println!(
            "{indent}{hint}: pass --auth sshcert with --cert-tag, or --client-cert with --client-key, to authenticate"
        );
    }
}

fn print_detail(indent: &str, label: &str, value: &str) {
    println!("{indent}{}{value}", output::dim(&format!("{label:<DETAIL_WIDTH$}")));
}

/// Names the credential behind an authentication method after its wire
/// name, so the line reads without knowing the authenticator names.
fn auth_method_str(method: &str) -> String {
    match method {
        "x509" => "x509, client certificate".to_owned(),
        "sshcert" => "sshcert, SSH certificate token".to_owned(),
        "sshkey" => "sshkey, SSH key token".to_owned(),
        "basic" => "basic, password token".to_owned(),
        other => other.to_owned(),
    }
}

fn groups_str(groups: &[String]) -> String {
    if groups.is_empty() {
        "-".to_owned()
    } else {
        groups.join(", ")
    }
}

/// Where a resolved value came from, refined for display: [`Origin::Argument`]
/// is split into `flag` and `environment` using the `show` command's own
/// `ArgMatches::value_source`, since [`Origin`] itself does not distinguish
/// them.
struct OriginView {
    kind: &'static str,
    path: Option<String>,
    alias: Option<String>,
    variable: Option<&'static str>,
    label: String,
}

/// Pure: never reads the process environment itself. The caller reads
/// clap's own value source for the key and hands it in as `value_source`.
fn describe_origin(origin: &Origin, value_source: Option<ValueSource>, key: &'static KeyInfo) -> OriginView {
    match origin {
        Origin::Argument => match value_source {
            Some(ValueSource::EnvVariable) => OriginView {
                kind: "environment",
                path: None,
                alias: None,
                variable: Some(key.variable),
                label: format!("environment {}", key.variable),
            },
            _ => OriginView {
                kind: "flag",
                path: None,
                alias: None,
                variable: None,
                label: format!("flag {}", key.flag),
            },
        },
        Origin::Section { path, alias } => {
            let path = path.display().to_string();

            OriginView {
                kind: "alias",
                label: format!("alias {alias} in {path}"),
                path: Some(path),
                alias: Some(alias.clone()),
                variable: None,
            }
        }
        Origin::File(path) => {
            let path = path.display().to_string();

            OriginView {
                kind: "file",
                label: path.clone(),
                path: Some(path),
                alias: None,
                variable: None,
            }
        }
        Origin::Ignored { path, alias } => {
            let path = path.display().to_string();
            let label = match alias {
                Some(alias) => format!("ignored for a plaintext endpoint, set in alias {alias} in {path}"),
                None => format!("ignored for a plaintext endpoint, set in {path}"),
            };

            OriginView {
                kind: "ignored",
                label,
                path: Some(path),
                alias: alias.clone(),
                variable: None,
            }
        }
        Origin::BuiltIn => OriginView {
            kind: "built-in",
            path: None,
            alias: None,
            variable: None,
            label: "built-in".to_owned(),
        },
    }
}

/// Widest connection key label (`client_cert`) plus two separating spaces.
const LABEL_WIDTH: usize = 13;

fn print_settings(settings: &Settings, matches: &ArgMatches) {
    print_endpoint_line(settings, matches);
    print_optional_line(
        "auth",
        Some(auth_str(settings.auth.value).to_owned()),
        &settings.auth.origin,
        matches,
    );
    print_optional_line(
        "cert_tag",
        settings.cert_tag.value.clone(),
        &settings.cert_tag.origin,
        matches,
    );
    print_optional_line("ca", path_str(&settings.ca.value), &settings.ca.origin, matches);
    print_optional_line(
        "client_cert",
        path_str(&settings.client_cert.value),
        &settings.client_cert.origin,
        matches,
    );
    print_optional_line(
        "client_key",
        path_str(&settings.client_key.value),
        &settings.client_key.origin,
        matches,
    );
    print_optional_line(
        "timeout",
        settings.timeout.value.map(|d| format!("{}s", d.as_secs_f64())),
        &settings.timeout.origin,
        matches,
    );

    println!();
    println!("files:");
    for file in &settings.files {
        let state = if file.read { "read  " } else { "absent" };

        println!("  {state}  {}", file.path.display());
    }
}

/// Prints the `endpoint` line, the one key whose selection (the alias name)
/// can carry its own origin distinct from the URI's.
fn print_endpoint_line(settings: &Settings, matches: &ArgMatches) {
    let key = key_info("endpoint");
    let source = matches.value_source(key.id);
    let view = describe_origin(&settings.endpoint.origin, source, key);

    let annotation = match &settings.alias {
        Some(alias) => {
            let selected_by = describe_origin(&alias.origin, source, key);

            format!("({}, selected by {})", view.label, selected_by.label)
        }
        None => format!("({})", view.label),
    };

    println!(
        "{:<LABEL_WIDTH$}{}  {}",
        key.id,
        settings.endpoint.value,
        output::dim(&annotation)
    );
}

fn print_line(id: &'static str, value: &str, origin: &Origin, matches: &ArgMatches) {
    let key = key_info(id);
    let view = describe_origin(origin, matches.value_source(key.id), key);
    let annotation = output::dim(&format!("({})", view.label));

    println!("{id:<LABEL_WIDTH$}{value}  {annotation}");
}

fn print_optional_line(id: &'static str, value: Option<String>, origin: &Origin, matches: &ArgMatches) {
    print_line(id, value.as_deref().unwrap_or("-"), origin, matches);
}

fn auth_str(auth: AuthMethod) -> &'static str {
    match auth {
        AuthMethod::None => "none",
        AuthMethod::Sshcert => "sshcert",
    }
}

fn path_str(path: &Option<PathBuf>) -> Option<String> {
    path.as_ref().map(|p| p.display().to_string())
}

#[derive(Serialize)]
struct OriginJson {
    kind: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    path: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    alias: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    variable: Option<&'static str>,
}

impl From<OriginView> for OriginJson {
    fn from(view: OriginView) -> Self {
        Self {
            kind: view.kind,
            path: view.path,
            alias: view.alias,
            variable: view.variable,
        }
    }
}

#[derive(Serialize)]
struct EntryJson<T: Serialize> {
    value: Option<T>,
    origin: OriginJson,
}

#[derive(Serialize)]
struct EndpointEntryJson {
    value: String,
    alias: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    selected_by: Option<OriginJson>,
    origin: OriginJson,
}

#[derive(Serialize)]
struct FileJson {
    path: String,
    read: bool,
}

#[derive(Serialize)]
struct ShowJson {
    endpoint: EndpointEntryJson,
    auth: EntryJson<String>,
    cert_tag: EntryJson<String>,
    ca: EntryJson<String>,
    client_cert: EntryJson<String>,
    client_key: EntryJson<String>,
    timeout: EntryJson<f64>,
    files: Vec<FileJson>,
}

fn show_payload(settings: &Settings, matches: &ArgMatches) -> ShowJson {
    let entry = |id: &'static str, value: Option<String>, origin: &Origin| {
        let key = key_info(id);

        EntryJson {
            value,
            origin: describe_origin(origin, matches.value_source(key.id), key).into(),
        }
    };

    let endpoint_key = key_info("endpoint");
    let endpoint_source = matches.value_source(endpoint_key.id);

    ShowJson {
        endpoint: EndpointEntryJson {
            value: settings.endpoint.value.clone(),
            alias: settings.alias.as_ref().map(|alias| alias.value.clone()),
            selected_by: settings
                .alias
                .as_ref()
                .map(|alias| describe_origin(&alias.origin, endpoint_source, endpoint_key).into()),
            origin: describe_origin(&settings.endpoint.origin, endpoint_source, endpoint_key).into(),
        },
        auth: entry(
            "auth",
            Some(auth_str(settings.auth.value).to_owned()),
            &settings.auth.origin,
        ),
        cert_tag: entry("cert_tag", settings.cert_tag.value.clone(), &settings.cert_tag.origin),
        ca: entry("ca", path_str(&settings.ca.value), &settings.ca.origin),
        client_cert: entry(
            "client_cert",
            path_str(&settings.client_cert.value),
            &settings.client_cert.origin,
        ),
        client_key: entry(
            "client_key",
            path_str(&settings.client_key.value),
            &settings.client_key.origin,
        ),
        timeout: EntryJson {
            value: settings.timeout.value.map(|d| d.as_secs_f64()),
            origin: describe_origin(
                &settings.timeout.origin,
                matches.value_source("timeout"),
                key_info("timeout"),
            )
            .into(),
        },
        files: settings
            .files
            .iter()
            .map(|file| FileJson {
                path: file.path.display().to_string(),
                read: file.read,
            })
            .collect(),
    }
}

fn print_empty_message(namespace: Option<&str>, modules: &HashSet<String>) {
    let infix = namespace.map(|ns| format!("{ns} ")).unwrap_or_default();
    eprintln!("{}: no {infix}module specified", *ERROR);
    eprintln!();
    eprintln!("{}: {} {infix}<module>", "Usage".underline().bold(), crate_name!());
    eprintln!();
    print_available_modules_message(namespace, modules);
}

fn print_available_modules_message(namespace: Option<&str>, modules: &HashSet<String>) {
    let kind = namespace.map(|ns| format!("{ns} ")).unwrap_or_default();

    if modules.is_empty() {
        eprintln!(
            "{}: {}",
            "hint".bright_green(),
            format!("no {kind}modules found on PATH").yellow()
        );
        return;
    }

    let mut modules = modules
        .iter()
        .map(|m| m.as_str().yellow().to_string())
        .collect::<Vec<_>>();
    modules.sort();

    eprintln!(
        "{}: available {kind}modules: {}",
        "hint".bright_green(),
        modules.iter().as_slice().join(", ")
    );
}

fn print_module_not_found_message(subcommand: &str, modules: &HashSet<String>) {
    let cmd = subcommand.replace("yanet-cli-", "");

    eprintln!("{}: module '{}' not found", *ERROR, cmd.yellow());
    eprintln!();
    eprintln!(
        "{}: binary '{}' is not found in any of paths described in '{}' environment variable",
        "hint".bright_green(),
        subcommand.yellow(),
        "PATH".yellow()
    );

    print_available_modules_message(None, modules);
}

#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn test_describe_origin_argument_from_command_line_is_flag() {
        let view = describe_origin(&Origin::Argument, Some(ValueSource::CommandLine), key_info("endpoint"));

        assert_eq!("flag", view.kind);
        assert_eq!(None, view.variable);
    }

    #[test]
    fn test_describe_origin_argument_from_environment_is_environment() {
        let view = describe_origin(&Origin::Argument, Some(ValueSource::EnvVariable), key_info("endpoint"));

        assert_eq!("environment", view.kind);
        assert_eq!(Some("YANET_ENDPOINT"), view.variable);
    }

    #[test]
    fn test_describe_origin_section_names_alias_and_path() {
        let origin = Origin::Section {
            path: "/home/x/.config/yanet2/cli.yaml".into(),
            alias: "m9-r1".to_owned(),
        };
        let view = describe_origin(&origin, None, key_info("endpoint"));

        assert_eq!("alias", view.kind);
        assert_eq!(Some("m9-r1".to_owned()), view.alias);
        assert_eq!(Some("/home/x/.config/yanet2/cli.yaml".to_owned()), view.path);
    }

    #[test]
    fn test_describe_origin_file_is_the_bare_path() {
        let origin = Origin::File("/etc/yanet2/cli.yaml".into());
        let view = describe_origin(&origin, None, key_info("endpoint"));

        assert_eq!("file", view.kind);
        assert_eq!(Some("/etc/yanet2/cli.yaml".to_owned()), view.path);
        assert_eq!(None, view.alias);
    }

    #[test]
    fn test_describe_origin_ignored_names_the_plaintext_reason_and_path() {
        let origin = Origin::Ignored {
            path: "/etc/yanet2/cli.yaml".into(),
            alias: Some("lab".to_owned()),
        };
        let view = describe_origin(&origin, None, key_info("ca"));

        assert_eq!("ignored", view.kind);
        assert_eq!(Some("/etc/yanet2/cli.yaml".to_owned()), view.path);
        assert_eq!(Some("lab".to_owned()), view.alias);
    }

    #[test]
    fn test_describe_origin_built_in_has_no_path_or_alias() {
        let view = describe_origin(&Origin::BuiltIn, None, key_info("endpoint"));

        assert_eq!("built-in", view.kind);
        assert_eq!(None, view.path);
        assert_eq!(None, view.alias);
    }

    #[test]
    fn test_auth_str_matches_the_wire_value_names() {
        assert_eq!("none", auth_str(AuthMethod::None));
        assert_eq!("sshcert", auth_str(AuthMethod::Sshcert));
    }

    /// A `KEYS` entry naming an argument `show` no longer has (e.g. after a
    /// rename) must fail the suite here rather than panic in `key_info` at
    /// runtime.
    #[test]
    fn test_every_key_id_is_a_show_argument() {
        let show = config_command()
            .find_subcommand("show")
            .expect("config_command always registers show")
            .clone();
        let ids: HashSet<&str> = show.get_arguments().map(|arg| arg.get_id().as_str()).collect();

        for key in KEYS {
            assert!(
                ids.contains(key.id),
                "KEYS entry \"{}\" is not a `show` argument",
                key.id
            );
        }
    }
}
