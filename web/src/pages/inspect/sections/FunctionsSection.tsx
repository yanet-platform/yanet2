import React, { useState } from 'react';
import { Box, Text, Icon, Label } from '@gravity-ui/uikit';
import { ChevronDown, ChevronRight, CurlyBracketsFunction } from '@gravity-ui/icons';
import type { InstanceInfo, FunctionInfo, FunctionChainInfo, ChainModuleInfo } from '../../../api/inspect';
import { InspectSection } from '../InspectSection';
import { formatUint64 } from '../utils';
import '../inspect.scss';

export interface FunctionsSectionProps {
    instance: InstanceInfo;
}

interface ModuleTagProps {
    module: ChainModuleInfo;
}

const ModuleTag: React.FC<ModuleTagProps> = ({ module }) => {
    const displayName = module.name || module.type || 'module';
    return (
        <Label theme="normal" size="s" className="functions-tree__module-tag">
            {displayName}
        </Label>
    );
};

interface ChainItemProps {
    chain: FunctionChainInfo;
}

const ChainItem: React.FC<ChainItemProps> = ({ chain }) => {
    const modules = chain.modules ?? [];
    const weight = formatUint64(chain.weight);

    return (
        <Box className="functions-tree__chain">
            <Box className="functions-tree__chain-header">
                <Text variant="body-2" className="functions-tree__chain-name">
                    {chain.name || 'unnamed'}
                </Text>
                <Label theme="info" size="xs">weight: {weight}</Label>
            </Box>
            {modules.length > 0 && (
                <Box className="functions-tree__modules">
                    {modules.map((mod, idx) => (
                        <React.Fragment key={`mod-${idx}`}>
                            <ModuleTag module={mod} />
                            {idx < modules.length - 1 && (
                                <Text variant="caption-2" color="secondary" className="functions-tree__arrow">→</Text>
                            )}
                        </React.Fragment>
                    ))}
                </Box>
            )}
        </Box>
    );
};

interface FunctionItemProps {
    func: FunctionInfo;
    defaultExpanded?: boolean;
}

const FunctionItem: React.FC<FunctionItemProps> = ({ func, defaultExpanded = false }) => {
    const [expanded, setExpanded] = useState(defaultExpanded);
    const chains = func.chains ?? [];
    const hasChains = chains.length > 0;

    return (
        <Box className="functions-tree__function">
            <Box
                className={`functions-tree__function-header ${hasChains ? 'functions-tree__function-header--clickable' : ''}`}
                onClick={hasChains ? () => setExpanded(!expanded) : undefined}
            >
                {hasChains && (
                    <Icon
                        data={expanded ? ChevronDown : ChevronRight}
                        size={16}
                        className="functions-tree__chevron"
                    />
                )}
                <Text variant="body-1" className="functions-tree__function-name">
                    {func.name || 'unnamed'}
                </Text>
                <Label theme="clear" size="xs">{chains.length} chains</Label>
            </Box>
            {expanded && hasChains && (
                <Box className="functions-tree__chains">
                    {chains.map((chain, idx) => (
                        <ChainItem key={chain.name ?? idx} chain={chain} />
                    ))}
                </Box>
            )}
        </Box>
    );
};

export const FunctionsSection: React.FC<FunctionsSectionProps> = ({ instance }) => {
    const functions = instance.functions ?? [];

    return (
        <InspectSection
            title="Functions"
            icon={CurlyBracketsFunction}
            count={functions.length}
            collapsible
            defaultExpanded
        >
            {functions.length > 0 ? (
                <Box className="functions-tree">
                    {functions.map((func, idx) => (
                        <FunctionItem
                            key={func.name ?? idx}
                            func={func}
                            defaultExpanded={idx === 0}
                        />
                    ))}
                </Box>
            ) : (
                <Text variant="body-1" color="secondary" className="inspect-text--block">
                    No functions
                </Text>
            )}
        </InspectSection>
    );
};
