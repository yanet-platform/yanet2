/**
 * Pure parse helpers shared between the main thread and the YAML import worker.
 *
 * No DOM, no React, no module side-effects beyond js-yaml.
 * Both hooks.ts and yamlImport.worker.ts import from here.
 */

import type { ProtoRange } from '../../../api/acl';

export { parseCidrsToIPNets } from '../../../utils';

/** Parse a port/vlan range string (e.g. "80-90", "80") to a {from, to} object. */
const parseRangeStr = (s: string): { from: number; to: number } | null => {
    const trimmed = s.trim();
    if (!trimmed) return null;
    if (trimmed.includes('-')) {
        const [fromStr, toStr] = trimmed.split('-');
        const from = parseInt(fromStr, 10);
        const to = parseInt(toStr, 10);
        if (isNaN(from) || isNaN(to)) return null;
        return { from, to };
    }
    const val = parseInt(trimmed, 10);
    if (isNaN(val)) return null;
    return { from: val, to: val };
};

/** Parse a raw range input string (comma or newline separated) to a range array. */
export const parseRangesRaw = (raw: string): Array<{ from: number; to: number }> => {
    if (!raw.trim()) return [];
    return raw.split(/[,\n]+/)
        .map(s => parseRangeStr(s))
        .filter((r): r is { from: number; to: number } => r !== null);
};

/** Parse encoded proto ranges (e.g. "1536-1791") to ProtoRange wire objects. */
export const parseProtoRangesRaw = (raw: string): ProtoRange[] => {
    if (!raw.trim()) return [];
    return raw.split(/[,\n]+/)
        .map(s => parseRangeStr(s))
        .filter((r): r is { from: number; to: number } => r !== null);
};
