import { CirclePlay } from '@gravity-ui/icons';
import type { PageManifest } from '@yanet/core/registry';

export const manifest: PageManifest = {
    id: 'modules/pdump',
    route: '/modules/pdump',
    section: 'Modules',
    title: 'Pdump',
    icon: CirclePlay,
    navOrder: 5,
    load: () => import('./PdumpPage'),
    redirects: ['/pdump'],
};
