import type { Command } from '../components/command-palette/types';
import type { Gateway } from './types';

/**
 * Builds the "Gateways" command group for the global command palette.
 *
 * Returns an "Edit gateways…" command first, followed by one "Switch gateway:"
 * command per gateway. The currently-active gateway is labelled with "(active)".
 */
export const gatewayCommands = (
    gateways: Gateway[],
    activeId: string | null,
    onSetActive: (id: string) => void,
    onOpenDrawer: () => void,
): Command[] => {
    const editCmd: Command = {
        id: '__gw_edit',
        icon: '⚙',
        label: 'Edit gateways…',
        keywords: 'gateways manage edit endpoints control plane add',
        group: 'Gateways',
        onSelect: () => onOpenDrawer(),
    };

    const switchCmds: Command[] = gateways
        .filter((gw) => gw.status !== 'offline')
        .map((gw) => {
            const isActive = gw.id === activeId;
            const label = isActive
                ? `Switch gateway: ${gw.host} · NUMA ${gw.numa} (active)`
                : `Switch gateway: ${gw.host} · NUMA ${gw.numa}`;
            return {
                id: `__gw_switch_${gw.id}`,
                icon: '⇄',
                label,
                sub: `${gw.addr} · ${gw.status}`,
                keywords: `gateway switch select activate ${gw.host} NUMA ${gw.numa} ${gw.addr}`,
                group: 'Gateways',
                onSelect: () => onSetActive(gw.id),
            };
        });

    return [editCmd, ...switchCmds];
};
