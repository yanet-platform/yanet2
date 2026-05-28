import React from 'react';
import { Button, Icon } from '@gravity-ui/uikit';
import { Plus } from '@gravity-ui/icons';

interface PdumpConfigTabsProps {
    /** Ordered list of config names. */
    configs: string[];
    /** Currently active config name. */
    activeConfig: string;
    /** Packet counts keyed by config name. */
    counts: Map<string, number>;
    /** Config name currently streaming (shows pulsing green dot). */
    liveConfig: string | null;
    /** Called when the user selects a tab. */
    onSelect: (configName: string) => void;
    /** Called when the user clicks the "+" add-config button. */
    onAddConfig: () => void;
}

/**
 * Tab strip for pdump configs. Like ConfigTabStrip but adds a pulsing live
 * indicator dot on the tab whose capture is currently streaming.
 */
const PdumpConfigTabs: React.FC<PdumpConfigTabsProps> = ({
    configs,
    activeConfig,
    counts,
    liveConfig,
    onSelect,
    onAddConfig,
}) => (
    <div className="fw-tabs" role="tablist">
        {configs.map((cfg) => {
            const isActive = cfg === activeConfig;
            const isLive = cfg === liveConfig;
            return (
                <button
                    key={cfg}
                    type="button"
                    role="tab"
                    aria-selected={isActive}
                    className={`fw-tab${isActive ? ' fw-tab--active' : ''}`}
                    onClick={() => onSelect(cfg)}
                >
                    {isLive && (
                        <span
                            className="fw-tab__dot fw-tab__dot--live"
                            aria-label="live capture"
                        />
                    )}
                    <span className="fw-tab__label">{cfg}</span>
                    <span className="fw-tab__count">{counts.get(cfg) ?? 0}</span>
                </button>
            );
        })}
        <Button view="flat" size="s" onClick={onAddConfig} className="fw-tabs__add" title="Add config">
            <Icon data={Plus} size={14} />
        </Button>
    </div>
);

export default React.memo(PdumpConfigTabs);
