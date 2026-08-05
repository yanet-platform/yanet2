import { describe, it, expect } from 'vitest';
import { validateRow } from './validation';
import type { FIBRowItem } from './types';

const baseRow: FIBRowItem = {
    id: 'r1',
    from: '10.0.0.0',
    to: '10.0.0.255',
    dst_mac: 'aa:bb:cc:dd:ee:ff',
    src_mac: '11:22:33:44:55:66',
    device: 'eth0',
    counter: '',
};

describe('validateRow range field', () => {
    it('accepts a valid ascending range', () => {
        expect(validateRow(baseRow).range).toBeNull();
    });

    it('rejects a reversed range', () => {
        const row = { ...baseRow, from: '10.0.0.255', to: '10.0.0.0' };
        expect(validateRow(row).range).not.toBeNull();
    });

    it('requires both endpoints when both are empty', () => {
        const row = { ...baseRow, from: '', to: '' };
        expect(validateRow(row).range).toBe('Required');
    });
});

describe('validateRow counter field', () => {
    it('accepts an empty counter as valid', () => {
        expect(validateRow({ ...baseRow, counter: '' }).counter).toBeNull();
    });

    it('accepts a counter that starts with nexthop_', () => {
        expect(validateRow({ ...baseRow, counter: 'nexthop_my-counter' }).counter).toBeNull();
    });

    it('rejects a non-empty counter that does not start with nexthop_', () => {
        expect(validateRow({ ...baseRow, counter: 'my-counter' }).counter).not.toBeNull();
    });

    it('rejects a counter longer than 127 bytes', () => {
        const tooLong = 'nexthop_' + 'a'.repeat(127);
        expect(validateRow({ ...baseRow, counter: tooLong }).counter).not.toBeNull();
    });

    it('rejects a counter at exactly 128 bytes', () => {
        const atLimit = 'nexthop_' + 'a'.repeat(128 - 'nexthop_'.length);
        expect(validateRow({ ...baseRow, counter: atLimit }).counter).not.toBeNull();
    });

    it('accepts a counter at exactly 127 bytes', () => {
        const exact = 'nexthop_' + 'a'.repeat(127 - 'nexthop_'.length);
        expect(validateRow({ ...baseRow, counter: exact }).counter).toBeNull();
    });
});
