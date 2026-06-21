import { House } from '@gravity-ui/icons';
import type { PageManifest } from '@yanet/core/registry';

export const manifest: PageManifest = {
    id: 'builtin/dashboard',
    route: '/builtin/dashboard',
    section: 'Builtin',
    title: 'Dashboard',
    icon: House,
    navOrder: 0,
    load: () => import('./DashboardPage'),
    hideFromNav: true,
    isDefault: true,
    redirects: ['/dashboard'],
};
