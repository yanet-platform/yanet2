use std::sync::RwLock;

struct localToClient {
    keys: Vec<(u32, u16)>,
    values: Vec<(u32, u16)>,
}

impl localToClient {
    fn get(&self, key: &(u32, u16)) -> Option<&(u32, u16)> {
        for (i, k) in self.keys.iter().enumerate() {
            if k == key {
                return Some(&self.values[i])
            }
        }
        None
    }

    fn insert(&mut self, key: (u32, u16), value: (u32, u16)) {
        for (i, k) in self.keys.iter().enumerate() {
            if k == &(0, 0) {
                self.keys[i] = key;
                self.values[i] = value;
                break;
            }
        };
    }

    fn remove(&mut self, key: &(u32, u16)) {
        for (i, k) in self.keys.iter().enumerate() {
            if k == key {
                self.keys[i] = (0, 0);
                self.values[i] = (0, 0);
                break;
            }
        };
    }
}

struct localPool {
    local_to_client: localToClient,
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
                local_to_client: localToClient{
                    keys: vec![(0, 0); num_addrs as usize],
                    values: vec![(0, 0); num_addrs as usize]
                },
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

    pub fn free(&self, local: (u32, u16)) {
        let mut lp = self.lp.write().unwrap();
        lp.local_to_client.remove(&local);
        lp.pool.push(local);
    }

    pub fn get_client(&self, local_addr: u32, local_port: u16) -> Option<(u32, u16)> {
        let lp = self.lp.read().unwrap();
        lp.local_to_client.get(&(local_addr, local_port)).cloned()
    }
}

