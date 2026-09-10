import type { Neighbour } from '@yanet/core/api/neighbours';
import {
    compareMACAddressValues,
    compareNullableNumbers,
    compareNullableStrings,
    getMACAddressValue,
    getUnixSecondsValue,
    isValidMAC,
} from '@yanet/core/utils';
import { formatIPFromBytes, parseIPToBytes, stringToIPAddress } from '@yanet/core/utils/netip';
import type { SortableColumn } from './types';
import { MERGED_TAB } from './types';

/** Resolve the target table for a neighbour drawer submit. */
export const resolveSubmitTable = (
    mode: 'add' | 'edit',
    activeTable: string,
    selectedTable: string | undefined,
    defaultTable: string,
    neighbour: Neighbour | null,
): string => {
    if (mode === 'add' && activeTable === MERGED_TAB) {
        return selectedTable || defaultTable;
    }
    if (mode === 'edit' && activeTable === MERGED_TAB) {
        return neighbour?.source || 'static';
    }
    return activeTable;
};

export { isValidMAC };

/** Canonicalizes next hops to the receiver's unmapped IP identity. */
export const getNeighbourNextHop = (neighbour: Neighbour): string => {
    const address = neighbour.next_hop ?? '';
    const bytes = parseIPToBytes(address);
    if (!bytes) return address;
    const mapped = bytes.length === 16 && bytes.slice(0, 10).every((byte) => byte === 0)
        && bytes[10] === 255 && bytes[11] === 255;
    return formatIPFromBytes(mapped ? bytes.slice(12) : bytes);
};

/** Identifies one IP/device pair independently of source and mutable payload. */
export const getNeighbourId = (neighbour: Neighbour): string =>
    JSON.stringify([getNeighbourNextHop(neighbour), neighbour.device ?? '']);

/** Expands selected pairs to the IP-wide scope of the removal RPC. */
export const getIPWideRemovalRows = (rows: Neighbour[], selected: Neighbour[]): Neighbour[] => {
    const addresses = new Set(selected.map(getNeighbourNextHop));
    return rows.filter((row) => addresses.has(getNeighbourNextHop(row)));
};

/** Type guard for sortable column names. */
export const isSortableColumn = (value: string): value is SortableColumn =>
    ['next_hop', 'link_addr', 'hardware_addr', 'device', 'state', 'source', 'priority', 'updated_at'].includes(value);

/** Type guard for sort direction values. */
export const isSortDirection = (value: string): value is 'asc' | 'desc' =>
    value === 'asc' || value === 'desc';

/** Validates a MAC address string. Returns an error message or undefined when valid. */
export const validateMAC = (value: string, options?: { required?: boolean }): string | undefined => {
    const trimmed = value.trim();
    if (!trimmed) {
        return options?.required ? 'MAC address is required' : undefined;
    }
    if (!isValidMAC(trimmed)) return 'Invalid MAC address (expected xx:xx:xx:xx:xx:xx)';
    return undefined;
};

/** Validates a next-hop IP address string. Returns an error message or undefined when valid. */
export const validateNextHop = (value: string): string | undefined => {
    if (!value.trim()) return 'Next Hop is required';
    if (!stringToIPAddress(value.trim())) return 'Invalid IP address';
    return undefined;
};

/** Sort comparators for each sortable neighbour column. */
export const sortComparators: Record<SortableColumn, (a: Neighbour, b: Neighbour) => number> = {
    next_hop: (a, b) =>
        compareNullableStrings(a.next_hop || undefined, b.next_hop || undefined),
    link_addr: (a, b) =>
        compareMACAddressValues(
            getMACAddressValue(a.link_addr),
            getMACAddressValue(b.link_addr),
        ),
    hardware_addr: (a, b) =>
        compareMACAddressValues(
            getMACAddressValue(a.hardware_addr),
            getMACAddressValue(b.hardware_addr),
        ),
    device: (a, b) => compareNullableStrings(a.device, b.device),
    state: (a, b) => {
        const stateA = a.state ?? 0;
        const stateB = b.state ?? 0;
        if (stateA !== stateB) return stateA - stateB;
        return compareNullableStrings(a.next_hop || undefined, b.next_hop || undefined);
    },
    source: (a, b) => compareNullableStrings(a.source, b.source),
    priority: (a, b) => (a.priority ?? 0) - (b.priority ?? 0),
    updated_at: (a, b) =>
        compareNullableNumbers(
            getUnixSecondsValue(a.updated_at),
            getUnixSecondsValue(b.updated_at),
        ),
};
