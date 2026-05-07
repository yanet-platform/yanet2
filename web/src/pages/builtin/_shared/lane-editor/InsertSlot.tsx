import React from 'react';

interface InsertSlotProps {
    idx: number;
    active: boolean;
    hidden: boolean;
}

/**
 * A drop-target slot rendered between module cards during an active drag.
 * Slots themselves have pointer-events: none to avoid dragover flicker;
 * the parent LaneTrack handles dragover geometry.
 */
export const InsertSlot: React.FC<InsertSlotProps> = ({ idx, active, hidden }) => {
    if (hidden) {
        return null;
    }
    return (
        <div
            className={`lane-insert-slot${active ? ' lane-insert-slot--active' : ''}`}
            data-slot-idx={idx}
            aria-hidden="true"
        >
            <div className="lane-insert-slot__inner">
                {active ? '+' : ''}
            </div>
        </div>
    );
};
