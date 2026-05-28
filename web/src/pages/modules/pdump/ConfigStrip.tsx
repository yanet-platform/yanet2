import React from 'react';
import { parseModeFlags } from '../../../api/pdump';
import { formatPps } from '../../../utils';
import type { PdumpConfigInfo } from './types';
import Sparkline from './Sparkline';

interface ConfigStripProps {
    config: PdumpConfigInfo;
    isCapturing: boolean;
    isCaptureActive: boolean;
    packetCount: number;
    ppsHistory: number[];
    onStartCapture: () => void;
    onStopCapture: () => void;
    onEdit: () => void;
    onDelete: () => void;
}

/**
 * Single-row strip showing mode chips, snaplen, ring size, live stats and action buttons
 * for the currently active pdump config.
 */
const ConfigStrip: React.FC<ConfigStripProps> = ({
    config,
    isCapturing,
    isCaptureActive,
    packetCount,
    ppsHistory,
    onStartCapture,
    onStopCapture,
    onEdit,
    onDelete,
}) => {
    const modes = config.config?.mode ? parseModeFlags(config.config.mode) : [];
    const snaplen = config.config?.snaplen;
    const ringSize = config.config?.ring_size;

    const currentPps = ppsHistory.length > 0 ? (ppsHistory[ppsHistory.length - 1] ?? 0) : 0;

    return (
        <div className="pdump-strip">
            <div className="pdump-strip__meta">
                <div className="pdump-strip__mode-chips">
                    {modes.map(m => (
                        <span
                            key={m}
                            className={`pdump-strip__chip pdump-strip__chip--${m.toLowerCase()}`}
                        >
                            {m}
                        </span>
                    ))}
                    {modes.length === 0 && (
                        <span className="pdump-strip__chip pdump-strip__chip--none">no mode</span>
                    )}
                </div>
                {snaplen !== undefined && (
                    <span className="pdump-strip__param">
                        <span className="pdump-strip__param-label">Snaplen</span>
                        <span className="pdump-strip__param-value">{snaplen}</span>
                    </span>
                )}
                {ringSize !== undefined && (
                    <span className="pdump-strip__param">
                        <span className="pdump-strip__param-label">Ring</span>
                        <span className="pdump-strip__param-value">{ringSize}</span>
                    </span>
                )}
            </div>

            <div className="pdump-live-stats">
                <div className="pdump-stat">
                    <span className="pdump-stat__label">Packets</span>
                    <span className="pdump-stat__value">{packetCount.toLocaleString()}</span>
                </div>
                <div className="pdump-stat">
                    <span className="pdump-stat__label">pps</span>
                    <span className="pdump-stat__value">
                        {isCaptureActive ? formatPps(currentPps) : '--'}
                    </span>
                </div>
                <Sparkline
                    values={isCaptureActive && ppsHistory.length >= 2 ? ppsHistory : null}
                    width={72}
                    height={22}
                />
            </div>

            <div className="pdump-strip__actions">
                <button
                    type="button"
                    className="fw-btn fw-btn--ghost fw-btn--sm"
                    onClick={onEdit}
                    title="Edit configuration"
                >
                    Edit
                </button>
                <button
                    type="button"
                    className="fw-btn fw-btn--ghost fw-btn--sm fw-btn--danger"
                    onClick={onDelete}
                    disabled={isCaptureActive}
                    title="Delete configuration"
                >
                    Delete
                </button>
                {isCaptureActive ? (
                    <button
                        type="button"
                        className="fw-btn fw-btn--sm pdump-strip__stop-btn"
                        onClick={onStopCapture}
                    >
                        Stop
                    </button>
                ) : (
                    <button
                        type="button"
                        className="fw-btn fw-btn--sm pdump-strip__start-btn"
                        onClick={onStartCapture}
                        disabled={isCapturing}
                        title={isCapturing ? 'Another capture is active' : 'Start capture'}
                    >
                        Start
                    </button>
                )}
            </div>
        </div>
    );
};

export default React.memo(ConfigStrip);
