import type { PageId } from '../../../types';
import { navItems } from '../../../navItems';
import type { Command } from './types';

/** Builds "Go to" navigation commands from the shared navItems list. */
export const navigationCommands = (onNavigate: (id: PageId) => void): Command[] => {
    const titleCounts = new Map<string, number>();
    for (const item of navItems) {
        titleCounts.set(item.title, (titleCounts.get(item.title) ?? 0) + 1);
    }
    return navItems.map((item) => {
        const isDuplicate = (titleCounts.get(item.title) ?? 0) > 1;
        const label = isDuplicate ? `Go to ${item.title} (${item.section})` : `Go to ${item.title}`;
        return {
            id: `__nav_${item.id}`,
            icon: '→',
            label,
            keywords: `go to navigate ${item.title} ${item.section}`,
            group: 'Go to',
            onSelect: () => onNavigate(item.id),
        };
    });
};
