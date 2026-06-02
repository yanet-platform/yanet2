import React from 'react';

interface AddSlotButtonProps {
    onClick: () => void;
    className: string;
    label: string;
}

/**
 * Ghost button appended at the end of a lane track to insert a new slot.
 */
export const AddSlotButton: React.FC<AddSlotButtonProps> = ({ onClick, className, label }) => (
    <button
        className={className}
        onClick={onClick}
        type="button"
        title={label}
        aria-label={label}
    >
        +
    </button>
);
