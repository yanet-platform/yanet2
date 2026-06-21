import { describe, expect, it } from 'vitest';
import { PAGE_IDS } from './types';
import { manifests, defaultRoute } from './registry';

describe('page registry', () => {
    it('discovers exactly one manifest per PAGE_ID and vice versa', () => {
        const manifestIds = manifests.map((m) => m.id).sort();
        const pageIds = [...PAGE_IDS].sort();
        expect(manifestIds).toEqual(pageIds);
    });

    it('has no duplicate manifest ids', () => {
        const ids = manifests.map((m) => m.id);
        expect(new Set(ids).size).toBe(ids.length);
    });

    it('each manifest route is its id prefixed with a slash', () => {
        for (const m of manifests) {
            expect(m.route).toBe(`/${m.id}`);
        }
    });

    it('has a single default page whose route is the default route', () => {
        const defaults = manifests.filter((m) => m.isDefault);
        expect(defaults.length).toBe(1);
        expect(defaultRoute).toBe(defaults[0].route);
    });
});
