use std::sync::Mutex;

use bindings::{memory_context, memory_balloc};
use memory::{set_offset_of, addr_of};

#[derive(Clone, Copy, Default)]
pub struct Connection {
    pub local_addr: u32,
    pub local_port: u16,
}

struct ConnectionTableElem {
    key: (u32, u16),
    value: Connection,
}

struct ConnectionStorage {
    ptr: *mut ConnectionTableElem,
    len: usize,
}

impl ConnectionStorage {
    fn new(ptr: *mut ConnectionTableElem, len: usize) -> Self {
        Self {
            ptr,
            len
        }
    }

    fn adjust_pointers(&mut self) {
        set_offset_of!(std::ptr::addr_of_mut!(self.ptr), self.ptr);
    }

    fn get(&self, key: &(u32, u16)) -> Option<&Connection> {
        for elem in self.slice().iter() {
            if elem.key == *key {
                return Some(&elem.value)
            }
        }
        None
    }

    fn insert(&mut self, key: (u32, u16), value: Connection) {
        for elem in self.slice_mut().iter_mut() {
            if elem.key == (0, 0) {
                elem.key = key;
                elem.value = value;
                break;
            }
        };
    }

    fn slice(&self) -> &[ConnectionTableElem] {
        unsafe {
            let ptr = addr_of!(std::ptr::addr_of!(self.ptr));
            std::slice::from_raw_parts(ptr, self.len)
        }
    }

    fn slice_mut(&mut self) -> &mut [ConnectionTableElem] {
        unsafe {
            let ptr = addr_of!(std::ptr::addr_of_mut!(self.ptr));
            std::slice::from_raw_parts_mut(ptr, self.len)
        }
    }
}

pub struct ConnectionsTable {
    connections: Mutex<ConnectionStorage>,
}

impl ConnectionsTable {
    pub fn new(mctx: *mut memory_context, num_connections: usize) -> Result<Self, &'static str> {
        let storage: *mut ConnectionTableElem = unsafe { memory_balloc(
            mctx,
            size_of::<ConnectionTableElem>() * num_connections,
        ) as *mut ConnectionTableElem };
        if storage.is_null() {
            return Err("failed to allocate conn table memory");
        }

        Ok(Self {
            connections: Mutex::new(
                ConnectionStorage::new(
                    storage,
                    num_connections
                ),
            ),
        })
    }

    pub fn adjust_pointers(&mut self) {
        self.connections.lock().unwrap().adjust_pointers();
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
