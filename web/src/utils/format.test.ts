import { describe, expect, it } from 'vitest';
import { formatBps, formatPps } from './format';

describe('formatPps', () => {
    it('rounds values below 1k and adds suffix', () => {
        expect(formatPps(0)).toBe('0 pps');
        expect(formatPps(285.13421819144713)).toBe('285 pps');
    });

    it('formats values in the K and M ranges with units', () => {
        expect(formatPps(1_234)).toBe('1.2K pps');
        expect(formatPps(1_234_567)).toBe('1.2M pps');
    });

    it('formats values in the G tier by default', () => {
        expect(formatPps(2_000_000_000)).toBe('2.0G pps');
    });

    it('accepts lane options: no unit, 2-decimal M, M-capped', () => {
        expect(formatPps(500, { unit: '', mDecimals: 2, maxUnit: 'M' })).toBe('500');
        expect(formatPps(1_500, { unit: '', mDecimals: 2, maxUnit: 'M' })).toBe('1.5K');
        expect(formatPps(5_000_000, { unit: '', mDecimals: 2, maxUnit: 'M' })).toBe('5.00M');
        expect(formatPps(2_000_000_000, { unit: '', mDecimals: 2, maxUnit: 'M' })).toBe('2000.00M');
    });
});

describe('formatBps', () => {
    it('rounds bytes per second below 1 KB', () => {
        expect(formatBps(285.13421819144713)).toBe('285 B/s');
    });

    it('formats rates with binary suffixes above 1 KB', () => {
        expect(formatBps(1536)).toBe('1.5 KB/s');
        expect(formatBps(1024 * 1024)).toBe('1.0 MB/s');
    });
});
