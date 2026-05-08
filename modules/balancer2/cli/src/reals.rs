use std::net::IpAddr;

use clap::Parser;

use crate::VsId;

#[derive(Debug, Clone, Parser)]
pub struct RealsCmd {
    #[clap(subcommand)]
    pub mode: RealsMode,
}

#[derive(Debug, Clone, Parser)]
pub enum RealsMode {
    /// Enable real servers.
    Enable(EnableRealCmd),
    /// Disable real servers.
    Disable(DisableRealCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct EnableRealCmd {
    /// Balancer configuration name.
    #[arg(long, short = 'n')]
    pub name: String,
    /// Virtual service identifier: "ip:port/proto" or "[ipv6]:port/proto".
    #[arg(long)]
    pub vs: VsId,
    /// Real server IPs to enable (port assumed 0; matches reals configured
    /// with relative port 0).
    #[arg(long, required = true, num_args = 1..)]
    pub reals: Vec<IpAddr>,
    /// Optional new weight for the real servers.
    #[arg(long)]
    pub weight: Option<u32>,
}

#[derive(Debug, Clone, Parser)]
pub struct DisableRealCmd {
    /// Balancer configuration name.
    #[arg(long, short = 'n')]
    pub name: String,
    /// Virtual service identifier: "ip:port/proto" or "[ipv6]:port/proto".
    #[arg(long)]
    pub vs: VsId,
    /// Real server IPs to disable (port assumed 0; matches reals configured
    /// with relative port 0).
    #[arg(long, required = true, num_args = 1..)]
    pub reals: Vec<IpAddr>,
}
