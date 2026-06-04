/** Format a {from, to} range to "80" (when from===to) or "80-90". */
export const formatRange = (r: { from?: number; to?: number }): string => {
    const from = r.from ?? 0;
    const to = r.to ?? 0;
    if (from === to) return String(from);
    return `${from}-${to}`;
};
