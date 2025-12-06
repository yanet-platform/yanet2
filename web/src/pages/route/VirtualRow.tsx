import { Checkbox } from '@gravity-ui/uikit';
import type { Route } from '../../api/routes';
import { cellStyles, TOTAL_WIDTH, ROW_HEIGHT, ROUTE_SOURCES } from './constants';

export interface VirtualRowProps {
    route: Route;
    index: number;
    start: number;
    isSelected: boolean;
    onSelect: (route: Route, checked: boolean) => void;
}

export const VirtualRow = ({ route, index, start, isSelected, onSelect }: VirtualRowProps) => {
    const handleCheckboxChange = (checked: boolean) => {
        onSelect(route, checked);
    };

    return (
        <div
            style={{
                position: 'absolute',
                top: 0,
                left: 0,
                width: '100%',
                minWidth: TOTAL_WIDTH,
                height: ROW_HEIGHT,
                transform: `translateY(${start}px)`,
                display: 'flex',
                alignItems: 'center',
                padding: '0 8px',
                borderBottom: '1px solid var(--g-color-line-generic)',
                backgroundColor: isSelected
                    ? 'var(--g-color-base-selection)'
                    : index % 2 === 0
                        ? 'transparent'
                        : 'var(--g-color-base-generic-ultralight)',
                boxSizing: 'border-box',
            }}
        >
            <div style={cellStyles.checkbox}>
                <Checkbox checked={isSelected} onUpdate={handleCheckboxChange} />
            </div>
            <div style={cellStyles.index}>{index + 1}</div>
            <div style={cellStyles.prefix}>{route.prefix || '-'}</div>
            <div style={cellStyles.nextHop}>{route.nextHop || '-'}</div>
            <div style={cellStyles.peer}>{route.peer || '-'}</div>
            <div style={cellStyles.isBest}>{route.isBest ? 'Yes' : 'No'}</div>
            <div style={cellStyles.pref}>{route.pref ?? '-'}</div>
            <div style={cellStyles.asPathLen}>{route.asPathLen ?? '-'}</div>
            <div style={cellStyles.source}>{ROUTE_SOURCES[route.source ?? 0] || 'Unknown'}</div>
        </div>
    );
};
