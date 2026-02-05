use std::sync::Mutex;

#[derive(Clone, Copy, Default)]
pub struct Connection {
    pub local_addr: u32,
    pub local_port: u16,
}

struct connectionStorage {
    keys: Vec<(u32, u16)>,
    values: Vec<Connection>,
}

impl connectionStorage {
    fn get(&self, key: &(u32, u16)) -> Option<&Connection> {
        for (i, k) in self.keys.iter().enumerate() {
            if k == key {
                return Some(&self.values[i])
            }
        }
        None
    }

    fn insert(&mut self, key: (u32, u16), value: Connection) {
        for (i, k) in self.keys.iter().enumerate() {
            if k == &(0, 0) {
                self.keys[i] = key;
                self.values[i] = value;
                break;
            }
        };
    }
}

pub struct ConnectionsTable {
    connections: Mutex<connectionStorage>,
}

impl ConnectionsTable {
    pub fn new(num_connections: usize) -> Self {
        Self {
            connections: Mutex::new(connectionStorage{
                keys: vec![(0, 0); num_connections],
                values: vec![Connection::default(); num_connections],
            }),
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
