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
    <div className="yn-tabs" role="tablist">
        {configs.map((cfg) => {
            const isActive = cfg === activeConfig;
            const isLive = cfg === liveConfig;
            return (
                <button
                    key={cfg}
                    type="button"
                    role="tab"
                    aria-selected={isActive}
                    className={`yn-tab${isActive ? ' yn-tab--active' : ''}`}
                    onClick={() => onSelect(cfg)}
                >
                    {isLive && (
                        <span
                            className="yn-tab__dot yn-tab__dot--live"
                            aria-label="live capture"
                        />
                    )}
                    <span className="yn-tab__label">{cfg}</span>
                    <span className="yn-tab__count">{counts.get(cfg) ?? 0}</span>
                </button>
            );
        })}
        <Button view="flat" size="s" onClick={onAddConfig} className="yn-tabs__add" title="Add config">
            <Icon data={Plus} size={14} />
        </Button>
    </div>
);

export default React.memo(PdumpConfigTabs);
