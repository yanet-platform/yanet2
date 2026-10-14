import { describe, it, expect } from 'vitest';
import {
    PDUMP_MIN_RING_CAPACITY,
    buildRingOptions,
    isRingSelectionValid,
    resolveRingNameForSubmit,
    ringFitsPdump,
} from './ringSelection';

describe('isRingSelectionValid', () => {
    it('requires a non-blank ring on create and edit alike', () => {
        expect(isRingSelectionValid('')).toBe(false);
        expect(isRingSelectionValid('  ')).toBe(false);
        expect(isRingSelectionValid('ring0')).toBe(true);
    });
});

describe('resolveRingNameForSubmit', () => {
    it('always sends the selected ring on create', () => {
        expect(resolveRingNameForSubmit(true, 'ring0', '')).toBe('ring0');
    });

    it('omits ring_name on edit when the selection matches the bound ring, keeping the binding', () => {
        expect(resolveRingNameForSubmit(false, 'ring0', 'ring0')).toBeUndefined();
    });

    it('sends the new ring on edit when the selection differs from the bound ring', () => {
        expect(resolveRingNameForSubmit(false, 'ring1', 'ring0')).toBe('ring1');
    });
});

describe('ringFitsPdump', () => {
    it('accepts a ring of at least the pdump minimum, whether capacity arrives as a number or a string', () => {
        expect(ringFitsPdump({ name: 'a', capacity: PDUMP_MIN_RING_CAPACITY })).toBe(true);
        expect(ringFitsPdump({ name: 'a', capacity: String(PDUMP_MIN_RING_CAPACITY * 2) })).toBe(true);
    });

    it('rejects a smaller or unknown capacity', () => {
        expect(ringFitsPdump({ name: 'a', capacity: PDUMP_MIN_RING_CAPACITY / 2 })).toBe(false);
        expect(ringFitsPdump({ name: 'a' })).toBe(false);
        expect(ringFitsPdump({ name: 'a', capacity: 'junk' })).toBe(false);
    });
});

describe('buildRingOptions', () => {
    const rings = [
        { name: 'ring10', capacity: PDUMP_MIN_RING_CAPACITY },
        { name: 'small', capacity: 4096 },
        { name: 'ring2', capacity: PDUMP_MIN_RING_CAPACITY },
        { capacity: PDUMP_MIN_RING_CAPACITY },
    ];

    it('sorts by name naturally, skips nameless rings and disables a ring too small for pdump', () => {
        const options = buildRingOptions(rings, true, '');
        expect(options.map((option) => [option.value, option.disabled])).toEqual([
            ['ring2', false],
            ['ring10', false],
            ['small', true],
        ]);
        expect(options[2]?.content).toContain('too small for pdump');
    });

    it('keeps a bound ring missing from the list selectable on edit, not on create', () => {
        expect(buildRingOptions(rings, false, 'gone').map((option) => option.value)).toContain('gone');
        expect(buildRingOptions(rings, true, 'gone').map((option) => option.value)).not.toContain('gone');
    });
});
