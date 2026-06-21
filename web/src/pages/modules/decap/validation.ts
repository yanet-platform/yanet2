import { isValidCidrPrefix, rowHasError as sharedRowHasError, countInvalidRows as sharedCountInvalidRows } from '@yanet/core/utils';
import type { PrefixRowItem, PrefixRowErrors } from './types';

/** Validate all fields of a prefix row. Returns null per field if valid. */
export const validateRow = (row: PrefixRowItem): PrefixRowErrors => ({
    prefix: isValidCidrPrefix(row.prefix) ? null : (row.prefix ? 'Invalid CIDR' : 'Required'),
});

/** Returns true if the row has any validation error. */
export const rowHasError = (row: PrefixRowItem): boolean => sharedRowHasError(validateRow(row));

/** Count invalid rows in a list. */
export const countInvalidRows = (rows: PrefixRowItem[]): number =>
    sharedCountInvalidRows(rows, validateRow);
