import { describe, expect, it } from 'vitest';
import { getAvailableModuleTypesFromInspect } from './moduleTypeOptions';

describe('getAvailableModuleTypesFromInspect', () => {
    it('trims, de-duplicates, and sorts module types from inspect', () => {
        const options = getAvailableModuleTypesFromInspect([
            { name: ' route ' },
            { name: 'acl' },
            { name: 'fwstate' },
            { name: 'acl' },
            { name: '  ' },
            {},
            { name: null },
        ]);

        expect(options).toEqual(['acl', 'fwstate', 'route']);
    });

    it('returns an empty list when inspect provides no usable module names', () => {
        const options = getAvailableModuleTypesFromInspect([
            { name: ' ' },
            {},
            { name: null },
        ]);

        expect(options).toEqual([]);
    });
});
