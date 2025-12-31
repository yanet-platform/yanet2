import React, { useMemo } from 'react';
import { Box, Text, Label } from '@gravity-ui/uikit';
import type { TableColumnConfig } from '@gravity-ui/uikit';
import { ListUl } from '@gravity-ui/icons';
import type { InstanceInfo } from '../../../api/inspect';
import { SortableDataTable } from '../../../components';
import { compareNullableStrings, compareNullableNumbers } from '../../../utils/sorting';
import { InspectSection } from '../InspectSection';
import '../inspect.scss';

export interface PipelinesSectionProps {
    instance: InstanceInfo;
}

interface PipelineRowData {
    [key: string]: unknown;
    name: string;
    functions: string[];
    functionCount: number;
}

export const PipelinesSection: React.FC<PipelinesSectionProps> = ({ instance }) => {
    const pipelines = instance.pipelines ?? [];

    const rowData: PipelineRowData[] = useMemo(() => {
        return pipelines.map((pipeline, idx) => ({
            name: pipeline.name || `pipeline-${idx}`,
            functions: pipeline.functions ?? [],
            functionCount: pipeline.functions?.length ?? 0,
        }));
    }, [pipelines]);

    const columns: TableColumnConfig<PipelineRowData>[] = useMemo(() => [
        {
            id: 'name',
            name: 'Pipeline',
            meta: {
                sort: (a: PipelineRowData, b: PipelineRowData) => compareNullableStrings(a.name, b.name),
            },
            template: (item: PipelineRowData) => (
                <Text variant="body-1" className="pipelines-table__name">
                    {item.name}
                </Text>
            ),
        },
        {
            id: 'functionCount',
            name: 'Functions',
            meta: {
                sort: (a: PipelineRowData, b: PipelineRowData) => compareNullableNumbers(a.functionCount, b.functionCount),
            },
            template: (item: PipelineRowData) => (
                <Label theme="normal" size="s">{item.functionCount}</Label>
            ),
        },
        {
            id: 'flow',
            name: 'Flow',
            template: (item: PipelineRowData) => {
                if (item.functions.length === 0) {
                    return <Text variant="body-2" color="secondary">No functions</Text>;
                }
                return (
                    <Box className="pipelines-table__flow">
                        <Label theme="success" size="s">RX</Label>
                        {item.functions.map((func, idx) => (
                            <React.Fragment key={`${item.name}-${func}-${idx}`}>
                                <Text variant="body-2" color="secondary" className="pipelines-table__arrow">→</Text>
                                <Text variant="body-2">{func}</Text>
                            </React.Fragment>
                        ))}
                        <Text variant="body-2" color="secondary" className="pipelines-table__arrow">→</Text>
                        <Label theme="info" size="s">TX</Label>
                    </Box>
                );
            },
        },
    ], []);

    return (
        <InspectSection
            title="Pipelines"
            icon={ListUl}
            count={pipelines.length}
            collapsible
            defaultExpanded
        >
            {pipelines.length > 0 ? (
                <Box className="pipelines-table-wrapper">
                    <SortableDataTable
                        data={rowData}
                        columns={columns as any}
                        width="max"
                    />
                </Box>
            ) : (
                <Text variant="body-1" color="secondary" className="inspect-text--block">
                    No pipelines
                </Text>
            )}
        </InspectSection>
    );
};
