import { describe, it, expect } from 'vitest';
import { parseYamlToRules } from './YamlIO';
import { rulesToDiffYaml } from './SaveDiffModal';
import { ForwardMode, type Rule } from '@yanet/core/api/forward';

const sampleRules = (): Rule[] => [
    {
        action: { target: 'virtio_user_kni0', mode: ForwardMode.OUT, counter: 'to_kni0' },
        devices: [{ name: '01:00.0' }],
        vlan_ranges: [{ from: 0, to: 4095 }],
        sources4: ['10.0.0.0/8'],
        sources6: ['fe80::/10'],
        destinations4: ['203.0.113.0/24'],
        destinations6: ['ff02::/16'],
    },
    {
        action: { target: '01:00.0', mode: ForwardMode.NONE, counter: undefined },
        devices: [],
        vlan_ranges: [],
        sources4: [],
        sources6: [],
        destinations4: [],
        destinations6: [],
    },
];

describe('importing a wire rules document', () => {
    it('parses the document the operator and the CLI speak', () => {
        const text = [
            'name: forward0',
            'rules:',
            '  - action:',
            '      target: virtio_user_kni0',
            '      mode: OUT',
            '      counter: to_kni0',
            '    devices:',
            '      - name: "01:00.0"',
            '    vlan_ranges:',
            '      - from: 0',
            '        to: 4095',
            '    sources4:',
            '      - 10.0.0.0/8',
            '    destinations6:',
            '      - ff02::/16',
        ].join('\n');

        const parsed = parseYamlToRules(text);

        expect(parsed.name).toBe('forward0');
        expect(parsed.rules).toHaveLength(1);
        const rule = parsed.rules[0];
        expect(rule.action).toEqual({ target: 'virtio_user_kni0', mode: ForwardMode.OUT, counter: 'to_kni0' });
        expect(rule.devices).toEqual([{ name: '01:00.0' }]);
        expect(rule.vlan_ranges).toEqual([{ from: 0, to: 4095 }]);
        expect(rule.sources4).toEqual(['10.0.0.0/8']);
        expect(rule.sources6).toEqual([]);
        expect(rule.destinations6).toEqual(['ff02::/16']);
    });

    it('round-trips the export back into the same rules', () => {
        const rules = sampleRules();

        const parsed = parseYamlToRules(rulesToDiffYaml(rules));

        expect(parsed.rules).toEqual(rules);
    });

    it('refuses the retired flat schema with a pointer to the wire form', () => {
        const text = 'rules:\n  - target: eth0\n    mode: OUT\n    srcs:\n      - 10.0.0.0/8\n';

        expect(() => parseYamlToRules(text)).toThrow(/retired flat schema/);
    });

    it('refuses an unknown top-level key', () => {
        expect(() => parseYamlToRules('rulez: []\n')).toThrow(/Unknown key "rulez"/);
    });

    it('refuses an undeclared mode name, number and inherited object key', () => {
        expect(() => parseYamlToRules('rules:\n  - action:\n      mode: BOGUS\n')).toThrow(/unknown forward mode/);
        expect(() => parseYamlToRules('rules:\n  - action:\n      mode: 99\n')).toThrow(/unknown forward mode/);
        expect(() => parseYamlToRules('rules:\n  - action:\n      mode: toString\n')).toThrow(/unknown forward mode/);
    });

    it('accepts the bi-contiguous IPv6 mask form the filter compiler supports', () => {
        const rules: Rule[] = [
            {
                action: { target: 't', mode: ForwardMode.OUT, counter: 'c' },
                devices: [],
                vlan_ranges: [],
                sources4: [],
                sources6: ['2001:db8::/ffff:ffff:ffff:0:ffff::'],
                destinations4: [],
                destinations6: [],
            },
        ];

        const parsed = parseYamlToRules(rulesToDiffYaml(rules));

        expect(parsed.rules).toEqual(rules);
    });

    it('accepts bare host networks as the wire decoders do', () => {
        const text = 'rules:\n  - action:\n      target: t\n    sources4:\n      - 192.0.2.1\n    sources6:\n      - 2001:db8::1\n';

        const parsed = parseYamlToRules(text);

        expect(parsed.rules[0].sources4).toEqual(['192.0.2.1']);
        expect(parsed.rules[0].sources6).toEqual(['2001:db8::1']);
    });

    it('refuses a network of the wrong family', () => {
        const text = 'rules:\n  - action:\n      target: t\n    sources4:\n      - fe80::/10\n';

        expect(() => parseYamlToRules(text)).toThrow(/sources4 entry "fe80::\/10"/);
    });

    it('refuses a padded prefix length the wire parser rejects', () => {
        const text = 'rules:\n  - action:\n      target: t\n    sources4:\n      - 10.0.0.0/024\n';

        expect(() => parseYamlToRules(text)).toThrow(/sources4 entry "10.0.0.0\/024"/);
    });

    it('refuses a non-string config name', () => {
        expect(() => parseYamlToRules('name: 123\nrules: []\n')).toThrow(/"name" to be a string/);
    });

    it('refuses a rule without an action', () => {
        expect(() => parseYamlToRules('rules:\n  - devices: []\n')).toThrow(/"action" is required/);
        expect(() => parseYamlToRules('rules:\n  - action: null\n')).toThrow(/"action" is required/);
    });

    it('refuses a sequence where the action mapping is required', () => {
        expect(() => parseYamlToRules('rules:\n  - action: []\n')).toThrow(/"action" is required/);
    });

    it('refuses a scalar document, such as a bare timestamp', () => {
        expect(() => parseYamlToRules('2026-08-31\n')).toThrow(/Expected a YAML object/);
    });

    it('refuses a non-string action target', () => {
        expect(() => parseYamlToRules('rules:\n  - action:\n      target: 123\n')).toThrow(/"target" to be a string/);
    });

    it('refuses a vlan bound that is not an unsigned integer', () => {
        expect(() =>
            parseYamlToRules("rules:\n  - action:\n      target: t\n    vlan_ranges:\n      - from: '100'\n"),
        ).toThrow(/unsigned integer/);
    });

    it('reads null vlan bounds as zero', () => {
        const text = 'rules:\n  - action:\n      target: t\n    vlan_ranges:\n      - from: null\n        to: 100\n';

        const parsed = parseYamlToRules(text);

        expect(parsed.rules[0].vlan_ranges).toEqual([{ from: 0, to: 100 }]);
    });

    it('accepts a declared numeric mode and null fields as zero values', () => {
        const text = 'rules:\n  - action:\n      target: eth0\n      mode: 2\n    devices: null\n    sources4: null\n';

        const parsed = parseYamlToRules(text);

        expect(parsed.rules[0].action?.mode).toBe(ForwardMode.OUT);
        expect(parsed.rules[0].devices).toEqual([]);
        expect(parsed.rules[0].sources4).toEqual([]);
    });

    it('reads an empty document as no rules', () => {
        expect(parseYamlToRules('')).toEqual({ rules: [] });
        expect(parseYamlToRules('# nothing yet\n')).toEqual({ rules: [] });
    });

    it('tolerates a bare trailing separator', () => {
        const parsed = parseYamlToRules('name: forward0\nrules: []\n---\n');

        expect(parsed.name).toBe('forward0');
    });

    it('refuses a second non-empty document', () => {
        expect(() => parseYamlToRules('rules: []\n---\nrules: []\n')).toThrow(/more than one document/);
    });
});

describe('exporting rules to the wire document', () => {
    it('emits the action form with declared mode names', () => {
        const text = rulesToDiffYaml(sampleRules());

        expect(text).toContain('action:');
        expect(text).toContain('mode: OUT');
        expect(text).toContain("- name: '01:00.0'");
        expect(text).not.toContain('srcs');
    });

    it('emits an undeclared mode number as is', () => {
        expect(rulesToDiffYaml([{ action: { target: 't', mode: 99 } }])).toContain('mode: 99');
    });

    it('emits an absent mode as its zero value NONE', () => {
        expect(rulesToDiffYaml([{ action: { target: 't' } }])).toContain('mode: NONE');
    });
});
