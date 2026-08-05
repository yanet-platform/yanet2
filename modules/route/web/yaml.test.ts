import { describe, it, expect } from 'vitest';
import { rowsToYaml, yamlToRows } from './yaml';
import type { FIBRowItem } from './types';

const row: FIBRowItem = {
    id: 'r1',
    from: '10.0.0.0',
    to: '10.0.0.255',
    dst_mac: 'aa:bb:cc:dd:ee:ff',
    src_mac: '11:22:33:44:55:66',
    device: 'eth0',
    counter: 'nexthop_custom-name',
};

describe('FIB YAML round trip', () => {
    it('round-trips from/to and counter', () => {
        const yaml = rowsToYaml('cfg0', [row]);
        const rows = yamlToRows(yaml);

        expect(rows).toHaveLength(1);
        expect(rows[0].from).toBe(row.from);
        expect(rows[0].to).toBe(row.to);
        expect(rows[0].counter).toBe(row.counter);
    });

    it('defaults a missing counter to the empty string', () => {
        const yaml = `routes:\n  - from: 10.0.0.0\n    to: 10.0.0.255\n    dst_mac: aa:bb:cc:dd:ee:ff\n    src_mac: 11:22:33:44:55:66\n    device: eth0\n`;
        const rows = yamlToRows(yaml);
        expect(rows[0].counter).toBe('');
    });

    it('rejects a non-string counter instead of silently coercing it to empty', () => {
        const yaml = `routes:\n  - from: 10.0.0.0\n    to: 10.0.0.255\n    dst_mac: aa:bb:cc:dd:ee:ff\n    src_mac: 11:22:33:44:55:66\n    device: eth0\n    counter: 123\n`;
        expect(() => yamlToRows(yaml)).toThrow();
    });

    describe('legacy "prefix" rows (pre-range export)', () => {
        it('converts a valid CIDR prefix to from/to', () => {
            const yaml = `routes:\n  - prefix: 10.0.0.0/24\n    dst_mac: aa:bb:cc:dd:ee:ff\n    src_mac: 11:22:33:44:55:66\n    device: eth0\n`;
            const rows = yamlToRows(yaml);
            expect(rows).toHaveLength(1);
            expect(rows[0].from).toBe('10.0.0.0');
            expect(rows[0].to).toBe('10.0.0.255');
        });

        it('imports an invalid prefix as a flagged row instead of aborting the file', () => {
            const yaml = `routes:\n  - prefix: not-a-cidr\n    dst_mac: aa:bb:cc:dd:ee:ff\n    src_mac: 11:22:33:44:55:66\n    device: eth0\n`;
            const rows = yamlToRows(yaml);
            expect(rows).toHaveLength(1);
            expect(rows[0].from).toBe('not-a-cidr');
            expect(rows[0].to).toBe('');
        });

        it('imports a blank row (no prefix, no from/to) as empty rather than throwing', () => {
            const yaml = `routes:\n  - prefix: ''\n    dst_mac: ''\n    src_mac: ''\n    device: ''\n`;
            const rows = yamlToRows(yaml);
            expect(rows).toHaveLength(1);
            expect(rows[0].from).toBe('');
            expect(rows[0].to).toBe('');
        });

        it('keeps good rows when only one legacy row in the file is bad', () => {
            const yaml = [
                'routes:',
                '  - prefix: 10.0.0.0/24',
                '    dst_mac: aa:bb:cc:dd:ee:ff',
                '    src_mac: 11:22:33:44:55:66',
                '    device: eth0',
                "  - prefix: ''",
                '    dst_mac: ""',
                '    src_mac: ""',
                '    device: ""',
                '  - prefix: garbage',
                '    dst_mac: aa:bb:cc:dd:ee:ff',
                '    src_mac: 11:22:33:44:55:66',
                '    device: eth1',
            ].join('\n');
            const rows = yamlToRows(yaml);
            expect(rows).toHaveLength(3);
            expect(rows[0].from).toBe('10.0.0.0');
            expect(rows[1].from).toBe('');
            expect(rows[2].from).toBe('garbage');
        });
    });
});
