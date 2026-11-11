use std::path::PathBuf;

use clap::{CommandFactory, Parser, Subcommand};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use nat64pb::{
    DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, UpdateConfigRequest,
    nat64_service_client::Nat64ServiceClient, nat64_service_server::SERVICE_NAME,
};
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{self, LayeredChannel, Service},
    completion, display,
    errors::Error,
    output, yaml,
};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod nat64pb {
    tonic::include_proto!("modules.nat64.controlplane.nat64pb.v1");
}

fn client(channel: LayeredChannel) -> Nat64ServiceClient<LayeredChannel> {
    Nat64ServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Zstd)
        .accept_compressed(CompressionEncoding::Zstd)
        .accept_compressed(CompressionEncoding::Gzip)
}

/// Manages nat64 module configs.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub globals: GlobalArgs,
}

#[derive(Debug, Clone, Subcommand)]
pub enum ModeCmd {
    /// List all NAT64 configurations.
    List,
    /// Show current configuration.
    Show(ShowConfigCmd),
    /// Create or replace a config.
    Update(UpdateConfigCmd),
    /// Delete a config.
    Delete(DeleteConfigCmd),
}

impl ModeCmd {
    fn action(&self) -> &'static str {
        match self {
            Self::List => "list",
            Self::Show(..) => "show",
            Self::Update(..) => "update",
            Self::Delete(..) => "delete",
        }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// The name of the config to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateConfigCmd {
    /// The name of the config to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
    /// Path to the module config file: the update request in YAML.
    ///
    /// The file spells the wire request, exactly what the generic operator
    /// pushes and what `show` prints: a `config` of `prefixes`, `mappings`
    /// that name their prefix by `prefix_index`, an `mtu` and the
    /// `drop_unknown_prefix` and `drop_unknown_mapping` flags. It replaces
    /// the whole config: what it leaves out is removed, an absent `mtu`
    /// taking the default. The `name` may be omitted, it is then taken from
    /// `--name`, and a file naming another config is refused.
    #[arg(value_name = "PATH")]
    pub file: PathBuf,
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteConfigCmd {
    /// The name of the config to delete.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
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
            yaml::bind_name(&mut request.name, &update.config_name)
                .map_err(|err| Error::invalid_argument("update", endpoint, err))?;
            Some(request)
        }
        _ => None,
    };

    let mut service = Service::connect_for(&cmd.globals.connection, action, SERVICE_NAME, client).await?;

    match cmd.mode {
        ModeCmd::List => list_configs(&mut service).await,
        ModeCmd::Show(cmd) => show_config(&mut service, cmd).await,
        ModeCmd::Update(cmd) => {
            let request = update.expect("prepared for the update mode");
            update_config(&mut service, cmd, request).await
        }
        ModeCmd::Delete(cmd) => delete_config(&mut service, cmd).await,
    }
}

type NAT64Service = Service<Nat64ServiceClient<LayeredChannel>>;

async fn list_configs(service: &mut NAT64Service) -> Result<(), Error> {
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
                format_args!("No NAT64 configurations found."),
                format_args!("create one with 'yanet-cli-nat64 update --name <name> <path>'"),
            )
        },
    );

    Ok(())
}

async fn show_config(service: &mut NAT64Service, cmd: ShowConfigCmd) -> Result<(), Error> {
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
            // The document is printed even without prefixes, so a
            // redirected show yields a file update accepts.
            print!(
                "{}",
                serde_yaml::to_string(&response).expect("nat64 config YAML serialization must not fail")
            );

            if response.config.as_ref().is_none_or(|config| config.prefixes.is_empty()) {
                output::empty_with_hint(
                    format_args!("No NAT64 prefixes found for '{}'.", cmd.config_name),
                    format_args!("create one with 'yanet-cli-nat64 update --name <name> <path>'"),
                );
            }
        },
    );

    Ok(())
}

async fn update_config(
    service: &mut NAT64Service,
    cmd: UpdateConfigCmd,
    request: UpdateConfigRequest,
) -> Result<(), Error> {
    service
        .unary("update", request, async |client, request| {
            client.update_config(request).await
        })
        .await?;

    output::success("update", format_args!("Updated config '{}'.", cmd.config_name));

    Ok(())
}

async fn delete_config(service: &mut NAT64Service, cmd: DeleteConfigCmd) -> Result<(), Error> {
    let request = DeleteConfigRequest { name: cmd.config_name.clone() };
    service
        .unary_with(
            "delete",
            request,
            service.not_found("delete", &format!("config '{}'", cmd.config_name)),
            async |client, request| client.delete_config(request).await,
        )
        .await?;

    output::success("delete", format_args!("Deleted config '{}'.", cmd.config_name));

    Ok(())
}

fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, client, async move |mut client| {
        Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs)
    })
}

#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn test_shown_config_round_trips_into_the_update_request() {
        let shown = nat64pb::ShowConfigResponse {
            config: Some(nat64pb::Config {
                prefixes: vec!["64:ff9b::/96".parse().expect("valid prefix in test fixture")],
                mappings: vec![nat64pb::Mapping {
                    ipv4: Some("192.0.2.1".parse().expect("valid IPv4 address in test fixture")),
                    ipv6: Some("2001:db8::1".parse().expect("valid IPv6 address in test fixture")),
                    prefix_index: 0,
                }],
                mtu: Some(nat64pb::MtuConfig { ipv4_mtu: 1450, ipv6_mtu: 1280 }),
                drop_unknown_prefix: true,
                drop_unknown_mapping: false,
            }),
        };

        let yaml = serde_yaml::to_string(&shown).expect("shown config must serialize");
        let parsed: UpdateConfigRequest = serde_yaml::from_str(&yaml).expect("shown config must parse back");

        assert_eq!(shown.config, parsed.config);
    }

    #[test]
    fn test_file_ignores_an_unknown_config_key() {
        let yaml = "name: nat64\nconfig:\n  future:\n    - 64:ff9b::/96\n";

        let parsed: UpdateConfigRequest = serde_yaml::from_str(yaml).unwrap();

        assert_eq!("nat64", parsed.name);
        assert_eq!(Some(Default::default()), parsed.config);
    }
}
