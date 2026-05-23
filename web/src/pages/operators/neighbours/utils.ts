import type { Neighbour } from '../../../api/neighbours';
import {
    compareMACAddressValues,
    compareNullableNumbers,
    compareNullableStrings,
    getMACAddressValue,
    getUnixSecondsValue,
    isValidMAC,
} from '../../../utils';
import { ipAddressToString, stringToIPAddress } from '../../../utils/netip';
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

/** Returns a stable string key for a neighbour row. */
export const getNeighbourId = (n: Neighbour): string => ipAddressToString(n.next_hop);

/** Type guard for sortable column names. */
export const isSortableColumn = (value: string): value is SortableColumn =>
    ['next_hop', 'link_addr', 'hardware_addr', 'device', 'state', 'source', 'priority', 'updated_at'].includes(value);

/** Type guard for sort direction values. */
export const isSortDirection = (value: string): value is 'asc' | 'desc' =>
    value === 'asc' || value === 'desc';

/** Validates a MAC address string. Returns an error message or undefined when valid. */
export const validateMAC = (value: string): string | undefined => {
    if (!value.trim()) return undefined;
    if (!isValidMAC(value.trim())) return 'Invalid MAC address (expected xx:xx:xx:xx:xx:xx)';
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
        compareNullableStrings(
            ipAddressToString(a.next_hop) || undefined,
            ipAddressToString(b.next_hop) || undefined,
        ),
    link_addr: (a, b) =>
        compareMACAddressValues(
            getMACAddressValue(a.link_addr?.addr),
            getMACAddressValue(b.link_addr?.addr),
        ),
    hardware_addr: (a, b) =>
        compareMACAddressValues(
            getMACAddressValue(a.hardware_addr?.addr),
            getMACAddressValue(b.hardware_addr?.addr),
        ),
    device: (a, b) => compareNullableStrings(a.device, b.device),
    state: (a, b) => {
        const stateA = a.state ?? 0;
        const stateB = b.state ?? 0;
        if (stateA !== stateB) return stateA - stateB;
        return compareNullableStrings(
            ipAddressToString(a.next_hop) || undefined,
            ipAddressToString(b.next_hop) || undefined,
        );
    },
    source: (a, b) => compareNullableStrings(a.source, b.source),
    priority: (a, b) => (a.priority ?? 0) - (b.priority ?? 0),
    updated_at: (a, b) =>
        compareNullableNumbers(
            getUnixSecondsValue(a.updated_at),
            getUnixSecondsValue(b.updated_at),
        ),
};
