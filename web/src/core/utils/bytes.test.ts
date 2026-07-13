import { describe, it, expect } from 'vitest';
import { base64ToBytes, bytesToBase64, getBytes, formatBytes, extractBytes } from './bytes';

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

describe('extractBytes', () => {
    it('returns undefined for undefined input', () => {
        expect(extractBytes(undefined)).toBeUndefined();
    });

    it('returns undefined for empty string (falsy)', () => {
        expect(extractBytes('')).toBeUndefined();
    });

    it('returns undefined for invalid base64 string', () => {
        expect(extractBytes('!!!not-base64!!!')).toBeUndefined();
    });

    it('decodes a valid base64 string to bytes', () => {
        expect(extractBytes('AAEC')).toEqual([0, 1, 2]);
    });

    it('converts a Uint8Array to a plain number array', () => {
        expect(extractBytes(Uint8Array.from([10, 20]))).toEqual([10, 20]);
    });

    it('returns the same number array reference for a number[] input', () => {
        const input = [1, 2, 3];
        expect(extractBytes(input)).toBe(input);
    });
});

describe('formatBytes', () => {
    it('formats 0 bytes', () => {
        expect(formatBytes(0n)).toBe('0 B');
    });

    it('formats values below 1 KiB', () => {
        expect(formatBytes(1023n)).toBe('1023 B');
    });

    it('formats exactly 1 KiB', () => {
        expect(formatBytes(1024n)).toBe('1.0 KiB');
    });

    it('formats 1.5 KiB', () => {
        expect(formatBytes(1536n)).toBe('1.5 KiB');
    });

    it('formats the last value in the KiB range (1 MiB - 1 byte)', () => {
        expect(formatBytes(1024n * 1024n - 1n)).toBe('1.0 MiB');
    });

    it('formats the last value in the MiB range (1 GiB - 1 byte)', () => {
        expect(formatBytes(1024n * 1024n * 1024n - 1n)).toBe('1.00 GiB');
    });

    it('formats the last value in the GiB range (1 TiB - 1 byte)', () => {
        expect(formatBytes(1024n ** 4n - 1n)).toBe('1.00 TiB');
    });

    it('does not overflow past TiB (5 exabyte-range value stays in TiB)', () => {
        expect(formatBytes(1024n ** 5n)).toBe('1024.00 TiB');
    });

    it('formats exactly 1 MiB', () => {
        expect(formatBytes(1024n * 1024n)).toBe('1.0 MiB');
    });

    it('formats exactly 1 GiB', () => {
        expect(formatBytes(1024n * 1024n * 1024n)).toBe('1.00 GiB');
    });

    it('formats exactly 1 TiB', () => {
        expect(formatBytes(1024n ** 4n)).toBe('1.00 TiB');
    });

    it('formats 5 TiB', () => {
        expect(formatBytes(5n * 1024n ** 4n)).toBe('5.00 TiB');
    });
});
