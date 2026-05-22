import type { Rule, Action } from '../../../api/acl-ng';
import { formatIPNet } from '../../../utils';
import { extractBytes } from './utils';

/** Format an IPNet wire object to a CIDR string for comparison purposes. */
const fmtIPNet = (net: { addr?: string | Uint8Array | number[]; mask?: string | Uint8Array | number[] }): string => {
    const addrBytes = extractBytes(net.addr);
    const maskBytes = extractBytes(net.mask);
    if (!addrBytes || addrBytes.length === 0) return '';
    return formatIPNet(addrBytes, maskBytes);
};

/** Serialize a range {from, to} to a canonical string for equality comparison. */
const fmtRange = (r: { from?: number; to?: number }): string =>
    `${r.from ?? 0}-${r.to ?? 0}`;

/** Serialize an action to a canonical string. */
const fmtAction = (a: Action): string => String(a.kind ?? 0);

/** Field names that participate in equality comparison. */
type RuleField =
    | 'actions'
    | 'counter'
    | 'srcs'
    | 'dsts'
    | 'src_port_ranges'
    | 'dst_port_ranges'
    | 'proto_ranges'
    | 'vlan_ranges'
    | 'devices';

/** Human-readable labels for display in the diff UI. */
export const RULE_FIELD_LABELS: Record<RuleField, string> = {
    actions: 'Actions',
    counter: 'Counter',
    srcs: 'Sources',
    dsts: 'Destinations',
    src_port_ranges: 'Src ports',
    dst_port_ranges: 'Dst ports',
    proto_ranges: 'Protocols',
    vlan_ranges: 'VLANs',
    devices: 'Devices',
};

const RULE_FIELDS: RuleField[] = [
    'actions',
    'counter',
    'srcs',
    'dsts',
    'src_port_ranges',
    'dst_port_ranges',
    'proto_ranges',
    'vlan_ranges',
    'devices',
];

/** Serialize a field from a Rule to a canonical string array for comparison. */
const fieldValues = (rule: Rule, field: RuleField): string[] => {
    switch (field) {
        case 'actions':
            return (rule.actions ?? []).map(fmtAction);
        case 'counter':
            return [rule.counter ?? ''];
        case 'srcs':
            return (rule.srcs ?? []).map(fmtIPNet).filter(Boolean);
        case 'dsts':
            return (rule.dsts ?? []).map(fmtIPNet).filter(Boolean);
        case 'src_port_ranges':
            return (rule.src_port_ranges ?? []).map(fmtRange);
        case 'dst_port_ranges':
            return (rule.dst_port_ranges ?? []).map(fmtRange);
        case 'proto_ranges':
            return (rule.proto_ranges ?? []).map(fmtRange);
        case 'vlan_ranges':
            return (rule.vlan_ranges ?? []).map(fmtRange);
        case 'devices':
            return (rule.devices ?? []).map(d => d.name ?? '').filter(Boolean);
    }
};

const fieldEqual = (a: Rule, b: Rule, field: RuleField): boolean =>
    JSON.stringify(fieldValues(a, field)) === JSON.stringify(fieldValues(b, field));

export interface AddedEntry {
    rule: Rule;
    newIdx: number;
    id: string;
}

export interface RemovedEntry {
    rule: Rule;
    oldIdx: number;
    id: string;
}

export interface ModifiedEntry {
    before: Rule;
    after: Rule;
    oldIdx: number;
    newIdx: number;
    id: string;
    diffFields: RuleField[];
}

export interface ReorderedEntry {
    rule: Rule;
    oldIdx: number;
    newIdx: number;
    id: string;
}

export interface StructuredDiff {
    added: AddedEntry[];
    removed: RemovedEntry[];
    modified: ModifiedEntry[];
    reordered: ReorderedEntry[];
}

/**
 * Compute a structured diff between two rule arrays.
 *
 * Rules are matched by stable id. draftIds[i] is the id for draft[i];
 * server rules are always assigned ids "srv-N" for index N.
 *
 * Action comparison is order-sensitive (action chain order matters for ACL).
 * CIDR comparison is string-level after formatting (addr+mask bytes → CIDR string).
 */
export const computeStructuredDiff = (
    draft: Rule[],
    draftIds: string[],
    server: Rule[],
): StructuredDiff => {
    // Server side: always use srv-N ids (matches what draftReducer assigns on load).
    const sById = new Map(server.map((rule, idx) => [`srv-${idx}`, { rule, idx }]));
    const lById = new Map(draft.map((rule, idx) => [draftIds[idx] ?? `tmp-${idx}`, { rule, idx }]));

    const added: AddedEntry[] = [];
    const removed: RemovedEntry[] = [];
    const modified: ModifiedEntry[] = [];
    const reordered: ReorderedEntry[] = [];

    for (const [id, { rule: lr, idx: li }] of lById) {
        const s = sById.get(id);
        if (!s) {
            added.push({ rule: lr, newIdx: li, id });
            continue;
        }
        const diffFields = RULE_FIELDS.filter(f => !fieldEqual(lr, s.rule, f));
        if (diffFields.length > 0) {
            modified.push({ before: s.rule, after: lr, oldIdx: s.idx, newIdx: li, id, diffFields });
        } else if (s.idx !== li) {
            reordered.push({ id, oldIdx: s.idx, newIdx: li, rule: lr });
        }
    }

    for (const [id, { rule, idx }] of sById) {
        if (!lById.has(id)) {
            removed.push({ rule, oldIdx: idx, id });
        }
    }

    return { added, removed, modified, reordered };
};
