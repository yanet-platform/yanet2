import React, { useEffect, useMemo, useState } from 'react';
import { Box, Text, TabProvider, TabList, Tab, TabPanel } from '@gravity-ui/uikit';
import type { TableColumnConfig } from '@gravity-ui/uikit';
import { Cpu } from '@gravity-ui/icons';
import type { InstanceInfo, AgentInfo, AgentInstanceInfo } from '../../../api/inspect';
import { SortableDataTable } from '../../../components';
import { compareBigIntValues, compareNullableNumbers } from '../../../utils/sorting';
import { InspectSection } from '../InspectSection';
import { formatAgentName, formatUint64 } from '../utils';
import '../inspect.scss';

export interface AgentsSectionProps {
    instance: InstanceInfo;
}

export const AgentsSection: React.FC<AgentsSectionProps> = ({ instance }) => {
    const agents = instance.agents ?? [];
    const [activeAgentTab, setActiveAgentTab] = useState<string>('0');

    const totalInstances = useMemo(() => {
        return agents.reduce((sum, agent) => sum + (agent.instances?.length ?? 0), 0);
    }, [agents]);

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
            id: 'memory_limit',
            name: 'Memory Limit',
            meta: {
                sort: (a: AgentInstanceInfo, b: AgentInstanceInfo) => compareBigIntValues(a.memory_limit, b.memory_limit),
            },
            template: (item: AgentInstanceInfo) => formatUint64(item.memory_limit),
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
        if (agents.length > 0) {
            const tabExists = agents.some((_, idx) => String(idx) === activeAgentTab);
            if (!tabExists) {
                setActiveAgentTab('0');
            }
        }
    }, [agents, activeAgentTab]);

    return (
        <InspectSection
            title="Agents"
            icon={Cpu}
            count={totalInstances}
            variant="agents"
            collapsible
            defaultExpanded
        >
            {agents.length > 0 ? (
                <TabProvider value={activeAgentTab} onUpdate={setActiveAgentTab}>
                    <TabList className="agents-section__tabs">
                        {agents.map((agent: AgentInfo, agentIdx: number) => (
                            <Tab key={agentIdx} value={String(agentIdx)}>
                                {agent.name ? formatAgentName(agent.name) : `Agent ${agentIdx}`}
                            </Tab>
                        ))}
                    </TabList>
                    <Box>
                        {agents.map((agent: AgentInfo, agentIdx: number) => (
                            <TabPanel key={agentIdx} value={String(agentIdx)}>
                                {agent.instances && agent.instances.length > 0 ? (
                                    <SortableDataTable
                                        data={agent.instances}
                                        columns={agentColumns}
                                        width="max"
                                    />
                                ) : (
                                    <Text variant="body-1" color="secondary" className="inspect-text--block">
                                        No instances
                                    </Text>
                                )}
                            </TabPanel>
                        ))}
                    </Box>
                </TabProvider>
            ) : (
                <Text variant="body-1" color="secondary" className="inspect-text--block">
                    No agents
                </Text>
            )}
        </InspectSection>
    );
};
