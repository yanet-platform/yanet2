import React from 'react';
import { Card, Box, Text, Button, Label, Flex } from '@gravity-ui/uikit';
import { Play, Stop, Pencil } from '@gravity-ui/icons';
import { parseModeFlags } from '../../api/pdump';
import type { PdumpConfigInfo } from './types';
import './pdump.scss';

interface ConfigCardProps {
    config: PdumpConfigInfo;
    isCapturing: boolean;
    isCaptureActive: boolean;
    onStartCapture: () => void;
    onStopCapture: () => void;
    onEdit: () => void;
}

const ModeLabel: React.FC<{ mode: string }> = ({ mode }) => {
    return (
        <>
            <Label
                theme="info"
                size="xs"
            >
                {mode}
            </Label>
        </>
    );
};

export const ConfigCard: React.FC<ConfigCardProps> = ({
    config,
    isCapturing,
    isCaptureActive,
    onStartCapture,
    onStopCapture,
    onEdit,
}) => {
    const modes = config.config?.mode ? parseModeFlags(config.config.mode) : [];
    const filter = config.config?.filter || '(no filter)';

    return (
        <Card theme="normal" className="config-card">
            {/* Header: name + edit button */}
            <Flex justifyContent="space-between" alignItems="center" className="config-card__header">
                <Text variant="subheader-2">{config.name}</Text>
                <Button
                    view="flat"
                    size="xs"
                    onClick={onEdit}
                    title="Edit configuration"
                >
                    <Button.Icon>
                        <Pencil />
                    </Button.Icon>
                </Button>
            </Flex>

            {/* Filter */}
            <Box className="config-card__filter">
                <Text variant="body-1" color="secondary" className="config-card__filter-label">
                    Filter:
                </Text>
                <Text
                    variant="code-1"
                    className="config-card__filter-value"
                    title={filter}
                >
                    {filter}
                </Text>
            </Box>

            {/* Mode */}
            <Box className="config-card__mode">
                <Text variant="body-1" color="secondary" className="config-card__mode-label">
                    Mode:
                </Text>
                <Flex gap={1}>
                    {modes.length > 0 ? (
                        modes.map((mode) => (
                            <ModeLabel key={mode} mode={mode} />
                        ))
                    ) : (
                        <Text variant="body-2" color="hint">none</Text>
                    )}
                </Flex>
            </Box>

            {/* Start/Stop button - full width */}
            {isCaptureActive ? (
                <Button
                    view="outlined-danger"
                    size="m"
                    width="max"
                    onClick={onStopCapture}
                >
                    <Button.Icon>
                        <Stop />
                    </Button.Icon>
                    Stop Capture
                </Button>
            ) : (
                <Button
                    view="outlined-success"
                    size="m"
                    width="max"
                    onClick={onStartCapture}
                    disabled={isCapturing}
                >
                    <Button.Icon>
                        <Play />
                    </Button.Icon>
                    Start Capture
                </Button>
            )}
        </Card>
    );
};
