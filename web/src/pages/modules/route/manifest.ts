import { Route } from '@gravity-ui/icons';
import type { PageManifest } from '@yanet/core/registry';

export const manifest: PageManifest = {
    id: 'modules/route',
    route: '/modules/route',
    section: 'Modules',
    title: 'Route',
    icon: Route,
    navOrder: 1,
    load: () => import('./RoutePage'),
};
