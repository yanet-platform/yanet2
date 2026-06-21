import { lazy, type ComponentType, type LazyExoticComponent } from 'react';
import type { NavItem, NavSection, PageManifest } from '@yanet/core/registry';

const SECTION_ORDER: Record<NavSection, number> = {
    Builtin: 0,
    Modules: 1,
    Operators: 2,
};

const discovered = import.meta.glob<PageManifest>(
    [
        './pages/**/manifest.ts',
        '../../modules/*/web/manifest.ts',
        '../../devices/*/web/manifest.ts',
        '../../operators/*/web/manifest.ts',
    ],
    { eager: true, import: 'manifest' },
);

/** All page manifests discovered under pages/, ordered for the sidebar. */
export const manifests: PageManifest[] = Object.values(discovered).sort((a, b) => {
    const bySection = SECTION_ORDER[a.section] - SECTION_ORDER[b.section];
    return bySection !== 0 ? bySection : a.navOrder - b.navOrder;
});

/** A discovered page paired with its lazily-loaded body component.
 *
 * The lazy component is created once here so its identity is stable across
 * renders and React keeps the mounted page instead of remounting it.
 */
export interface RouteEntry {
    manifest: PageManifest;
    Component: LazyExoticComponent<ComponentType>;
}

export const routes: RouteEntry[] = manifests.map((manifest) => ({
    manifest,
    Component: lazy(manifest.load),
}));

/** Sidebar entries derived from the non-hidden manifests, in display order. */
export const navItems: NavItem[] = manifests
    .filter((manifest) => !manifest.hideFromNav)
    .map(({ id, title, section, icon }) => ({ id, title, section, icon }));

/** Page loaders to warm during idle after first paint. */
export const prefetchers: Array<PageManifest['load']> = manifests
    .filter((manifest) => manifest.idlePrefetch !== false)
    .map((manifest) => manifest.load);

/** Route to redirect the index path to; falls back to the dashboard. */
export const defaultRoute: string =
    manifests.find((manifest) => manifest.isDefault)?.route ?? '/builtin/dashboard';
