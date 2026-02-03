use std::collections::HashMap;
use std::sync::RwLock;

use libc::__u16;

struct localPool {
    local_to_client: HashMap<(u32, u16), (u32, u16)>,
    pool: Vec<(u32, u16)>,
}
pub struct LocalPool {
    lp: RwLock<localPool>,
}

impl LocalPool {
    const MIN_PORT: u16 = 32768;
    const MAX_PORT: u16 = 65535;
    const NUM_PORTS: u16 = Self::MAX_PORT - Self::MIN_PORT + 1;

    pub fn new(subnet_addr: u32, subnet_mask: u8) -> Self {
        let num_addrs = 1 << (32 - subnet_mask);
        let num_conns = num_addrs * Self::NUM_PORTS as u32;

        let mut pool = Vec::new();
        pool.reserve(num_conns as usize);

        for i in (0..num_addrs).rev() {
            for j in (0..Self::NUM_PORTS).rev() {
                pool.push(((subnet_addr + i).swap_bytes(), (Self::MIN_PORT + j).swap_bytes()));
            }
        }

        Self {
            lp: RwLock::new(localPool {
                local_to_client: HashMap::new(),
                pool
            }),
        }
    }

    pub fn allocate(&self, client_addr: u32, client_port: u16) -> Option<(u32, u16)> {
        let mut lp = self.lp.write().unwrap();
        let local = lp.pool.pop();
        if let Some(local) = local {
            lp.local_to_client.insert(local, (client_addr, client_port));
            Some(local)
        } else {
            None
        }
    }

    pub fn free(&self, local: (u32, u16)) -> Option<(u32, u16)> {
        let mut lp = self.lp.write().unwrap();
        lp.local_to_client.remove(&local).map(|client| {
            lp.pool.push(local);
            client
        })
    }

    pub fn get_client(&self, local_addr: u32, local_port: u16) -> Option<(u32, u16)> {
        let lp = self.lp.read().unwrap();
        lp.local_to_client.get(&(local_addr, local_port)).cloned()
    }
}

