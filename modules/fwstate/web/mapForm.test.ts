import { describe, it, expect } from 'vitest';
import {
    EMPTY_SIZING,
    MAX_WORKER_COUNT,
    toUint,
    validateWorkerCount,
} from './mapForm';

describe('mapForm defaults', () => {
    it('seeds the sizing fields with the recommended defaults', () => {
        expect(EMPTY_SIZING.indexSize).toBe('1048576');
        expect(EMPTY_SIZING.extraBucketCount).toBe('1024');
        expect(EMPTY_SIZING.workerCount).toBe('1');
    });

    it('exposes the server-side uint16 worker ceiling', () => {
        expect(MAX_WORKER_COUNT).toBe(65535);
    });
});

describe('validateWorkerCount', () => {
    it('accepts the minimum valid worker count', () => {
        expect(validateWorkerCount('1')).toBeUndefined();
    });

    it('accepts the maximum valid worker count', () => {
        expect(validateWorkerCount(String(MAX_WORKER_COUNT))).toBeUndefined();
    });

    it('rejects zero because the server requires at least one worker', () => {
        expect(validateWorkerCount('0')).toMatch(/integer ≥ 1/);
    });

    it('rejects values above the uint16 ceiling', () => {
        const err = validateWorkerCount(String(MAX_WORKER_COUNT + 1));
        expect(err).toMatch(new RegExp(String(MAX_WORKER_COUNT)));
        expect(err).toMatch(/must not exceed/);
    });

    it('rejects non-integer input', () => {
        expect(validateWorkerCount('1.5')).toMatch(/integer ≥ 1/);
    });

    it('rejects empty and non-numeric input', () => {
        expect(validateWorkerCount('')).toMatch(/integer ≥ 1/);
        expect(validateWorkerCount('abc')).toMatch(/integer ≥ 1/);
    });
});

describe('toUint', () => {
    it('parses plain integer strings', () => {
        expect(toUint('42')).toBe(42);
        expect(toUint('0')).toBe(0);
    });

    it('returns zero for negative values', () => {
        expect(toUint('-5')).toBe(0);
    });

    it('returns zero for non-integer or non-numeric input', () => {
        expect(toUint('1.5')).toBe(0);
        expect(toUint('abc')).toBe(0);
        expect(toUint('')).toBe(0);
    });

    it('clamps to the uint32 ceiling rather than overflowing', () => {
        expect(toUint(String(4_294_967_295))).toBe(4_294_967_295);
        expect(toUint(String(4_294_967_296))).toBe(4_294_967_295);
    });
});
