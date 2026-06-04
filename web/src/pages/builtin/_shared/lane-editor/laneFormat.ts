import { formatPps as formatPpsBase } from '../../../../utils/format';

/** Format a pps number with a compact K/M suffix (no unit, finer M precision, capped at M). */
export const formatPps = (v: number): string =>
    formatPpsBase(v, { unit: '', mDecimals: 2, maxUnit: 'M' });
