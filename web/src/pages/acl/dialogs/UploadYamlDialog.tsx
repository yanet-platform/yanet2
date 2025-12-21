import React, { useState, useCallback, useEffect, useRef } from 'react';
import { Dialog, Box, Text, TextInput, Label } from '@gravity-ui/uikit';
import { FileArrowUp } from '@gravity-ui/icons';
import type { UploadYamlDialogProps } from '../types';
import type { Rule } from '../../../api/acl';
import { parseYamlConfig, formatIPNet } from '../yamlParser';
import { ACTION_LABELS } from '../constants';
import './UploadYamlDialog.css';

// Preview component for parsed rules
interface RulesPreviewProps {
    rules: Rule[];
    maxDisplay?: number;
}

const RulesPreview: React.FC<RulesPreviewProps> = ({ rules, maxDisplay = 5 }) => {
    const displayRules = rules.slice(0, maxDisplay);
    const remaining = rules.length - maxDisplay;

    return (
        <Box className="rules-preview">
            <Text variant="body-2" className="rules-preview__title">
                Preview ({rules.length} rule{rules.length !== 1 ? 's' : ''}):
            </Text>
            <Box className="rules-preview__container">
                {displayRules.map((rule, index) => {
                    const srcs = rule.srcs?.map(formatIPNet).join(', ') || '*';
                    const dsts = rule.dsts?.map(formatIPNet).join(', ') || '*';
                    const action = ACTION_LABELS[rule.action ?? 0];

                    return (
                        <Box
                            key={index}
                            className={`rules-preview__item ${index < displayRules.length - 1 ? 'rules-preview__item--with-border' : ''}`}
                        >
                            <Text variant="body-2" className="rules-preview__rule-text">
                                #{index + 1}: {srcs} → {dsts}{' '}
                                <Label theme={action === 'PASS' ? 'success' : 'danger'} size="xs">
                                    {action}
                                </Label>
                            </Text>
                        </Box>
                    );
                })}
                {remaining > 0 && (
                    <Text variant="body-2" color="secondary" className="rules-preview__more">
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
                <Box className="upload-yaml-dialog__body">
                    {/* Config name input */}
                    <Box>
                        <Text variant="body-2" className="upload-yaml-dialog__label">
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
                            <Text variant="caption-2" color="warning" className="upload-yaml-dialog__warning">
                                This config already exists. Uploading will replace its rules.
                            </Text>
                        )}
                    </Box>

                    {/* File upload */}
                    <Box>
                        <Text variant="body-2" className="upload-yaml-dialog__label">
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
                            className="upload-yaml-dialog__dropzone"
                            onClick={handleBrowseClick}
                            role="button"
                            tabIndex={0}
                            onKeyDown={(e) => {
                                if (e.key === 'Enter' || e.key === ' ') {
                                    handleBrowseClick();
                                }
                            }}
                        >
                            <FileArrowUp width={32} height={32} className="upload-yaml-dialog__dropzone-icon" />
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
                        <Box className="upload-yaml-dialog__error">
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
