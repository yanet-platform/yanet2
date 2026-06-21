import { Shield } from '@gravity-ui/icons';
import type { PageManifest } from '@yanet/core/registry';

export const manifest: PageManifest = {
    id: 'modules/acl',
    route: '/modules/acl',
    section: 'Modules',
    title: 'ACL',
    icon: Shield,
    navOrder: 3,
    load: () => import('./AclPage'),
    redirects: ['/acl'],
};
