import React from 'react';
import { Box, Text, Icon } from '@gravity-ui/uikit';
import { ArrowRight } from '@gravity-ui/icons';
import type { InstanceInfo, FunctionInfo, FunctionChainInfo } from '../../../api/inspect';
import { formatUint64 } from '../utils';
import './FunctionsSection.scss';

export interface FunctionsSectionProps {
    instance: InstanceInfo;
}

const Arrow: React.FC = () => (
    <span className="function-arrow">
        <Icon data={ArrowRight} size={14} />
    </span>
);

interface ModulesFlowProps {
    chain: FunctionChainInfo;
}

const ModulesFlow: React.FC<ModulesFlowProps> = ({ chain }) => {
    const modules = chain.modules ?? [];

    if (modules.length === 0) {
        return <Text variant="body-2" color="secondary">-</Text>;
    }

    return (
        <Box className="function-modules-flow">
            {modules.map((mod, idx) => (
                <React.Fragment key={`${chain.name}-mod-${idx}`}>
                    <Box className="function-module">
                        <Text variant="caption-2">{mod.name || mod.type || 'module'}</Text>
                    </Box>
                    {idx < modules.length - 1 && <Arrow />}
                </React.Fragment>
            ))}
        </Box>
    );
};

interface ChainRowProps {
    chain: FunctionChainInfo;
}

const ChainRow: React.FC<ChainRowProps> = ({ chain }) => {
    const weight = formatUint64(chain.weight);

    return (
        <Box className="function-chain-row">
            <Text variant="body-2" className="function-chain__name">
                {chain.name || 'unnamed'}
            </Text>
            <span className="function-chain__separator" />
            <Text variant="body-2" className="function-chain__weight">
                {weight}
            </Text>
            <Box className="function-chain__modules">
                <ModulesFlow chain={chain} />
            </Box>
        </Box>
    );
};

interface FunctionCardProps {
    func: FunctionInfo;
}

const FunctionCard: React.FC<FunctionCardProps> = ({ func }) => {
    const chains = func.chains ?? [];

    return (
        <Box className="function-card">
            <Text variant="body-1" className="function-card__name">
                {func.name || 'unnamed'}
            </Text>
            {chains.length > 0 ? (
                <Box className="function-chains-list">
                    {chains.map((chain, idx) => (
                        <ChainRow key={chain.name ?? idx} chain={chain} />
                    ))}
                </Box>
            ) : (
                <Text variant="body-2" color="secondary" className="function-no-chains">
                    No chains
                </Text>
            )}
        </Box>
    );
};

export const FunctionsSection: React.FC<FunctionsSectionProps> = ({ instance }) => {
    const functions = instance.functions ?? [];

    return (
        <Box className="inspect-section-box">
            <Text variant="header-1">
                Functions
            </Text>
            {functions.length > 0 ? (
                <Box className="functions-list">
                    {functions.map((func, idx) => (
                        <FunctionCard key={func.name ?? idx} func={func} />
                    ))}
                </Box>
            ) : (
                <Text variant="body-1" color="secondary" className="inspect-text--block">
                    No functions
                </Text>
            )}
        </Box>
    );
};
