import { describe, it, expect } from 'vitest';
import { base64ToBytes, bytesToBase64, getBytes, formatBytes } from './bytes';

describe('base64ToBytes', () => {
    it('decodes a known base64 string to bytes', () => {
        expect(base64ToBytes('AAEC')).toEqual([0, 1, 2]);
    });

    it('returns an empty array for an empty string', () => {
        expect(base64ToBytes('')).toEqual([]);
    });

    it('returns an empty array for invalid base64 input', () => {
        expect(base64ToBytes('!!!not-base64!!!')).toEqual([]);
    });
});

describe('bytesToBase64', () => {
    it('encodes a byte array to a base64 string', () => {
        expect(bytesToBase64([0, 1, 2])).toBe('AAEC');
    });

    it('encodes an empty array to an empty string', () => {
        expect(bytesToBase64([])).toBe('');
    });
});

describe('base64ToBytes / bytesToBase64 round-trip', () => {
    it('round-trips various byte arrays without data loss', () => {
        const cases: number[][] = [[], [0], [255], [1, 2, 3, 4, 5]];
        for (const input of cases) {
            expect(base64ToBytes(bytesToBase64(input))).toEqual(input);
        }
    });
});

describe('getBytes', () => {
    it('returns an empty array for undefined', () => {
        expect(getBytes(undefined)).toEqual([]);
    });

    it('decodes a base64 string', () => {
        expect(getBytes('AAEC')).toEqual([0, 1, 2]);
    });

    it('converts a Uint8Array to a plain number array', () => {
        const result = getBytes(Uint8Array.from([1, 2, 3]));
        expect(result).toEqual([1, 2, 3]);
        expect(Array.isArray(result)).toBe(true);
    });

    it('returns a new array, not the original reference, when given a number array', () => {
        const input = [7, 8, 9];
        const result = getBytes(input);
        expect(result).toEqual([7, 8, 9]);
        expect(result).not.toBe(input);
    });
});

describe('formatBytes', () => {
    it('formats 0 bytes', () => {
        expect(formatBytes(0n)).toBe('0 B');
    });

    it('formats values below 1 KB', () => {
        expect(formatBytes(1023n)).toBe('1023 B');
    });

    it('formats exactly 1 KB', () => {
        expect(formatBytes(1024n)).toBe('1.0 KB');
    });

    it('formats 1.5 KB', () => {
        expect(formatBytes(1536n)).toBe('1.5 KB');
    });

    it('formats the last value in the KB range (1 MB - 1 byte)', () => {
        expect(formatBytes(1024n * 1024n - 1n)).toBe('1.0 MB');
    });

    it('formats the last value in the MB range (1 GB - 1 byte)', () => {
        expect(formatBytes(1024n * 1024n * 1024n - 1n)).toBe('1.00 GB');
    });

    it('formats the last value in the GB range (1 TB - 1 byte)', () => {
        expect(formatBytes(1024n ** 4n - 1n)).toBe('1.00 TB');
    });

    it('does not overflow past TB (5 exabyte-range value stays in TB)', () => {
        expect(formatBytes(1024n ** 5n)).toBe('1024.00 TB');
    });

    it('formats exactly 1 MB', () => {
        expect(formatBytes(1024n * 1024n)).toBe('1.0 MB');
    });

    it('formats exactly 1 GB', () => {
        expect(formatBytes(1024n * 1024n * 1024n)).toBe('1.00 GB');
    });

    it('formats exactly 1 TB', () => {
        expect(formatBytes(1024n ** 4n)).toBe('1.00 TB');
    });

    it('formats 5 TB', () => {
        expect(formatBytes(5n * 1024n ** 4n)).toBe('5.00 TB');
    });
});
