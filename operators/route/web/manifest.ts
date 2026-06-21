import { Route } from '@gravity-ui/icons';
import type { PageManifest } from '@yanet/core/registry';

export const manifest: PageManifest = {
    id: 'operators/route',
    route: '/operators/route',
    section: 'Operators',
    title: 'Route',
    icon: Route,
    navOrder: 0,
    load: () => import('./RoutePage'),
    redirects: ['/route'],
};
