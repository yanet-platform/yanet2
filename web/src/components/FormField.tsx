import React from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import './common.css';

export interface FormFieldProps {
    label: string;
    required?: boolean;
    hint?: string;
    children: React.ReactNode;
}

export const FormField: React.FC<FormFieldProps> = ({ label, required, hint, children }) => (
    <Box>
        <Text variant="body-1" className="form-field__label">
            {label} {required && <Text color="danger">*</Text>}
        </Text>
        {children}
        {hint && (
            <Text variant="body-1" color="secondary" className="form-field__hint">
                {hint}
            </Text>
        )}
    </Box>
);
