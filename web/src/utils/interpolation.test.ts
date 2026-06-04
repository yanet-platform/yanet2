import { describe, it, expect } from 'vitest';
import { lerp, clampProgress, computeRate } from './interpolation';

describe('lerp', () => {
    it('returns a at t=0', () => {
        expect(lerp(0, 100, 0)).toBe(0);
    });

    it('returns b at t=1', () => {
        expect(lerp(0, 100, 1)).toBe(100);
    });

    it('returns midpoint at t=0.5', () => {
        expect(lerp(0, 100, 0.5)).toBe(50);
    });

    it('does not clamp: allows t outside [0,1]', () => {
        expect(lerp(0, 100, 1.5)).toBe(150);
        expect(lerp(0, 100, -0.5)).toBe(-50);
    });
});

describe('clampProgress', () => {
    it('returns 0 at sample time', () => {
        expect(clampProgress(1000, 1000, 500)).toBe(0);
    });

    it('returns 0.5 halfway through interval', () => {
        expect(clampProgress(1250, 1000, 500)).toBe(0.5);
    });

    it('returns 1 at exactly one interval', () => {
        expect(clampProgress(1500, 1000, 500)).toBe(1);
    });

    it('clamps to 1 past the interval', () => {
        expect(clampProgress(2000, 1000, 500)).toBe(1);
    });

    it('clamps to 0 before the sample', () => {
        expect(clampProgress(900, 1000, 500)).toBe(0);
    });

    it('returns 1 when intervalMs is 0', () => {
        expect(clampProgress(1000, 1000, 0)).toBe(1);
    });
});

describe('computeRate', () => {
    it('computes pps and bps from positive deltas', () => {
        const prev = { packets: BigInt(0), bytes: BigInt(0) };
        const cur = { packets: BigInt(1000), bytes: BigInt(125000) };
        const result = computeRate(prev, cur, 1);
        expect(result.pps).toBe(1000);
        expect(result.bps).toBe(125000);
    });

    it('divides by dtSeconds correctly', () => {
        const prev = { packets: BigInt(0), bytes: BigInt(0) };
        const cur = { packets: BigInt(3000), bytes: BigInt(6000) };
        const result = computeRate(prev, cur, 3);
        expect(result.pps).toBe(1000);
        expect(result.bps).toBe(2000);
    });

    it('returns 0 pps on negative packet delta (counter reset)', () => {
        const prev = { packets: BigInt(5000), bytes: BigInt(0) };
        const cur = { packets: BigInt(100), bytes: BigInt(1000) };
        const result = computeRate(prev, cur, 1);
        expect(result.pps).toBe(0);
        expect(result.bps).toBeGreaterThan(0);
    });

    it('returns 0 bps on negative byte delta (counter reset)', () => {
        const prev = { packets: BigInt(0), bytes: BigInt(5000) };
        const cur = { packets: BigInt(100), bytes: BigInt(0) };
        const result = computeRate(prev, cur, 1);
        expect(result.pps).toBeGreaterThan(0);
        expect(result.bps).toBe(0);
    });

    it('returns zeros when dtSeconds is 0', () => {
        const prev = { packets: BigInt(0), bytes: BigInt(0) };
        const cur = { packets: BigInt(1000), bytes: BigInt(1000) };
        const result = computeRate(prev, cur, 0);
        expect(result.pps).toBe(0);
        expect(result.bps).toBe(0);
    });

    it('returns zeros when dtSeconds is negative', () => {
        const prev = { packets: BigInt(0), bytes: BigInt(0) };
        const cur = { packets: BigInt(1000), bytes: BigInt(1000) };
        const result = computeRate(prev, cur, -1);
        expect(result.pps).toBe(0);
        expect(result.bps).toBe(0);
    });
});
