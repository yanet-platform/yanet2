import { isValidCIDR } from '../../_shared/draft/cidr';
import type { PrefixRowItem, PrefixRowErrors } from './types';

/** Validate all fields of a prefix row. Returns null per field if valid. */
export const validateRow = (row: PrefixRowItem): PrefixRowErrors => ({
    prefix: isValidCIDR(row.prefix) ? null : (row.prefix ? 'Invalid CIDR' : 'Required'),
});

/** Returns true if the row has any validation error. */
export const rowHasError = (row: PrefixRowItem): boolean => {
    const errs = validateRow(row);
    return Object.values(errs).some(Boolean);
};

/** Count invalid rows in a list. */
export const countInvalidRows = (rows: PrefixRowItem[]): number =>
    rows.filter(rowHasError).length;
