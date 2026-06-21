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

/** Format a {from, to} range to "80" (when from===to) or "80-90". */
export const formatRange = (r: { from?: number; to?: number }): string => {
    const from = r.from ?? 0;
    const to = r.to ?? 0;
    if (from === to) return String(from);
    return `${from}-${to}`;
};
