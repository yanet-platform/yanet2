use std::{ptr, sync::RwLock};

use bindings::{memory_context, memory_balloc};
use crate::config::IPv4Prefix;
use memory::{set_offset_of, addr_of};

struct LocalToClientElem {
    key: (u32, u16),
    value: (u32, u16)
}

struct LocalToClient {
    ptr: *mut LocalToClientElem,
    len: usize
}

impl LocalToClient {
    fn new(mctx: *mut memory_context, size: usize) -> Result<Self, &'static str>  {
        let ltc = unsafe { memory_balloc(
            mctx,
            size_of::<LocalToClientElem>() * size,
        ) as *mut LocalToClientElem };
        if ltc.is_null() {
            return Err("failed to allocate local_to_client memory");
        }

        unsafe { ptr::write_bytes(ltc, 0, size) };

        Ok(Self{
            ptr: ltc,
            len: size
        })
    }

    fn adjust_pointers(&mut self) {
        set_offset_of!(std::ptr::addr_of_mut!(self.ptr), self.ptr);
    }

    fn get(&self, key: &(u32, u16)) -> Option<&(u32, u16)> {
        for k in self.slice().iter() {
            if k.key == *key {
                return Some(&k.value)
            }
        }
        None
    }

    fn insert(&mut self, key: (u32, u16), value: (u32, u16)) {
        for k in self.slice_mut().iter_mut() {
            if k.key == (0, 0) {
                k.key = key;
                k.value = value;
                break;
            }
        };
    }

    fn remove(&mut self, key: &(u32, u16)) {
        for k in self.slice_mut().iter_mut() {
            if k.key == *key {
                k.key = (0, 0);
                k.value = (0, 0);
                break;
            }
        };
    }

    fn slice(&self) -> &[LocalToClientElem] {
        unsafe {
            let ptr = addr_of!(std::ptr::addr_of!(self.ptr));
            std::slice::from_raw_parts(ptr, self.len)
        }
    }

    fn slice_mut(&mut self) -> &mut [LocalToClientElem] {
        unsafe {
            let ptr = addr_of!(std::ptr::addr_of_mut!(self.ptr));
            std::slice::from_raw_parts_mut(ptr, self.len)
        }
    }
}

#[derive(Clone, Copy)]
pub struct LocalPoolElem {
    pub addr: u32,
    pub port: u16,
}

struct _LocalPool {
    local_to_client: LocalToClient,
    pool_ptr: *mut LocalPoolElem,
    pool_len: usize,
    pool_idx: usize,
}

impl _LocalPool {
    fn new(local_to_client: LocalToClient, pool: *mut LocalPoolElem, len: usize) -> Self {
        Self {
            local_to_client,
            pool_ptr: pool,
            pool_len: len,
            pool_idx: len
        }
    }

    fn adjust_pointers(&mut self) {
        self.local_to_client.adjust_pointers();
        set_offset_of!(std::ptr::addr_of_mut!(self.pool_ptr), self.pool_ptr);
    }

    fn pop(&mut self) -> Option<LocalPoolElem> {
        if self.pool_idx == 0 {
            return None
        }
        self.pool_idx -= 1;
        let idx = self.pool_idx;
        let pool = self.pool_mut();
        Some(pool[idx])
    }

    fn push(&mut self, addr: u32, port: u16) {
        let idx = self.pool_idx;
        let pool = self.pool_mut();
        pool[idx] = LocalPoolElem { addr, port };
        self.pool_idx += 1;
    }

    fn pool_mut(&mut self) -> &mut [LocalPoolElem] {
        unsafe {
            let ptr = addr_of!(std::ptr::addr_of_mut!(self.pool_ptr));
            std::slice::from_raw_parts_mut(ptr, self.pool_len)
        }
    }
}

pub struct LocalPool {
    lp: RwLock<_LocalPool>,
}

impl LocalPool {
    const MIN_PORT: u16 = 32768;
    const MAX_PORT: u16 = 65535;
    const NUM_PORTS: u16 = Self::MAX_PORT - Self::MIN_PORT + 1;

    pub fn new(mctx: *mut memory_context, subnet: IPv4Prefix) -> Result<Self, &'static str>  {
        let num_addrs = 1 << (32 - subnet.mask);
        let num_conns = num_addrs * Self::NUM_PORTS as usize;

        let pool = unsafe { memory_balloc(
            mctx,
            size_of::<LocalPoolElem>() * num_conns,
        ) as *mut LocalPoolElem };
        if pool.is_null() {
            return Err("failed to allocate pool memory");
        }
        
        let pool_slice = unsafe { std::slice::from_raw_parts_mut(pool, num_conns) };
        for i in (0..num_addrs).rev() {
            for j in (0..Self::NUM_PORTS).rev() {
                pool_slice[i * Self::NUM_PORTS as usize + j as usize] = LocalPoolElem{
                    addr: (subnet.addr + i as u32).swap_bytes(),
                    port: (Self::MIN_PORT + j).swap_bytes()
                }
            }
        }

        let local_to_client = LocalToClient::new(mctx, num_conns)?;

        Ok(Self {
            lp: RwLock::new(_LocalPool::new(local_to_client, pool, num_conns)),
        })
    }

    pub fn adjust_pointers(&mut self) {
        self.lp.write().unwrap().adjust_pointers();
    }

    pub fn allocate(&mut self, client_addr: u32, client_port: u16) -> Option<LocalPoolElem> {
        let mut lp = self.lp.write().unwrap();
        let local = lp.pop()?;
        lp.local_to_client.insert((local.addr, local.port), (client_addr, client_port));

        Some(local)
    }

    pub fn free(&mut self, local_addr: u32, local_port: u16) {
        let mut lp = self.lp.write().unwrap();
        lp.local_to_client.remove(&(local_addr, local_port));
        lp.push(local_addr, local_port);
    }

    pub fn get_client(&self, local_addr: u32, local_port: u16) -> Option<(u32, u16)> {
        let lp = self.lp.read().unwrap();
        lp.local_to_client.get(&(local_addr, local_port)).cloned()
    }
}

