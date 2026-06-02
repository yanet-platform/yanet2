import React from 'react';
import { ChevronDownIcon } from '../icons';

interface LaneCollapseButtonProps {
    prefix: 'fn' | 'pl';
    collapsed: boolean;
    onToggle: () => void;
    expandLabel: string;
    collapseLabel: string;
}

export const LaneCollapseButton = ({
    prefix,
    collapsed,
    onToggle,
    expandLabel,
    collapseLabel,
}: LaneCollapseButtonProps): React.JSX.Element => (
    <button
        className={`${prefix}-card-header__collapse-btn`}
        onClick={onToggle}
        type="button"
        aria-expanded={!collapsed}
        aria-label={collapsed ? expandLabel : collapseLabel}
    >
        <span
            className={`${prefix}-card-header__chevron${collapsed ? '' : ` ${prefix}-card-header__chevron--open`}`}
        >
            <ChevronDownIcon />
        </span>
    </button>
);
