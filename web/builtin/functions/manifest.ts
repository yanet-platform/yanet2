import { CurlyBracketsFunction } from '@gravity-ui/icons';
import type { PageManifest } from '@yanet/core/registry';

export const manifest: PageManifest = {
    id: 'builtin/functions',
    route: '/builtin/functions',
    section: 'Builtin',
    title: 'Functions',
    icon: CurlyBracketsFunction,
    navOrder: 1,
    load: () => import('./FunctionsPage'),
    redirects: ['/functions', '/builtin/functions-ng'],
};
