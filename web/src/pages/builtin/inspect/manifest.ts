import { Eye } from '@gravity-ui/icons';
import type { PageManifest } from '@yanet/core/registry';

export const manifest: PageManifest = {
    id: 'builtin/inspect',
    route: '/builtin/inspect',
    section: 'Builtin',
    title: 'Inspect',
    icon: Eye,
    navOrder: 0,
    load: () => import('./InspectPage'),
    redirects: ['/inspect'],
};
