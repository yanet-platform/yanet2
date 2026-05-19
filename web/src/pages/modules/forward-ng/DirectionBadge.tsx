import React from 'react';
import { Icon } from '@gravity-ui/uikit';
import { ArrowLeft, ArrowRight } from '@gravity-ui/icons';
import { ForwardMode } from '../../../api/forward';
import { MODE_LABELS } from './types';

interface DirectionBadgeProps {
    mode: ForwardMode;
}

/** Colored pill badge showing direction mode (IN / OUT / NONE) with equal width. */
const DirectionBadge: React.FC<DirectionBadgeProps> = ({ mode }) => {
    let cls = 'fwng-badge-dir';
    if (mode === ForwardMode.IN) cls += ' fwng-badge-dir--in';
    else if (mode === ForwardMode.OUT) cls += ' fwng-badge-dir--out';
    else cls += ' fwng-badge-dir--none';

    return (
        <span className={cls}>
            {mode === ForwardMode.IN && <Icon data={ArrowLeft} size={12} />}
            {mode === ForwardMode.OUT && <Icon data={ArrowRight} size={12} />}
            {MODE_LABELS[mode]}
        </span>
    );
};

export default DirectionBadge;
