import { Eye, CurlyBracketsFunction, ListUl, HardDrive, ArrowRight, Route, LayoutCellsLarge, Shield, CirclePlay, Link } from '@gravity-ui/icons';
import type { MenuItem } from '@gravity-ui/navigation';
import type { PageId } from './types';

export type NavSection = 'Builtin' | 'Modules' | 'Operators';

export interface NavItem {
    id: PageId;
    title: string;
    section: NavSection;
    icon: MenuItem['icon'];
}

/** Ordered list of navigable pages, shared between MainMenu and the command palette. */
export const navItems: NavItem[] = [
    { id: 'builtin/inspect', title: 'Inspect', section: 'Builtin', icon: Eye },
    { id: 'builtin/functions', title: 'Functions', section: 'Builtin', icon: CurlyBracketsFunction },
    { id: 'builtin/pipelines', title: 'Pipelines', section: 'Builtin', icon: ListUl },
    { id: 'builtin/devices', title: 'Devices', section: 'Builtin', icon: HardDrive },
    { id: 'modules/forward', title: 'Forward', section: 'Modules', icon: ArrowRight },
    { id: 'modules/route', title: 'Route', section: 'Modules', icon: Route },
    { id: 'modules/decap', title: 'Decap', section: 'Modules', icon: LayoutCellsLarge },
    { id: 'modules/acl', title: 'ACL', section: 'Modules', icon: Shield },
    { id: 'modules/fwstate', title: 'FWState', section: 'Modules', icon: Shield },
    { id: 'modules/pdump', title: 'Pdump', section: 'Modules', icon: CirclePlay },
    { id: 'operators/route', title: 'Route', section: 'Operators', icon: Route },
    { id: 'operators/neighbours', title: 'Neighbours', section: 'Operators', icon: Link },
];
