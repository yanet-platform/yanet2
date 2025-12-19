import React, { useState, useCallback, useEffect, useRef } from 'react';
import { Dialog, Box, Text, TextInput, Label } from '@gravity-ui/uikit';
import { FileArrowUp } from '@gravity-ui/icons';
import type { UploadYamlDialogProps } from '../types';
import type { Rule } from '../../../api/acl';
import { parseYamlConfig, formatIPNet } from '../yamlParser';
import { ACTION_LABELS } from '../constants';

// Preview component for parsed rules
interface RulesPreviewProps {
    rules: Rule[];
    maxDisplay?: number;
}

const RulesPreview: React.FC<RulesPreviewProps> = ({ rules, maxDisplay = 5 }) => {
    const displayRules = rules.slice(0, maxDisplay);
    const remaining = rules.length - maxDisplay;

    return (
        <Box style={{ marginTop: 16 }}>
            <Text variant="body-2" style={{ display: 'block', marginBottom: 8 }}>
                Preview ({rules.length} rule{rules.length !== 1 ? 's' : ''}):
            </Text>
            <Box
                style={{
                    maxHeight: 200,
                    overflowY: 'auto',
                    border: '1px solid var(--g-color-line-generic)',
                    borderRadius: 8,
                    padding: 12,
                    backgroundColor: 'var(--g-color-base-generic)',
                }}
            >
                {displayRules.map((rule, index) => {
                    const srcs = rule.srcs?.map(formatIPNet).join(', ') || '*';
                    const dsts = rule.dsts?.map(formatIPNet).join(', ') || '*';
                    const action = ACTION_LABELS[rule.action ?? 0];

                    return (
                        <Box
                            key={index}
                            style={{
                                padding: '6px 0',
                                borderBottom: index < displayRules.length - 1 ? '1px solid var(--g-color-line-generic)' : 'none',
                            }}
                        >
                            <Text variant="body-2" style={{ fontFamily: 'monospace', fontSize: 12 }}>
                                #{index + 1}: {srcs} → {dsts}{' '}
                                <Label theme={action === 'PASS' ? 'success' : 'danger'} size="xs">
                                    {action}
                                </Label>
                            </Text>
                        </Box>
                    );
                })}
                {remaining > 0 && (
                    <Text variant="body-2" color="secondary" style={{ marginTop: 8 }}>
                        ... and {remaining} more rule{remaining !== 1 ? 's' : ''}
                    </Text>
                )}
            </Box>
        </Box>
    );
};

export const UploadYamlDialog: React.FC<UploadYamlDialogProps> = ({
    open,
    onClose,
    onConfirm,
    existingConfigs,
}) => {
    const [configName, setConfigName] = useState('');
    const [file, setFile] = useState<File | null>(null);
    const [parsedRules, setParsedRules] = useState<Rule[] | null>(null);
    const [parseError, setParseError] = useState<string | null>(null);
    const [configNameError, setConfigNameError] = useState<string | undefined>();
    const fileInputRef = useRef<HTMLInputElement>(null);

    // Reset form when dialog opens/closes
    useEffect(() => {
        if (open) {
            setConfigName('');
            setFile(null);
            setParsedRules(null);
            setParseError(null);
            setConfigNameError(undefined);
        }
    }, [open]);

    const handleFileSelect = useCallback((event: React.ChangeEvent<HTMLInputElement>) => {
        const selectedFile = event.target.files?.[0];
        if (!selectedFile) return;

        setFile(selectedFile);
        setParsedRules(null);
        setParseError(null);

        // Read and parse file
        const reader = new FileReader();
        reader.onload = (e) => {
            try {
                const content = e.target?.result as string;
                const rules = parseYamlConfig(content);
                setParsedRules(rules);
                setParseError(null);
            } catch (err) {
                setParseError(err instanceof Error ? err.message : 'Failed to parse YAML file');
                setParsedRules(null);
            }
        };
        reader.onerror = () => {
            setParseError('Failed to read file');
            setParsedRules(null);
        };
        reader.readAsText(selectedFile);
    }, []);

    const handleBrowseClick = useCallback(() => {
        fileInputRef.current?.click();
    }, []);

    const validateConfigName = useCallback((name: string): string | undefined => {
        const trimmed = name.trim();
        if (!trimmed) {
            return 'Config name is required';
        }
        return undefined;
    }, []);

    const handleConfigNameChange = useCallback((value: string) => {
        setConfigName(value);
        setConfigNameError(validateConfigName(value));
    }, [validateConfigName]);

    const handleConfirm = useCallback(() => {
        const nameError = validateConfigName(configName);
        if (nameError) {
            setConfigNameError(nameError);
            return;
        }

        if (!parsedRules) {
            return;
        }

        onConfirm(configName.trim(), parsedRules);
    }, [configName, parsedRules, validateConfigName, onConfirm]);

    const isExistingConfig = existingConfigs.includes(configName.trim());
    const canConfirm = configName.trim() && parsedRules && !configNameError;

    return (
        <Dialog open={open} onClose={onClose}>
            <Dialog.Header caption="Create ACL Configuration" />
            <Dialog.Body>
                <Box style={{ display: 'flex', flexDirection: 'column', gap: 16, minWidth: 480 }}>
                    {/* Config name input */}
                    <Box>
                        <Text variant="body-2" style={{ display: 'block', marginBottom: 4 }}>
                            Config Name <Text color="danger">*</Text>
                        </Text>
                        <TextInput
                            value={configName}
                            onUpdate={handleConfigNameChange}
                            placeholder="Enter config name"
                            validationState={configNameError ? 'invalid' : undefined}
                            errorMessage={configNameError}
                            autoFocus
                        />
                        {isExistingConfig && !configNameError && (
                            <Text variant="caption-2" color="warning" style={{ display: 'block', marginTop: 4 }}>
                                This config already exists. Uploading will replace its rules.
                            </Text>
                        )}
                    </Box>

                    {/* File upload */}
                    <Box>
                        <Text variant="body-2" style={{ display: 'block', marginBottom: 4 }}>
                            YAML File <Text color="danger">*</Text>
                        </Text>
                        <input
                            ref={fileInputRef}
                            type="file"
                            accept=".yaml,.yml"
                            onChange={handleFileSelect}
                            style={{ display: 'none' }}
                        />
                        <div
                            style={{
                                padding: 20,
                                display: 'flex',
                                flexDirection: 'column',
                                alignItems: 'center',
                                justifyContent: 'center',
                                border: '2px dashed var(--g-color-line-generic)',
                                borderRadius: 8,
                                cursor: 'pointer',
                                backgroundColor: 'var(--g-color-base-generic)',
                            }}
                            onClick={handleBrowseClick}
                            role="button"
                            tabIndex={0}
                            onKeyDown={(e) => {
                                if (e.key === 'Enter' || e.key === ' ') {
                                    handleBrowseClick();
                                }
                            }}
                        >
                            <FileArrowUp width={32} height={32} style={{ opacity: 0.5, marginBottom: 8 }} />
                            {file ? (
                                <Text variant="body-2">{file.name}</Text>
                            ) : (
                                <Text variant="body-2" color="secondary">
                                    Click to select YAML file
                                </Text>
                            )}
                        </div>
                    </Box>

                    {/* Parse error */}
                    {parseError && (
                        <Box
                            style={{
                                padding: 12,
                                backgroundColor: 'var(--g-color-base-danger-light)',
                                borderRadius: 8,
                            }}
                        >
                            <Text variant="body-2" color="danger">
                                {parseError}
                            </Text>
                        </Box>
                    )}

                    {/* Rules preview */}
                    {parsedRules && <RulesPreview rules={parsedRules} />}
                </Box>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonCancel={onClose}
                onClickButtonApply={handleConfirm}
                textButtonApply={isExistingConfig ? 'Replace Config' : 'Create Config'}
                textButtonCancel="Cancel"
                propsButtonApply={{
                    disabled: !canConfirm,
                    view: isExistingConfig ? 'outlined-warning' : 'action',
                }}
            />
        </Dialog>
    );
};
