import type { Command } from './types';

/** Options for building the standard config management command trio. */
export interface ConfigCommandsOptions {
    /** Currently active config name; falsy means no config is selected. */
    currentConfig: string;
    /** All draft config names, including the active one. */
    draftConfigs: string[];
    /** Set of config names that have unsaved changes. */
    dirtySet: Set<string>;
    /** Optional secondary line for the Add config item. */
    addConfigSub?: string;
    /** Whether to attach keywords to the Add and Delete items for fuzzy search. */
    withKeywords?: boolean;
    /** Called when the user selects "Add config". */
    onAddConfig: () => void;
    /** When true the "Add config" command is omitted from the palette. */
    addConfigDisabled?: boolean;
    /** Called when the user selects "Delete config". */
    onDeleteConfig: () => void;
    /** Called with the target config name when the user selects a switch item. */
    onSwitchConfig: (name: string) => void;
}

/**
 * Builds the standard Add / Delete / Switch config command trio for module pages.
 *
 * Switch items always carry keywords regardless of withKeywords, matching the
 * behavior common to all four module pages that use this pattern.
 */
export const buildConfigCommands = (options: ConfigCommandsOptions): Command[] => {
    const {
        currentConfig,
        draftConfigs,
        dirtySet,
        addConfigSub,
        withKeywords,
        onAddConfig,
        addConfigDisabled,
        onDeleteConfig,
        onSwitchConfig,
    } = options;

    const list: Command[] = [];

    if (!addConfigDisabled) {
        list.push({
            id: '__add_config',
            icon: '▤',
            label: 'Add config',
            sub: addConfigSub,
            keywords: withKeywords ? 'add config create new' : undefined,
            onSelect: onAddConfig,
        });
    }

    if (currentConfig) {
        list.push({
            id: '__delete_config',
            icon: '✕',
            label: 'Delete config',
            sub: `Delete "${currentConfig}"`,
            keywords: withKeywords ? 'delete remove config' : undefined,
            onSelect: onDeleteConfig,
        });
    }

    for (const cfg of draftConfigs) {
        if (cfg === currentConfig) continue;
        const name = cfg;
        list.push({
            id: `__config_${name}`,
            icon: '⇥',
            label: `Switch to config ${name}`,
            sub: dirtySet.has(name) ? 'unsaved changes' : undefined,
            keywords: `switch config tab ${name}`,
            onSelect: () => onSwitchConfig(name),
        });
    }

    return list;
};
