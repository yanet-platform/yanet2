import React from 'react';

interface AddModuleButtonProps {
    onClick: () => void;
}

/**
 * Ghost button appended at the end of a lane track to insert a new module.
 */
export const AddModuleButton: React.FC<AddModuleButtonProps> = ({ onClick }) => (
    <button
        className="fng-add-module-btn"
        onClick={onClick}
        type="button"
        title="Add module"
        aria-label="Add module"
    >
        + module
    </button>
);
