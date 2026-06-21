import { ListUl } from '@gravity-ui/icons';
import type { PageManifest } from '@yanet/core/registry';

export const manifest: PageManifest = {
    id: 'builtin/pipelines',
    route: '/builtin/pipelines',
    section: 'Builtin',
    title: 'Pipelines',
    icon: ListUl,
    navOrder: 2,
    load: () => import('./PipelinesPage'),
    redirects: ['/pipelines'],
};
