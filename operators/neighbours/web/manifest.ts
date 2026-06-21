import { Link } from '@gravity-ui/icons';
import type { PageManifest } from '@yanet/core/registry';

export const manifest: PageManifest = {
    id: 'operators/neighbours',
    route: '/operators/neighbours',
    section: 'Operators',
    title: 'Neighbours',
    icon: Link,
    navOrder: 1,
    load: () => import('./NeighboursPage'),
    redirects: ['/neighbours'],
};
