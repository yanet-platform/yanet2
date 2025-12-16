import React from 'react';
import { Box, Text, Icon } from '@gravity-ui/uikit';
import { ArrowRight } from '@gravity-ui/icons';
import type { InstanceInfo, PipelineInfo } from '../../../api/inspect';
import './PipelinesSection.css';

export interface PipelinesSectionProps {
    instance: InstanceInfo;
}

interface EndpointBadgeProps {
    type: 'rx' | 'tx';
}

const EndpointBadge: React.FC<EndpointBadgeProps> = ({ type }) => (
    <span className={`pipeline-endpoint pipeline-endpoint--${type}`}>
        {type}
    </span>
);

const Arrow: React.FC = () => (
    <span className="pipeline-arrow">
        <Icon data={ArrowRight} size={14} />
    </span>
);

interface PipelineFlowProps {
    pipelineName: string;
    functions?: string[];
}

const PipelineFlow: React.FC<PipelineFlowProps> = ({ pipelineName, functions }) => {
    const funcList = functions ?? [];

    return (
        <Box className="pipeline-flow">
            <EndpointBadge type="rx" />
            <Arrow />
            {funcList.map((func, idx) => (
                <React.Fragment key={`${pipelineName}-${func}-${idx}`}>
                    <Box className="pipeline-function">
                        <Text variant="body-2">{func}</Text>
                    </Box>
                    <Arrow />
                </React.Fragment>
            ))}
            <EndpointBadge type="tx" />
        </Box>
    );
};

const PipelineItem: React.FC<{ pipeline: PipelineInfo; fallbackName: string }> = ({ pipeline, fallbackName }) => {
    const displayName = pipeline.name || fallbackName;

    return (
        <Box className="pipeline-item">
            <Box className="pipeline-row">
                <Text variant="body-1" className="pipeline-title">
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
            <Text variant="body-1" color="secondary" className="pipelines-empty">
                No pipelines
            </Text>
        );
    }

    return (
        <Box className="pipeline-list">
            {pipelines.map((pipeline, idx) => (
                <PipelineItem key={pipeline.name ?? idx} pipeline={pipeline} fallbackName={`pipeline-${idx}`} />
            ))}
        </Box>
    );
};

export const PipelinesSection: React.FC<PipelinesSectionProps> = ({ instance }) => {
    const pipelines = instance.pipelines ?? [];

    return (
        <Box className="inspect-section-box">
            <Text variant="header-1">
                Pipelines
            </Text>
            <PipelinesContent pipelines={pipelines} />
        </Box>
    );
};
