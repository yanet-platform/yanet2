import React from 'react';
import { Button, Icon } from '@gravity-ui/uikit';
import { Plus } from '@gravity-ui/icons';

interface AddChainButtonProps {
    onClick: () => void;
}

/**
 * Compact icon-button to append a new chain to the function, rendered in the sub-header bar.
 */
export const AddChainButton: React.FC<AddChainButtonProps> = ({ onClick }) => (
    <Button
        view="outlined-action"
        size="s"
        onClick={onClick}
        title="Add chain"
        aria-label="Add chain"
    >
        <Icon data={Plus} size={14} />
        Chain
    </Button>
);
