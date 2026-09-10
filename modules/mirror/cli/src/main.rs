use std::path::PathBuf;

use clap::{CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use commonpb::serde_with;
use mirrorpb::{
    DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, UpdateConfigRequest,
    mirror_service_client::MirrorServiceClient,
};
use serde::{Deserializer, Serializer};
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{self, LayeredChannel, Service},
    completion, display,
    errors::Error,
    output, yaml,
};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod mirrorpb {
    tonic::include_proto!("modules.mirror.controlplane.mirrorpb.v1");
}

/// Manages mirror module configs.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub globals: GlobalArgs,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// Delete a config.
    Delete(DeleteCmd),
    /// Create or replace a config.
    Update(UpdateCmd),
    /// Show a config.
    Show(ShowCmd),
    /// List configs.
    List,
}

impl ModeCmd {
    fn action(&self) -> &'static str {
        match self {
            Self::Delete(..) => "delete",
            Self::Update(..) => "update",
            Self::Show(..) => "show",
            Self::List => "list",
        }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// The name of the module config to show.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// The name of the module config to delete.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// The name of the module config to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config: String,
    /// Path to the module config file: the update request in YAML.
    ///
    /// The file spells the wire request, exactly what the generic operator
    /// pushes and what `show` prints: rules with an `action`, `devices` as
    /// named objects, family-typed `sources4/6` and `destinations4/6`
    /// networks, a mode by its declared name in any case or by its number.
    /// An undeclared mode number, which `show` still prints raw, is
    /// refused. The `name` may be omitted, it is then taken from `--name`,
    /// and a file naming another config is refused.
    #[arg(value_name = "PATH")]
    pub file: PathBuf,
}

/// Serializes a mirror mode as its declared name, an undeclared number as
/// the number itself, so one unknown mode cannot hide the rest of a config.
fn serialize_mirror_mode<S: Serializer>(mode: &i32, serializer: S) -> Result<S::Ok, S::Error> {
    serde_with::declared_name(mode, serializer, mirrorpb::MirrorMode::as_str_name)
}

/// Deserializes a mirror mode from its declared name or number, a null
/// as NONE.
///
/// An undeclared value is refused here, because the service would
/// silently coerce it to NONE rather than reject it.
fn deserialize_mirror_mode<'de, D: Deserializer<'de>>(deserializer: D) -> Result<i32, D::Error> {
    serde_with::from_declared_name(deserializer, mirrorpb::MirrorMode::from_str_name)
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.mirror.controlplane.mirrorpb.v1.MirrorService";

fn client(channel: LayeredChannel) -> MirrorServiceClient<LayeredChannel> {
    MirrorServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

type MirrorService = Service<MirrorServiceClient<LayeredChannel>>;

async fn show_config(service: &mut MirrorService, cmd: ShowCmd) -> Result<(), Error> {
    let request = ShowConfigRequest { name: cmd.config_name.clone() };
    let response = service
        .unary_with(
            "show",
            request,
            service.not_found("show", &format!("config '{}'", cmd.config_name)),
            async |client, request| client.show_config(request).await,
        )
        .await?;

    output::data(
        || &response,
        || {
            // The document is printed even without rules, so a
            // redirected show yields a file update accepts, an
            // undeclared mode number excepted.
            print!(
                "{}",
                serde_yaml::to_string(&response).expect("mirror config YAML serialization must not fail")
            );

            if response.rules.is_empty() {
                output::empty_with_hint(
                    format_args!("No mirror rules found for '{}'.", cmd.config_name),
                    format_args!("create one with 'yanet-cli-mirror update --name <name> <path>'"),
                );
            }
        },
    );

    Ok(())
}

async fn list_configs(service: &mut MirrorService) -> Result<(), Error> {
    let response = service
        .unary("list", ListConfigsRequest {}, async |client, request| {
            client.list_configs(request).await
        })
        .await?;

    output::data(
        || &response.configs,
        || {
            display::print_names_with_hint(
                &response.configs,
                format_args!("No mirror configurations found."),
                format_args!("create one with 'yanet-cli-mirror update --name <name> <path>'"),
            )
        },
    );

    Ok(())
}

async fn delete_config(service: &mut MirrorService, cmd: DeleteCmd) -> Result<(), Error> {
    let request = DeleteConfigRequest { name: cmd.config.clone() };
    service
        .unary_with(
            "delete",
            request,
            service.not_found("delete", &format!("config '{}'", cmd.config)),
            async |client, request| client.delete_config(request).await,
        )
        .await?;

    output::success("delete", format_args!("Deleted config '{}'.", cmd.config));

    Ok(())
}

async fn update_config(service: &mut MirrorService, cmd: UpdateCmd, request: UpdateConfigRequest) -> Result<(), Error> {
    service
        .unary("update", request, async |client, request| {
            client.update_config(request).await
        })
        .await?;

    output::success("update", format_args!("Updated config '{}'.", cmd.config));

    Ok(())
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();

    // The update file is read and bound before the connection, so bad
    // local input fails deterministically with or without a reachable
    // gateway.
    let update = match &cmd.mode {
        ModeCmd::Update(update) => {
            let endpoint = client::resolve_label(&cmd.globals.connection, action)?;
            let mut request: UpdateConfigRequest = yaml::load_document(&update.file)
                .map_err(|err| Error::invalid_argument("update", endpoint.clone(), err.to_string()))?;
            yaml::bind_name(&mut request.name, &update.config)
                .map_err(|err| Error::invalid_argument("update", endpoint, err))?;
            Some(request)
        }
        _ => None,
    };

    let mut service = Service::connect_for(&cmd.globals.connection, action, SERVICE_NAME, client).await?;

    match cmd.mode {
        ModeCmd::Delete(cmd) => delete_config(&mut service, cmd).await,
        ModeCmd::Update(cmd) => {
            let request = update.expect("prepared for the update mode");
            update_config(&mut service, cmd, request).await
        }
        ModeCmd::Show(cmd) => show_config(&mut service, cmd).await,
        ModeCmd::List => list_configs(&mut service).await,
    }
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

/// Completion candidates for a `--name` argument: the mirror configs the
/// module currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, client, async move |mut client| {
        Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs)
    })
}

#[cfg(test)]
mod test {
    use commonpb::pb::{IPv4Network, IPv6Network};

    use super::*;

    #[test]
    fn test_shown_config_round_trips_into_the_update_request() {
        let shown = mirrorpb::ShowConfigResponse {
            name: "mirror0".to_string(),
            rules: vec![mirrorpb::Rule {
                action: Some(mirrorpb::Action {
                    target: "target-out".to_string(),
                    mode: mirrorpb::MirrorMode::Out as i32,
                    counter: "counter-out".to_string(),
                }),
                devices: vec![filterpb::pb::Device { name: "eth0".to_string() }],
                vlan_ranges: vec![filterpb::pb::VlanRange { from: 10, to: 20 }],
                sources4: vec!["10.0.0.0/8".parse::<IPv4Network>().unwrap()],
                // The mask hole sits exactly at the /64 boundary, which the
                // filter compiler accepts.
                sources6: vec!["2001:db8::/ffff:ffff:ffff:0:ffff::".parse::<IPv6Network>().unwrap()],
                destinations4: vec![],
                destinations6: vec![],
            }],
        };

        let yaml = serde_yaml::to_string(&shown).expect("shown config must serialize");
        let parsed: UpdateConfigRequest = serde_yaml::from_str(&yaml).expect("shown config must parse back");

        assert_eq!(shown.name, parsed.name);
        assert_eq!(shown.rules, parsed.rules);
    }

    #[test]
    fn test_null_fields_read_as_zero_values() {
        let yaml = "name: mirror0\nrules:\n  - action:\n      target: t\n      mode: OUT\n      counter: c\n    devices: null\n    sources4: null\n";

        let request: UpdateConfigRequest = serde_yaml::from_str(yaml).expect("null fields must load");

        assert_eq!(1, request.rules.len());
        assert!(request.rules[0].devices.is_empty());
        assert!(request.rules[0].sources4.is_empty());
    }

    #[test]
    fn test_mode_accepts_the_legacy_pascal_case_spelling() {
        let action: mirrorpb::Action = serde_yaml::from_str("mode: Out\n").expect("a legacy spelling must parse");

        assert_eq!(mirrorpb::MirrorMode::Out as i32, action.mode);
    }

    #[test]
    fn test_an_undeclared_mode_is_shown_but_refused_by_update() {
        let action = mirrorpb::Action {
            target: "t".to_string(),
            mode: 99,
            counter: String::new(),
        };

        assert!(
            serde_yaml::to_string(&action)
                .expect("must serialize")
                .contains("mode: 99")
        );
        assert!(serde_yaml::from_str::<mirrorpb::Action>("mode: 99\n").is_err());
    }
}
