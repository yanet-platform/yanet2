import { Checkbox, Button, Icon } from '@gravity-ui/uikit';
import { Pencil } from '@gravity-ui/icons';
import type { Neighbour } from '../../api/neighbours';
import { getNUDStateString } from '../../utils/nud';
import { formatUnixSeconds } from '../../utils/sorting';
import { cellStyles, TOTAL_WIDTH, ROW_HEIGHT } from './constants';

export interface NeighbourVirtualRowProps {
    neighbour: Neighbour;
    index: number;
    start: number;
    isSelected: boolean;
    onSelect: (neighbour: Neighbour, checked: boolean) => void;
    onEdit?: (neighbour: Neighbour) => void;
}

const renderMac = (addr?: { addr?: string }): string => {
    return addr?.addr || '-';
};

export const NeighbourVirtualRow = ({
    neighbour,
    index,
    start,
    isSelected,
    onSelect,
    onEdit,
}: NeighbourVirtualRowProps) => {
    const handleCheckboxChange = (checked: boolean) => {
        onSelect(neighbour, checked);
    };

    const handleEditClick = () => {
        onEdit?.(neighbour);
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
            <div style={cellStyles.next_hop}>{neighbour.next_hop || '-'}</div>
            <div style={cellStyles.link_addr}>{renderMac(neighbour.link_addr)}</div>
            <div style={cellStyles.hardware_addr}>{renderMac(neighbour.hardware_addr)}</div>
            <div style={cellStyles.device}>{neighbour.device || '-'}</div>
            <div style={cellStyles.state}>{getNUDStateString(neighbour.state)}</div>
            <div style={cellStyles.source}>{neighbour.source || '-'}</div>
            <div style={cellStyles.priority}>{neighbour.priority?.toString() ?? '-'}</div>
            <div style={cellStyles.updated_at}>{formatUnixSeconds(neighbour.updated_at)}</div>
            <div style={cellStyles.actions}>
                <Button
                    view="flat"
                    size="s"
                    onClick={handleEditClick}
                >
                    <Icon data={Pencil} size={16} />
                </Button>
            </div>
        </div>
    );
};
