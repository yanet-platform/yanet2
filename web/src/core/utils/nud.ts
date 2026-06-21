/**
 * Neighbour Unreachability Detection (NUD) state mapping.
 * 
 * Maps NUD state enum values to their string representations.
 */
export const NUD_STATE_MAP: Record<number, string> = {
    0x00: 'NONE',
    0x01: 'INCOMPLETE',
    0x02: 'REACHABLE',
    0x04: 'STALE',
    0x08: 'DELAY',
    0x10: 'PROBE',
    0x20: 'FAILED',
    0x40: 'NOARP',
    0x80: 'PERMANENT',
    0xff: 'UNKNOWN',
};

/**
 * Gets NUD state string representation.
 * 
 * @param state - NUD state enum value
 * @returns State string representation or "UNKNOWN(state)" if state is not recognized
 */
export function getNUDStateString(state: number | undefined | null): string {
    if (state === undefined || state === null) {
        return '-';
    }
    return NUD_STATE_MAP[state] || `UNKNOWN(${state})`;
}
