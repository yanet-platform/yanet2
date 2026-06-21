import type { ComponentType } from 'react';
import type { MenuItem } from '@gravity-ui/navigation';
import type { PageId } from '../../types';

/** Sidebar grouping for a page, shown in the order Builtin, Modules, Operators. */
export type NavSection = 'Builtin' | 'Modules' | 'Operators';

/** Static description of one navigable page, discovered at build time.
 *
 * Each page exports one of these as `manifest` from its `manifest.ts`. The
 * shell registry derives the route table, sidebar, legacy redirects, and
 * idle-prefetch set from the discovered manifests, so a page registers itself
 * by existing rather than by editing central lists.
 */
export interface PageManifest {
    /** Stable page id; must be a member of PAGE_IDS. */
    id: PageId;

    /** Primary route path, e.g. /modules/acl. */
    route: string;

    /** Sidebar section and label. */
    section: NavSection;
    title: string;

    /** Sidebar icon. */
    icon: MenuItem['icon'];

    /** Within-section sidebar order; lower comes first. */
    navOrder: number;

    /** Loads the page body; kept lazy so each page stays a separate chunk. */
    load: () => Promise<{ default: ComponentType }>;

    /** Hide from the sidebar, e.g. the default landing page. */
    hideFromNav?: boolean;

    /** Render as the index route at /. */
    isDefault?: boolean;

    /** Idle-prefetch this page's chunk after first paint; defaults to true. */
    idlePrefetch?: boolean;

    /** Legacy paths that should redirect to `route`. */
    redirects?: string[];
}

/** Sidebar projection of a manifest, consumed by the menu and command palette. */
export interface NavItem {
    id: PageId;
    title: string;
    section: NavSection;
    icon: MenuItem['icon'];
}
