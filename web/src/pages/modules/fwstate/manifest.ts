import { Shield } from '@gravity-ui/icons';
import type { PageManifest } from '@yanet/core/registry';

export const manifest: PageManifest = {
    id: 'modules/fwstate',
    route: '/modules/fwstate',
    section: 'Modules',
    title: 'FWState',
    icon: Shield,
    navOrder: 4,
    load: () => import('./FWStatePage'),
    redirects: ['/fwstate'],
};
