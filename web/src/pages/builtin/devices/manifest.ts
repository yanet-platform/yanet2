import { HardDrive } from '@gravity-ui/icons';
import type { PageManifest } from '@yanet/core/registry';

export const manifest: PageManifest = {
    id: 'builtin/devices',
    route: '/builtin/devices',
    section: 'Builtin',
    title: 'Devices',
    icon: HardDrive,
    navOrder: 3,
    load: () => import('./DevicesPage'),
    redirects: ['/devices'],
};
