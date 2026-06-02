import React from 'react';
import { Button, Icon } from '@gravity-ui/uikit';
import { Plus } from '@gravity-ui/icons';

export interface ConfigTabStripProps {
    /** Ordered list of config names. */
    configs: string[];
    /** Currently active config name. */
    activeConfig: string;
    /** Item counts keyed by config name (shown as a badge). */
    counts: Map<string, number>;
    /** Config names that have unsaved changes (show dirty dot). */
    dirtyConfigs: Set<string>;
    /** Called when the user selects a tab. */
    onSelect: (configName: string) => void;
    /** Called when the user clicks the "+" add-config button. */
    onAddConfig: () => void;
    /** Accessible label for the "add config" button tooltip. Defaults to "Add config". */
    addLabel?: string;
    /** Optional leading icon renderer — return a node to prepend before the tab label. */
    leadingIcon?: (configName: string) => React.ReactNode;
    /** Optional trailing icon renderer — return a node to append after the tab label, before the count badge. */
    trailingIcon?: (configName: string) => React.ReactNode;
}

/**
 * Horizontal tab strip for multi-config pages. Shows a dirty dot and item count badge
 * per tab, and a "+" button to add a new config.
 * Consumes the yn-* CSS design tokens from forward.scss.
 */
export const ConfigTabStrip: React.FC<ConfigTabStripProps> = ({
    configs,
    activeConfig,
    counts,
    dirtyConfigs,
    onSelect,
    onAddConfig,
    addLabel = 'Add config',
    leadingIcon,
    trailingIcon,
}) => (
    <div className="yn-tabs" role="tablist">
        {configs.map((cfg) => (
            <button
                key={cfg}
                type="button"
                role="tab"
                aria-selected={cfg === activeConfig}
                className={`yn-tab${cfg === activeConfig ? ' yn-tab--active' : ''}${dirtyConfigs.has(cfg) ? ' yn-tab--dirty' : ''}`}
                onClick={() => onSelect(cfg)}
            >
                {leadingIcon?.(cfg)}
                <span className="yn-tab__label">{cfg}</span>
                {dirtyConfigs.has(cfg) && (
                    <span className="yn-tab__dot" aria-label="unsaved changes" />
                )}
                {trailingIcon?.(cfg)}
                <span className="yn-tab__count">{counts.get(cfg) ?? 0}</span>
            </button>
        ))}
        <Button view="flat" size="s" onClick={onAddConfig} className="yn-tabs__add" title={addLabel}>
            <Icon data={Plus} size={14} />
        </Button>
    </div>
);
