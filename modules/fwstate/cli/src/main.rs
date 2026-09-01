use core::net::IpAddr;

use args::{DeleteCmd, ModeCmd, ShowCmd, UpdateCmd};
use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::{CompleteEnv, engine::CompletionCandidate};
use commonpb::pb::IpAddress;
use fwstatepb::{
    DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, ShowConfigResponse, UpdateConfigRequest,
    fw_state_service_client::FwStateServiceClient,
};
use tonic::codec::CompressionEncoding;
use ync::{
    client::{Connection, ConnectionArgs, LayeredChannel, Service},
    completion,
    errors::Error,
    output::{self, CommonFormat},
};

mod args;

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod fwstatepb {
    use serde::Serialize;

    tonic::include_proto!("modules.fwstate.controlplane.fwstatepb.v1");
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.fwstate.controlplane.fwstatepb.v1.FWStateService";

/// FWState module CLI.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

/// Merges the linked map object names an update should carry.
///
/// The pre-flight lookup answers an unknown name with an empty message,
/// while a stored config always echoes the requested one — an empty name
/// in the reply therefore marks the create case. A create has no stored
/// names to merge from and the server rejects empty ones, so both
/// map-name flags are required then; otherwise each flag, when present,
/// overrides the stored value.
fn merged_map_names(current: &ShowConfigResponse, cmd: &UpdateCmd) -> Result<(String, String), String> {
    if current.name.is_empty() && (cmd.map_name_v4.is_none() || cmd.map_name_v6.is_none()) {
        return Err(format!(
            "creating config '{}' requires --map-name-v4 and --map-name-v6",
            cmd.config_name
        ));
    }
    Ok((
        cmd.map_name_v4.clone().unwrap_or_else(|| current.map_name_v4.clone()),
        cmd.map_name_v6.clone().unwrap_or_else(|| current.map_name_v6.clone()),
    ))
}

pub struct FWStateService {
    service: Service<FwStateServiceClient<LayeredChannel>>,
}

impl FWStateService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let conn = Connection::connect(connection).await?;
        let service = Service::new(&conn, SERVICE_NAME, |channel| {
            FwStateServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        });
        Ok(Self { service })
    }

    pub async fn list_configs(&mut self) -> Result<(), Error> {
        let request = ListConfigsRequest {};
        let response = self
            .service
            .client()
            .list_configs(request)
            .await
            .map_err(self.service.status("list"))?
            .into_inner();

        output::data(
            || &response.configs,
            || {
                if response.configs.is_empty() {
                    output::empty_with_hint(
                        format_args!("No FWState configurations found."),
                        format_args!(
                            "provision maps with 'yanet-cli-fwstatemap create --name <name> --kind <v4|v6>', then create a config with 'yanet-cli-fwstate update --name <name> --map-name-v4 <map> --map-name-v6 <map> --src-addr <addr> --dst-addr-multicast <addr> --port-multicast <port>'"
                        ),
                    );
                    return;
                }

                println!(
                    "{}",
                    serde_json::to_string_pretty(&response.configs)
                        .expect("fwstate config list JSON serialization must not fail")
                );
            },
        );

        Ok(())
    }

    pub async fn show_config(&mut self, cmd: ShowCmd) -> Result<(), Error> {
        let request = ShowConfigRequest {
            name: cmd.config_name.clone(),
            ok_if_not_found: false,
        };
        let response = self
            .service
            .client()
            .show_config(request)
            .await
            .map_err(self.service.status("show"))?
            .into_inner();

        output::data(
            || &response,
            || {
                println!(
                    "{}",
                    serde_json::to_string_pretty(&response).expect("fwstate config JSON serialization must not fail")
                );
            },
        );

        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Error> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };
        self.service
            .client()
            .delete_config(request)
            .await
            .map_err(self.service.status("delete"))?;

        output::success("delete", format_args!("Deleted fwstate config {}.", cmd.config_name));

        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Error> {
        // First, fetch the current config to merge with new values
        //
        // A failed read aborts the update: only an empty reply proves the
        // config is missing and may take the create path.
        let current_request = ShowConfigRequest {
            name: cmd.config_name.clone(),
            ok_if_not_found: true,
        };
        let current = self
            .service
            .client()
            .show_config(current_request)
            .await
            .map_err(self.service.status("update"))?
            .into_inner();
        let (map_name_v4, map_name_v6) =
            merged_map_names(&current, &cmd).map_err(|err| self.service.invalid("update", err))?;
        let mut sync_config = current.sync_config.unwrap_or_default();

        // Update only the fields that were provided
        if let Some(src_addr) = cmd.src_addr {
            sync_config.src_addr = Some(IpAddress::from(IpAddr::V6(src_addr)));
        }

        if let Some(dst_addr_multicast) = cmd.dst_addr_multicast {
            sync_config.dst_addr_multicast = Some(IpAddress::from(IpAddr::V6(dst_addr_multicast)));
        }

        if let Some(port_multicast) = cmd.port_multicast {
            sync_config.port_multicast = u32::from(port_multicast);
        }

        // Convert timeouts from Duration to nanoseconds if provided
        if let Some(tcp_syn_ack) = cmd.tcp_syn_ack {
            sync_config.tcp_syn_ack = tcp_syn_ack.as_nanos() as u64;
        }

        if let Some(tcp_syn) = cmd.tcp_syn {
            sync_config.tcp_syn = tcp_syn.as_nanos() as u64;
        }

        if let Some(tcp_fin) = cmd.tcp_fin {
            sync_config.tcp_fin = tcp_fin.as_nanos() as u64;
        }

        if let Some(tcp) = cmd.tcp {
            sync_config.tcp = tcp.as_nanos() as u64;
        }

        if let Some(udp) = cmd.udp {
            sync_config.udp = udp.as_nanos() as u64;
        }

        if let Some(default) = cmd.default {
            sync_config.default = default.as_nanos() as u64;
        }

        if let Some(suppress) = cmd.sync_suppress_timeout {
            sync_config.sync_suppress_timeout = suppress.as_nanos() as u64;
        }

        let request = UpdateConfigRequest {
            name: cmd.config_name.clone(),
            map_name_v4,
            map_name_v6,
            sync_config: Some(sync_config),
        };
        log::trace!("UpdateConfigRequest: {request:?}");
        self.service
            .client()
            .update_config(request)
            .await
            .map_err(self.service.status("update"))?;

        output::success("update", format_args!("Updated fwstate config {}.", cmd.config_name));

        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = FWStateService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
    }
}

fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();
    start();
}

#[tokio::main(flavor = "current_thread")]
async fn start() {
    let cmd = Cmd::parse();
    ync::init(cmd.verbose, cmd.format);

    if let Err(err) = run(cmd).await {
        output::failure(&err);
        std::process::exit(err.exit_code());
    }
}

/// Completion candidates for a `--name` argument: the fwstate configs the
/// module currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(
        Cmd::command,
        |channel| {
            FwStateServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        },
        async move |mut client| Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs),
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn cmd_is_valid() {
        Cmd::command().debug_assert();
    }

    /// Update command carrying only the config name and optional map names.
    fn update_cmd(config_name: &str, map_name_v4: Option<&str>, map_name_v6: Option<&str>) -> UpdateCmd {
        UpdateCmd {
            config_name: config_name.to_string(),
            map_name_v4: map_name_v4.map(str::to_string),
            map_name_v6: map_name_v6.map(str::to_string),
            src_addr: None,
            dst_addr_multicast: None,
            port_multicast: None,
            tcp_syn_ack: None,
            tcp_syn: None,
            tcp_fin: None,
            tcp: None,
            udp: None,
            default: None,
            sync_suppress_timeout: None,
        }
    }

    /// Reply for the given config name and its two linked map objects.
    fn show_response(name: &str, map_name_v4: &str, map_name_v6: &str) -> ShowConfigResponse {
        ShowConfigResponse {
            name: name.to_string(),
            map_name_v4: map_name_v4.to_string(),
            map_name_v6: map_name_v6.to_string(),
            sync_config: None,
        }
    }

    #[test]
    fn test_merged_map_names_create_without_map_names_is_rejected() {
        let cmd = update_cmd("cfg", None, None);
        let err = merged_map_names(&show_response("", "", ""), &cmd).unwrap_err();

        assert_eq!("creating config 'cfg' requires --map-name-v4 and --map-name-v6", err);
    }

    #[test]
    fn test_merged_map_names_create_with_one_map_name_is_rejected() {
        let empty_reply = show_response("", "", "");
        assert!(merged_map_names(&empty_reply, &update_cmd("cfg", Some("v4"), None)).is_err());
        assert!(merged_map_names(&empty_reply, &update_cmd("cfg", None, Some("v6"))).is_err());
    }

    #[test]
    fn test_merged_map_names_create_with_both_map_names_uses_flags() {
        let cmd = update_cmd("cfg", Some("map4"), Some("map6"));
        let (map_name_v4, map_name_v6) = merged_map_names(&show_response("", "", ""), &cmd).unwrap();

        assert_eq!(("map4", "map6"), (map_name_v4.as_str(), map_name_v6.as_str()));
    }

    #[test]
    fn test_merged_map_names_existing_config_keeps_stored_names_without_flags() {
        let cmd = update_cmd("cfg", None, None);
        let (map_name_v4, map_name_v6) = merged_map_names(&show_response("cfg", "stored4", "stored6"), &cmd).unwrap();

        assert_eq!(("stored4", "stored6"), (map_name_v4.as_str(), map_name_v6.as_str()));
    }

    #[test]
    fn test_merged_map_names_existing_config_flag_overrides_stored_name() {
        let reply = show_response("cfg", "stored4", "stored6");
        let (map_name_v4, map_name_v6) = merged_map_names(&reply, &update_cmd("cfg", Some("new4"), None)).unwrap();

        assert_eq!(("new4", "stored6"), (map_name_v4.as_str(), map_name_v6.as_str()));
    }
}
