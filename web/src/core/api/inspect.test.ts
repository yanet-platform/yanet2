import { describe, it, expect, vi, beforeEach } from 'vitest';
import { inventoryConfigNames, unionConfigNames } from './inspect';

describe('unionConfigNames', () => {
    it('regression: an empty service list plus a non-empty inventory yields the inventory names, so onAllDropped can fire after a restart', () => {
        expect(unionConfigNames([], ['acl0'])).toEqual(['acl0']);
    });

    it('keeps service names first, in their original order', () => {
        expect(unionConfigNames(['b', 'a'], [])).toEqual(['b', 'a']);
    });

    it('keeps service names before inventory-only names', () => {
        expect(unionConfigNames(['b'], ['a'])).toEqual(['b', 'a']);
    });

    it('lists a name present in both sources exactly once', () => {
        expect(unionConfigNames(['a', 'b'], ['a', 'b'])).toEqual(['a', 'b']);
    });

    it('returns the service list unchanged when the inventory is empty', () => {
        expect(unionConfigNames(['a', 'b'], [])).toEqual(['a', 'b']);
    });
});

describe('inventoryConfigNames', () => {
    beforeEach(() => {
        vi.unstubAllGlobals();
    });

    const stubInspectResponse = (body: unknown): void => {
        vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
            ok: true,
            status: 200,
            statusText: 'OK',
            json: async () => body,
        }));
    };

    it('filters cp_configs by the requested module type', async () => {
        stubInspectResponse({
            instance_info: {
                cp_configs: [
                    { type: 'acl', name: 'acl0' },
                    { type: 'route', name: 'route0' },
                ],
            },
        });
        await expect(inventoryConfigNames('acl')).resolves.toEqual(['acl0']);
    });

    it('drops entries with a missing or empty name', async () => {
        stubInspectResponse({
            instance_info: {
                cp_configs: [
                    { type: 'acl', name: '' },
                    { type: 'acl' },
                    { type: 'acl', name: 'acl0' },
                ],
            },
        });
        await expect(inventoryConfigNames('acl')).resolves.toEqual(['acl0']);
    });

    it('returns an empty list when instance_info is absent', async () => {
        stubInspectResponse({});
        await expect(inventoryConfigNames('acl')).resolves.toEqual([]);
    });

    it('returns an empty list when cp_configs is absent', async () => {
        stubInspectResponse({ instance_info: {} });
        await expect(inventoryConfigNames('acl')).resolves.toEqual([]);
    });
});

