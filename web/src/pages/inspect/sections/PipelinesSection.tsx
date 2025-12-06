import React from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import type { InstanceInfo, PipelineInfo } from '../../../api/inspect';
import './PipelinesSection.css';

export interface PipelinesSectionProps {
    instance: InstanceInfo;
}

interface PipelineFlowProps {
    pipelineName: string;
    functions?: string[];
}

const PipelineFlow: React.FC<PipelineFlowProps> = ({ pipelineName, functions }) => {
    const segments = ['rx', ...(functions ?? []), 'tx'];

    return (
        <Box className="pipelineFlow">
            {segments.map((segment, idx) => {
                const isFunction = idx > 0 && idx < segments.length - 1;

                return (
                    <React.Fragment key={`${pipelineName}-${segment}-${idx}`}>
                        {isFunction ? (
                            <Box className="pipelineFunction">
                                <Text variant="body-2">{segment}</Text>
                            </Box>
                        ) : (
                            <Text variant="body-2" color="secondary">
                                {segment}
                            </Text>
                        )}
                        {idx < segments.length - 1 && (
                            <Text variant="body-2" color="secondary">
                                -&gt;
                            </Text>
                        )}
                    </React.Fragment>
                );
            })}
        </Box>
    );
};

const PipelineItem: React.FC<{ pipeline: PipelineInfo; fallbackName: string }> = ({ pipeline, fallbackName }) => {
    const displayName = pipeline.name || fallbackName;

    return (
        <Box className="pipelineItem">
            <Box className="pipelineRow">
                <Text variant="body-1" className="pipelineTitle">
                    {displayName}:
                </Text>
                <PipelineFlow pipelineName={displayName} functions={pipeline.functions} />
            </Box>
        </Box>
    );
};

const PipelinesContent: React.FC<{ pipelines: PipelineInfo[] }> = ({ pipelines }) => {
    if (pipelines.length === 0) {
        return (
            <Text variant="body-1" color="secondary" className="pipelinesEmpty">
                No pipelines
            </Text>
        );
    }

    return (
        <Box className="pipelineList">
            {pipelines.map((pipeline, idx) => (
                <PipelineItem key={pipeline.name ?? idx} pipeline={pipeline} fallbackName={`pipeline-${idx}`} />
            ))}
        </Box>
    );
};

export const PipelinesSection: React.FC<PipelinesSectionProps> = ({ instance }) => {
    const pipelines = instance.pipelines ?? [];

    return (
        <Box className="pipelinesSection">
            <Text variant="header-1" className="pipelinesHeader">
                Pipelines
            </Text>
            <PipelinesContent pipelines={pipelines} />
        </Box>
    );
};

