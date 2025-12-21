import React from 'react';
import { TextInput } from '@gravity-ui/uikit';
import { FormField } from './FormField';
import './common.css';

export interface InputFormFieldProps {
    /** Field label */
    label: string;
    /** Current input value */
    value: string;
    /** Handler called when value changes */
    onChange: (value: string) => void;
    /** Input placeholder text */
    placeholder?: string;
    /** Hint text shown below the input */
    hint?: string;
    /** Whether the field is required */
    required?: boolean;
    /** Validation error message */
    error?: string;
    /** Whether the input is disabled */
    disabled?: boolean;
}

/**
 * Combines FormField with TextInput for a reusable labeled input pattern
 */
export const InputFormField: React.FC<InputFormFieldProps> = ({
    label,
    value,
    onChange,
    placeholder,
    hint,
    required,
    error,
    disabled,
}) => (
    <FormField label={label} required={required} hint={hint}>
        <TextInput
            value={value}
            onUpdate={onChange}
            placeholder={placeholder}
            className="input-form-field__input"
            validationState={error ? 'invalid' : undefined}
            errorMessage={error}
            disabled={disabled}
        />
    </FormField>
);
