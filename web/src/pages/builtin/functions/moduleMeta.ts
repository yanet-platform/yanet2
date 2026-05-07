export interface ModuleMeta {
    color: string;
    desc: string;
}

export const MODULE_META: Record<string, ModuleMeta> = {
    route:       { color: '#6FB6FF', desc: 'L3 forwarding (FIB lookup)' },
    pdump:       { color: '#C396FF', desc: 'Packet capture / mirror' },
    acl:         { color: '#6FE0A0', desc: 'Stateless ACL ruleset' },
    decap:       { color: '#5FE0E0', desc: 'Tunnel decapsulation (IPIP/GRE)' },
    nat64:       { color: '#FFB454', desc: 'Stateful NAT64 translation' },
    balancer:    { color: '#FF8FA3', desc: 'L4 load balancer (DSCP/hash)' },
    forward:     { color: '#B8B0A4', desc: 'Pure forwarding (no transform)' },
    'route-mpls': { color: '#80CFFF', desc: 'MPLS route forwarding' },
    fwstate:     { color: '#A0E080', desc: 'Firewall state tracking' },
    dscp:        { color: '#FFC080', desc: 'DSCP marking' },
};

export const metaFor = (type: string): ModuleMeta =>
    MODULE_META[type] ?? { color: '#888', desc: `${type} (custom)` };
