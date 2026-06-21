import { NUD_STATE_MAP } from '@yanet/core/utils/nud';

/** Metadata for a single NUD state — descriptions and action guidance. */
export interface StateMetaEntry {
    /** CSS color token for this state (uses existing yn-* / g-color-* tokens). */
    color: string;
    /** Short human-readable description of what the state means. */
    desc: string;
    /** Actionable guidance for an operator. */
    action: string;
}

/** State→color/description/action mapping for NUD states. */
export const STATE_META: Record<string, StateMetaEntry> = {
    REACHABLE: {
        color: 'var(--g-color-text-positive)',
        desc: 'Confirmed reachable — a valid mapping was verified recently. Traffic is forwarded directly.',
        action: 'Healthy. No action needed.',
    },
    PERMANENT: {
        color: 'var(--yn-accent)',
        desc: 'Static entry pinned by an administrator. Never expires and is not subject to ARP/ND timers.',
        action: 'Managed manually. Edit or delete via the static table.',
    },
    STALE: {
        color: 'var(--g-color-text-warning)',
        desc: 'Mapping was valid but its lifetime has expired. The entry stays usable; the kernel revalidates it on the next packet.',
        action: 'Normal for idle peers. If traffic flows but it stays STALE, check that return packets reach this device.',
    },
    DELAY: {
        color: 'var(--g-color-text-warning)',
        desc: 'A packet was sent using a stale entry; the kernel is waiting briefly for upper-layer confirmation before probing.',
        action: 'Transient. Should move to REACHABLE or PROBE within seconds.',
    },
    PROBE: {
        color: 'var(--g-color-text-info)',
        desc: 'The kernel is actively sending unicast probes to re-confirm the neighbour.',
        action: 'Transient. If it loops PROBE→FAILED, the peer is likely offline or filtering ARP/ND.',
    },
    INCOMPLETE: {
        color: 'var(--g-color-text-info)',
        desc: 'Address resolution is in progress — a request was sent but no reply received yet. No L2 address is known.',
        action: 'If it never resolves, verify the peer is on-link and answering ARP/ND on this device.',
    },
    FAILED: {
        color: 'var(--g-color-text-danger)',
        desc: 'Resolution failed — the neighbour did not answer probes. Packets to this next hop are dropped.',
        action: 'Check link/cabling, that the peer is up, and that ARP/ND is not filtered. A static entry can override this.',
    },
    NOARP: {
        color: 'var(--yn-text-3)',
        desc: 'No L2 resolution is required for this entry (loopback, point-to-point, or multicast). This is expected, not an error.',
        action: 'Expected for lo / multicast / kni interfaces.',
    },
    NONE: {
        color: 'var(--yn-text-3)',
        desc: 'No state recorded for this entry.',
        action: '—',
    },
    UNKNOWN: {
        color: 'var(--yn-text-3)',
        desc: 'Unrecognized state value.',
        action: '—',
    },
};

/** Resolves a NUD state number to its name using NUD_STATE_MAP. */
export const nudStateToName = (state: number | undefined | null): string => {
    if (state === undefined || state === null) return 'NONE';
    return NUD_STATE_MAP[state] || 'UNKNOWN';
};

/** Gets the state metadata for a NUD state number. Falls back to UNKNOWN. */
export const getStateMeta = (state: number | undefined | null): StateMetaEntry => {
    const name = nudStateToName(state);
    return STATE_META[name] ?? STATE_META['UNKNOWN'];
};
