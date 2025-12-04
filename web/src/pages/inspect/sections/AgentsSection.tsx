import React, { useEffect, useMemo, useState } from 'react';
import { Box, Text, TabProvider, TabList, Tab, TabPanel } from '@gravity-ui/uikit';
import type { TableColumnConfig } from '@gravity-ui/uikit';
import type { InstanceInfo, AgentInfo, AgentInstanceInfo } from '../../../api/inspect';
import { SortableDataTable } from '../../../components';
import { compareBigIntValues, compareNullableNumbers } from '../../../utils/sorting';
import { formatAgentName, formatUint64 } from '../utils';

export interface AgentsSectionProps {
    instance: InstanceInfo;
}

export const AgentsSection: React.FC<AgentsSectionProps> = ({ instance }) => {
    const [activeAgentTab, setActiveAgentTab] = useState<string>('0');

    const agentColumns: TableColumnConfig<AgentInstanceInfo>[] = useMemo(() => [
        {
            id: 'pid',
            name: 'PID',
            meta: {
                sort: (a: AgentInstanceInfo, b: AgentInstanceInfo) => compareNullableNumbers(a.pid, b.pid),
            },
            template: (item: AgentInstanceInfo) => item.pid?.toString() || '-',
        },
        {
            id: 'memoryLimit',
            name: 'Memory Limit',
            meta: {
                sort: (a: AgentInstanceInfo, b: AgentInstanceInfo) => compareBigIntValues(a.memoryLimit, b.memoryLimit),
            },
            template: (item: AgentInstanceInfo) => formatUint64(item.memoryLimit),
        },
        {
            id: 'allocated',
            name: 'Allocated',
            meta: {
                sort: (a: AgentInstanceInfo, b: AgentInstanceInfo) => compareBigIntValues(a.allocated, b.allocated),
            },
            template: (item: AgentInstanceInfo) => formatUint64(item.allocated),
        },
        {
            id: 'freed',
            name: 'Freed',
            meta: {
                sort: (a: AgentInstanceInfo, b: AgentInstanceInfo) => compareBigIntValues(a.freed, b.freed),
            },
            template: (item: AgentInstanceInfo) => formatUint64(item.freed),
        },
        {
            id: 'generation',
            name: 'Generation',
            meta: {
                sort: (a: AgentInstanceInfo, b: AgentInstanceInfo) => compareBigIntValues(a.generation, b.generation),
            },
            template: (item: AgentInstanceInfo) => formatUint64(item.generation),
        },
    ], []);

    useEffect(() => {
        if (instance.agents && instance.agents.length > 0) {
            const tabExists = instance.agents.some((_, idx) => String(idx) === activeAgentTab);
            if (!tabExists) {
                setActiveAgentTab('0');
            }
        }
    }, [instance.agents, activeAgentTab]);

    return (
        <Box style={{ marginBottom: '24px' }}>
            <Text variant="header-1" style={{ marginBottom: '12px' }}>
                Agents
            </Text>
            {instance.agents && instance.agents.length > 0 ? (
                <TabProvider value={activeAgentTab} onUpdate={setActiveAgentTab}>
                    <TabList style={{ marginBottom: '16px' }}>
                        {instance.agents.map((agent: AgentInfo, agentIdx: number) => (
                            <Tab key={agentIdx} value={String(agentIdx)}>
                                {agent.name ? formatAgentName(agent.name) : `Agent ${agentIdx}`}
                            </Tab>
                        ))}
                    </TabList>
                    <Box>
                        {instance.agents.map((agent: AgentInfo, agentIdx: number) => (
                            <TabPanel key={agentIdx} value={String(agentIdx)}>
                                {agent.instances && agent.instances.length > 0 ? (
                                    <SortableDataTable
                                        data={agent.instances}
                                        columns={agentColumns}
                                        width="max"
                                    />
                                ) : (
                                    <Text variant="body-1" color="secondary" style={{ display: 'block' }}>
                                        No instances
                                    </Text>
                                )}
                            </TabPanel>
                        ))}
                    </Box>
                </TabProvider>
            ) : (
                <Text variant="body-1" color="secondary" style={{ display: 'block' }}>No agents</Text>
            )}
        </Box>
    );
};

