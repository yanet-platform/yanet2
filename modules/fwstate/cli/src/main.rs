use core::error::Error;
use std::net::Ipv6Addr;

use args::{DeleteCmd, LinkCmd, ModeCmd, ShowCmd, StatsCmd, UpdateCmd};
use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use fwstatepb::{
    DeleteConfigRequest, GetStatsRequest, LinkFwStateRequest, ListConfigsRequest, ShowConfigRequest,
    UpdateConfigRequest, fw_state_service_client::FwStateServiceClient,
};
use tonic::{codec::CompressionEncoding, transport::Channel};
use ync::logging;

mod args;

#[allow(non_snake_case)]
pub mod fwstatepb {
    use serde::Serialize;

    tonic::include_proto!("fwstatepb");
}

/// FWState module CLI.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    /// Gateway endpoint.
    #[clap(long, default_value = "grpc://[::1]:8080", global = true)]
    pub endpoint: String,
    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

/// Parse IPv6 address from string to bytes
fn parse_ipv6(s: &str) -> Result<Vec<u8>, Box<dyn Error>> {
    let addr: Ipv6Addr = s.parse()?;
    Ok(addr.octets().to_vec())
}

/// Parse MAC address from string to bytes
fn parse_mac(s: &str) -> Result<Vec<u8>, Box<dyn Error>> {
    let parts: Vec<&str> = s.split(':').collect();
    if parts.len() != 6 {
        return Err(format!("invalid MAC address format: {}", s).into());
    }

    let mut bytes = Vec::with_capacity(6);
    for part in parts {
        let byte = u8::from_str_radix(part, 16).map_err(|_| format!("invalid MAC address byte: {}", part))?;
        bytes.push(byte);
    }

    Ok(bytes)
}

pub struct FWStateService {
    client: FwStateServiceClient<Channel>,
}

impl FWStateService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = FwStateServiceClient::connect(endpoint).await?;
        let client = client
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);
        Ok(Self { client })
    }

    pub async fn list_configs(&mut self) -> Result<(), Box<dyn Error>> {
        let request = ListConfigsRequest {};
        let response = self.client.list_configs(request).await?.into_inner();
        println!("{}", serde_json::to_string(&response.configs)?);
        Ok(())
    }

    pub async fn show_config(&mut self, cmd: ShowCmd) -> Result<(), Box<dyn Error>> {
        let request = ShowConfigRequest {
            name: cmd.config_name.clone(),
            ok_if_not_found: false,
        };
        let response = self.client.show_config(request).await?.into_inner();
        println!("{}", serde_json::to_string(&response)?);
        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Box<dyn Error>> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };
        self.client.delete_config(request).await?.into_inner();
        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Box<dyn Error>> {
        // First, fetch the current config to merge with new values
        let current_request = ShowConfigRequest {
            name: cmd.config_name.clone(),
            ok_if_not_found: true,
        };
        let current_response = self.client.show_config(current_request).await;
        let (mut map_config, mut sync_config) = match current_response {
            Ok(resp) => {
                let msg = resp.into_inner();
                (msg.map_config.unwrap_or_default(), msg.sync_config.unwrap_or_default())
            }
            _ => (Default::default(), Default::default()),
        };

        // Update map config fields if provided
        if let Some(index_size) = cmd.index_size {
            map_config.index_size = index_size;
        }

        if let Some(extra_bucket_count) = cmd.extra_bucket_count {
            map_config.extra_bucket_count = extra_bucket_count;
        }

        // Update only the fields that were provided
        if let Some(ref src_addr) = cmd.src_addr {
            sync_config.src_addr = parse_ipv6(src_addr)?;
        }

        if let Some(ref dst_ether) = cmd.dst_ether {
            sync_config.dst_ether = parse_mac(dst_ether)?;
        }

        if let Some(ref dst_addr_multicast) = cmd.dst_addr_multicast {
            sync_config.dst_addr_multicast = parse_ipv6(dst_addr_multicast)?;
        }

        if let Some(port_multicast) = cmd.port_multicast {
            sync_config.port_multicast = port_multicast;
        }

        if let Some(ref dst_addr_unicast) = cmd.dst_addr_unicast {
            sync_config.dst_addr_unicast = parse_ipv6(dst_addr_unicast)?;
        }

        if let Some(port_unicast) = cmd.port_unicast {
            sync_config.port_unicast = port_unicast;
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

        let request = UpdateConfigRequest {
            name: cmd.config_name.clone(),
            map_config: Some(map_config),
            sync_config: Some(sync_config),
        };
        log::trace!("UpdateConfigRequest: {request:?}");
        let response = self.client.update_config(request).await?.into_inner();
        log::debug!("UpdateConfigResponse: {response:?}");
        Ok(())
    }

    pub async fn link_fwstate(&mut self, cmd: LinkCmd) -> Result<(), Box<dyn Error>> {
        let request = LinkFwStateRequest {
            fwstate_name: cmd.config_name.clone(),
            acl_config_names: cmd.acl_configs.clone(),
        };
        log::trace!("LinkFwStateRequest: {request:?}");
        let response = self.client.link_fw_state(request).await?.into_inner();
        log::debug!("LinkFwStateResponse: {response:?}");
        Ok(())
    }

    pub async fn get_stats(&mut self, cmd: StatsCmd) -> Result<(), Box<dyn Error>> {
        let request = GetStatsRequest { name: cmd.config_name.clone() };
        log::trace!("GetStatsRequest: {request:?}");
        let response = self.client.get_stats(request).await?.into_inner();
        println!("{}", serde_json::to_string_pretty(&response)?);
        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = FWStateService::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::Link(cmd) => service.link_fwstate(cmd).await,
        ModeCmd::Stats(cmd) => service.get_stats(cmd).await,
    }
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();
    let cmd = Cmd::parse();
    logging::init(cmd.verbose as usize).expect("initialize logging");

    if let Err(err) = run(cmd).await {
        log::error!("ERROR: {err}");
        std::process::exit(1);
    }
}
