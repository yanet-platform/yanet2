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
}

/**
 * Horizontal tab strip for multi-config pages. Shows a dirty dot and item count badge
 * per tab, and a "+" button to add a new config.
 * Consumes the fw-* CSS design tokens from forward.scss.
 */
export const ConfigTabStrip: React.FC<ConfigTabStripProps> = ({
    configs,
    activeConfig,
    counts,
    dirtyConfigs,
    onSelect,
    onAddConfig,
    addLabel = 'Add config',
}) => (
    <div className="fw-tabs" role="tablist">
        {configs.map((cfg) => (
            <button
                key={cfg}
                type="button"
                role="tab"
                aria-selected={cfg === activeConfig}
                className={`fw-tab${cfg === activeConfig ? ' fw-tab--active' : ''}${dirtyConfigs.has(cfg) ? ' fw-tab--dirty' : ''}`}
                onClick={() => onSelect(cfg)}
            >
                <span className="fw-tab__label">{cfg}</span>
                {dirtyConfigs.has(cfg) && (
                    <span className="fw-tab__dot" aria-label="unsaved changes" />
                )}
                <span className="fw-tab__count">{counts.get(cfg) ?? 0}</span>
            </button>
        ))}
        <Button view="flat" size="s" onClick={onAddConfig} className="fw-tabs__add" title={addLabel}>
            <Icon data={Plus} size={14} />
        </Button>
    </div>
);
