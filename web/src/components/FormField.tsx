import React from 'react';
import { Box, Text } from '@gravity-ui/uikit';

export interface FormFieldProps {
    label: string;
    required?: boolean;
    hint?: string;
    children: React.ReactNode;
}

export const FormField: React.FC<FormFieldProps> = ({ label, required, hint, children }) => (
    <Box>
        <Text variant="body-1" style={{ marginBottom: '8px', display: 'block' }}>
            {label} {required && <Text color="danger">*</Text>}
        </Text>
        {children}
        {hint && (
            <Text variant="body-1" color="secondary" style={{ marginTop: '4px', display: 'block' }}>
                {hint}
            </Text>
        )}
    </Box>
);

