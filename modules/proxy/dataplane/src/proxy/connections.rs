use std::collections::HashMap;
use std::sync::Mutex;

#[derive(Clone, Copy)]
pub struct Connection {
    pub local_addr: u32,
    pub local_port: u16,
}

pub struct ConnectionsTable {
    connections: Mutex<HashMap<(u32, u16), Connection>>,
}

impl ConnectionsTable {
    pub fn new(num_connections: u32) -> Self {
        let mut connections = HashMap::new();
        connections.reserve(num_connections as usize);

        Self {
            connections: Mutex::new(connections),
        }
    }

    pub fn find(&self, addr: u32, port: u16) -> Option<Connection> {
        self.connections.lock().ok()?.get(&(addr, port)).cloned()
    }

    pub fn insert(&self, addr: u32, port: u16, connection: Connection) -> Result<(), Box<dyn std::error::Error + '_>> {
        let mut connections = self.connections.lock()?;
        connections.insert((addr, port), connection);
        Ok(())
    }
}
