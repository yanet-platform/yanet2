import { LayoutCellsLarge } from '@gravity-ui/icons';
import type { PageManifest } from '@yanet/core/registry';

export const manifest: PageManifest = {
    id: 'modules/decap',
    route: '/modules/decap',
    section: 'Modules',
    title: 'Decap',
    icon: LayoutCellsLarge,
    navOrder: 2,
    load: () => import('./DecapPage'),
    redirects: ['/decap'],
};
