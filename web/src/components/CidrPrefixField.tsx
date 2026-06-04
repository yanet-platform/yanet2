import React from 'react';

interface CidrPrefixFieldProps {
    label: string;
    value: string;
    placeholder: string;
    error?: string | null;
    onChange: (value: string) => void;
}

export const CidrPrefixField = ({ label, value, placeholder, error, onChange }: CidrPrefixFieldProps): React.JSX.Element => (
    <div className="yn-field">
        <label className="yn-field__label">
            {label} <span className="yn-field__req">*</span>
        </label>
        <input
            className={`yn-input yn-input--mono${error ? ' yn-input--invalid' : ''}`}
            value={value}
            placeholder={placeholder}
            onChange={(e) => onChange(e.target.value)}
        />
        {error
            ? <span className="yn-field__hint yn-field__error">{error}</span>
            : <span className="yn-field__hint">IPv4 or IPv6 with mask.</span>}
    </div>
);
