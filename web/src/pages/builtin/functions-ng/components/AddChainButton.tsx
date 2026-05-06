import React from 'react';

interface AddChainButtonProps {
    onClick: () => void;
}

/**
 * Ghost button below all lanes to append a new chain to the function.
 */
export const AddChainButton: React.FC<AddChainButtonProps> = ({ onClick }) => (
    <button
        className="fng-add-chain-btn"
        onClick={onClick}
        type="button"
        title="Add chain"
        aria-label="Add chain"
    >
        + Chain
    </button>
);
