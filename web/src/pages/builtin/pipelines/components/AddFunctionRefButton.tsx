import React from 'react';

interface AddFunctionRefButtonProps {
    onClick: () => void;
}

/**
 * Ghost button appended at the end of a pipeline track to insert a new function reference.
 */
export const AddFunctionRefButton: React.FC<AddFunctionRefButtonProps> = ({ onClick }) => (
    <button
        className="pl-add-ref-btn"
        onClick={onClick}
        type="button"
        title="Add function reference"
        aria-label="Add function reference"
    >
        +
    </button>
);
