//! gRPC service client implementation

use std::error::Error;
use tonic::transport::Channel;

use crate::{
    cmd::*,
    entities::BalancerConfig,
    output,
    rpc::{BalancerServiceClient, balancerpb},
};

////////////////////////////////////////////////////////////////////////////////
// Service
////////////////////////////////////////////////////////////////////////////////

pub struct BalancerService {
    client: BalancerServiceClient<Channel>,
}

impl BalancerService {
    /// Connect to the gRPC endpoint
    pub async fn connect(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = BalancerServiceClient::connect(endpoint).await?;
        Ok(Self { client })
    }

    /// Handle the command
    pub async fn handle_cmd(&mut self, mode: Mode) -> Result<(), Box<dyn Error>> {
        log::trace!("Handling command: {:?}", mode);
        
        match mode {
            Mode::Update(cmd) => self.update_config(cmd).await,
            Mode::Reals(cmd) => self.handle_reals(cmd).await,
            Mode::Config(cmd) => self.config(cmd).await,
            Mode::List(cmd) => self.list(cmd).await,
            Mode::Stats(cmd) => self.stats(cmd).await,
            Mode::State(cmd) => self.state(cmd).await,
            Mode::Sessions(cmd) => self.sessions(cmd).await,
        }
    }

    /// Update balancer configuration
    async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Box<dyn Error>> {
        log::info!("Loading configuration from: {}", cmd.config);
        
        let config = BalancerConfig::from_yaml_file(&cmd.config)?;
        let (module_config, module_state_config) = config.try_into()?;

        let request = balancerpb::UpdateConfigRequest {
            target: Some(crate::rpc::commonpb::TargetModule {
                config_name: cmd.name.clone(),
                dataplane_instance: cmd.instance,
            }),
            module_config: Some(module_config),
            module_state_config: Some(module_state_config),
        };

        log::debug!("Sending UpdateConfig request");
        self.client.update_config(request).await?;
        
        log::info!("Successfully updated configuration for '{}' (instance: {})", cmd.name, cmd.instance);
        Ok(())
    }

    /// Handle reals commands
    async fn handle_reals(&mut self, cmd: RealsCmd) -> Result<(), Box<dyn Error>> {
        match cmd.mode {
            RealsMode::Enable(cmd) => self.enable_real(cmd).await,
            RealsMode::Disable(cmd) => self.disable_real(cmd).await,
            RealsMode::Flush(cmd) => self.flush_real_updates(cmd).await,
        }
    }

    /// Enable a real server
    async fn enable_real(&mut self, cmd: EnableRealCmd) -> Result<(), Box<dyn Error>> {
        log::info!("Buffering enable request for real {} in VS {}:{}/{}",
            cmd.real_ip, cmd.virtual_ip, cmd.virtual_port, cmd.proto);

        let request: balancerpb::UpdateRealsRequest = cmd.try_into()?;
        
        log::debug!("Sending UpdateReals request");
        self.client.update_reals(request).await?;
        
        log::info!("Successfully buffered real enable");
        Ok(())
    }

    /// Disable a real server
    async fn disable_real(&mut self, cmd: DisableRealCmd) -> Result<(), Box<dyn Error>> {
        log::info!("Buffering disable request for real {} in VS {}:{}/{}",
            cmd.real_ip, cmd.virtual_ip, cmd.virtual_port, cmd.proto);

        let request: balancerpb::UpdateRealsRequest = cmd.try_into()?;
        
        log::debug!("Sending UpdateReals request");
        self.client.update_reals(request).await?;
        
        log::info!("Successfully buffered real disable");
        Ok(())
    }

    /// Flush buffered real updates
    async fn flush_real_updates(&mut self, cmd: FlushRealUpdatesCmd) -> Result<(), Box<dyn Error>> {
        log::info!("Flushing buffered real updates for '{}' (instance: {})", cmd.name, cmd.instance);

        let request: balancerpb::FlushRealUpdatesRequest = cmd.into();
        
        log::debug!("Sending FlushRealUpdates request");
        let response = self.client.flush_real_updates(request).await?.into_inner();
        
        log::info!("Successfully flushed {} update(s)", response.updates_flushed);
        Ok(())
    }

    /// Show balancer configuration
    async fn config(&mut self, cmd: ConfigCmd) -> Result<(), Box<dyn Error>> {
        log::debug!("Fetching configuration for '{}' (instance: {})", cmd.name, cmd.instance);

        let request: balancerpb::ShowConfigRequest = (&cmd).into();
        let response = self.client.show_config(request).await?.into_inner();
        
        output::print_show_config(&response, cmd.format.into())?;
        Ok(())
    }

    /// List all balancer configurations
    async fn list(&mut self, cmd: ListCmd) -> Result<(), Box<dyn Error>> {
        log::debug!("Fetching all configurations");

        let request = balancerpb::ListConfigsRequest {};
        let response = self.client.list_configs(request).await?.into_inner();
        
        output::print_list_configs(&response, cmd.format.into())?;
        Ok(())
    }

    /// Show configuration statistics
    async fn stats(&mut self, cmd: StatsCmd) -> Result<(), Box<dyn Error>> {
        log::debug!("Fetching statistics for '{}' (instance: {})", cmd.name, cmd.instance);

        let request: balancerpb::ConfigStatsRequest = (&cmd).into();
        let response = self.client.config_stats(request).await?.into_inner();
        
        output::print_config_stats(&response, cmd.format.into())?;
        Ok(())
    }

    /// Show state information
    async fn state(&mut self, cmd: StateCmd) -> Result<(), Box<dyn Error>> {
        log::debug!("Fetching state info for '{}' (instance: {})", cmd.name, cmd.instance);

        let request: balancerpb::StateInfoRequest = (&cmd).into();
        let response = self.client.state_info(request).await?.into_inner();
        
        output::print_state_info(&response, cmd.format.into())?;
        Ok(())
    }

    /// Show sessions information
    async fn sessions(&mut self, cmd: SessionsCmd) -> Result<(), Box<dyn Error>> {
        log::debug!("Fetching sessions info for '{}' (instance: {})", cmd.name, cmd.instance);

        let request: balancerpb::SessionsInfoRequest = (&cmd).into();
        let response = self.client.sessions_info(request).await?.into_inner();
        
        output::print_sessions_info(&response, cmd.format.into())?;
        Ok(())
    }
}