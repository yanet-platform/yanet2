import { ArrowRight } from '@gravity-ui/icons';
import type { PageManifest } from '@yanet/core/registry';

export const manifest: PageManifest = {
    id: 'modules/forward',
    route: '/modules/forward',
    section: 'Modules',
    title: 'Forward',
    icon: ArrowRight,
    navOrder: 0,
    load: () => import('./ForwardPage'),
    redirects: ['/forward'],
};
