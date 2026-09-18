import { describe, it, expect } from 'vitest';
import { formatBytes } from './bytes';

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
