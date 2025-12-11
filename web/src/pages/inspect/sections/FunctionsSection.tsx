import React from 'react';
import { Box, Text, Icon } from '@gravity-ui/uikit';
import { ArrowRight } from '@gravity-ui/icons';
import type { InstanceInfo, FunctionInfo, FunctionChainInfo } from '../../../api/inspect';
import { formatUint64 } from '../utils';
import './FunctionsSection.css';

export interface FunctionsSectionProps {
    instance: InstanceInfo;
}

const Arrow: React.FC = () => (
    <span className="functionArrow">
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
        <Box className="functionModulesFlow">
            {modules.map((mod, idx) => (
                <React.Fragment key={`${chain.Name}-mod-${idx}`}>
                    <Box className="functionModule">
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
    const weight = formatUint64(chain.Weight);

    return (
        <Box className="functionChainRow">
            <Text variant="body-2" className="functionChainName">
                {chain.Name || 'unnamed'}
            </Text>
            <span className="functionChainSeparator" />
            <Text variant="body-2" className="functionChainWeight">
                {weight}
            </Text>
            <Box className="functionChainModules">
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
        <Box className="functionCard">
            <Text variant="body-1" className="functionCardName">
                {func.Name || 'unnamed'}
            </Text>
            {chains.length > 0 ? (
                <Box className="functionChainsList">
                    {chains.map((chain, idx) => (
                        <ChainRow key={chain.Name ?? idx} chain={chain} />
                    ))}
                </Box>
            ) : (
                <Text variant="body-2" color="secondary" className="functionNoChains">
                    No chains
                </Text>
            )}
        </Box>
    );
};

export const FunctionsSection: React.FC<FunctionsSectionProps> = ({ instance }) => {
    const functions = instance.functions ?? [];

    return (
        <Box className="functionsSection">
            <Text variant="header-1" className="functionsSectionHeader">
                Functions
            </Text>
            {functions.length > 0 ? (
                <Box className="functionsList">
                    {functions.map((func, idx) => (
                        <FunctionCard key={func.Name ?? idx} func={func} />
                    ))}
                </Box>
            ) : (
                <Text variant="body-1" color="secondary" style={{ display: 'block' }}>
                    No functions
                </Text>
            )}
        </Box>
    );
};
