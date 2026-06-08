import React, { useState, useCallback, useMemo } from 'react';
import { Button, Icon } from '@gravity-ui/uikit';
import { Plus } from '@gravity-ui/icons';
import { PageLayout, PageLoader, EmptyState, CommandPaletteHeader } from '../../../components';
import { usePipelinesData } from './hooks/usePipelinesData';
import { useDragState, useUnsavedChangesBlocker } from '../_shared/lane-editor';
import { PipelineCard } from './components/PipelineCard';
import { CreateEntityDialog } from '../../../components';
import type { Pipeline } from './types';
import type { Command, RowAdapter, ShortcutSection, PagePaletteContribution } from '../../../components/command-palette';
import { useLaneCardNavigation, usePageContribution } from '../../../hooks';
import './PipelinesPage.scss';

/** Builds a space-joined search string for a pipeline (id and function names). */
const plSearchText = (pl: Pipeline): string => [pl.id, ...pl.functions.map(f => f.name)].join(' ');

/**
 * Pipelines page: track editor with function references, drag-and-drop, and live counters.
 */
const PipelinesPage = (): React.JSX.Element => {
    const { pipelines, loading, isDirty, getServerPipeline, dispatch, savePipeline, discardPipeline, createPipeline, deletePipeline, loadFunctionList } = usePipelinesData();
    const [createDialogOpen, setCreateDialogOpen] = useState(false);
    const [diffOpenId, setDiffOpenId] = useState<string | null>(null);
    const { dragState, startDrag, endDrag } = useDragState();

    const anyDirty = useMemo(
        () => pipelines.some(pl => isDirty(pl.id)),
        [pipelines, isDirty],
    );

    useUnsavedChangesBlocker(anyDirty);

    const handleSave = useCallback((pipelineId: string) => (): Promise<void> => savePipeline(pipelineId), [savePipeline]);
    const handleDiscard = useCallback((pipelineId: string) => (): void => discardPipeline(pipelineId), [discardPipeline]);
    const handleDelete = useCallback((pipelineId: string) => (): Promise<boolean> => deletePipeline(pipelineId), [deletePipeline]);

    const { activeId, flashId, jumpTo: jumpToPipeline } = useLaneCardNavigation<Pipeline>({
        rows: pipelines,
        cardIdPrefix: 'pl',
        onActivate: (pl) => setDiffOpenId(pl.id),
    });

    const commands = useMemo((): Command[] => {
        const list: Command[] = [];
        if (!loading) {
            list.push({
                id: '__create_pipeline',
                icon: '+',
                label: 'Create pipeline',
                sub: 'Open the create pipeline dialog',
                keywords: 'create new pipeline add',
                onSelect: () => setCreateDialogOpen(true),
            });
        }
        for (const pl of pipelines) {
            if (isDirty(pl.id)) {
                list.push({
                    id: `__save_${pl.id}`,
                    icon: '✓',
                    label: `Save ${pl.id}`,
                    sub: 'Preview YAML diff before saving',
                    keywords: 'save commit apply',
                    onSelect: () => setDiffOpenId(pl.id),
                });
            }
        }
        return list;
    }, [loading, pipelines, isDirty]);

    const rowAdapter = useMemo((): RowAdapter<Pipeline> => ({
        rows: pipelines,
        getId: (pl) => pl.id,
        getLabel: (pl) => pl.id,
        getSub: (pl) => `${pl.functions.length} functions`,
        searchText: plSearchText,
        onSelect: (id) => jumpToPipeline(id),
        icon: '→',
    }), [pipelines, jumpToPipeline]);

    const shortcuts = useMemo((): ShortcutSection[] => [{
        title: 'Pipelines',
        items: [
            { keys: '↑ ↓', desc: 'Highlight a pipeline' },
            { keys: 'Enter', desc: 'Open the YAML diff' },
            { keys: 'Esc', desc: 'Clear selection' },
        ],
    }], []);

    const contribution = useMemo<PagePaletteContribution>(() => ({
        commands,
        rowAdapter: rowAdapter as RowAdapter<unknown>,
        placeholder: 'Search pipelines or run an action…',
        shortcuts,
    }), [commands, rowAdapter, shortcuts]);
    usePageContribution(contribution);

    const headerContent = (
        <CommandPaletteHeader
            title="Pipelines"
            placeholder="Search pipelines or run an action…"
            actions={<Button view="action" onClick={() => setCreateDialogOpen(true)} disabled={loading}>
                <Icon data={Plus} size={16} />
                Create pipeline
            </Button>}
        />
    );

    return (
        <PageLayout header={headerContent}>
            {loading ? (
                <PageLoader loading size="l" />
            ) : (
                <div className="pl-page">
                    {pipelines.length === 0 ? (
                        <EmptyState message='No pipelines found. Click "Create pipeline" to add one.' />
                    ) : (
                        pipelines.map(pl => (
                            <PipelineCard
                                key={pl.id}
                                pipeline={pl}
                                serverPipeline={getServerPipeline(pl.id)}
                                isDirty={isDirty(pl.id)}
                                dispatch={dispatch}
                                dragState={dragState}
                                onDragStart={startDrag}
                                onDragEnd={endDrag}
                                onSave={handleSave(pl.id)}
                                onDiscard={handleDiscard(pl.id)}
                                onDelete={handleDelete(pl.id)}
                                loadFunctionList={loadFunctionList}
                                diffOpen={diffOpenId === pl.id}
                                onOpenDiff={() => setDiffOpenId(pl.id)}
                                onCloseDiff={() => setDiffOpenId(null)}
                                flash={flashId === pl.id}
                                active={activeId === pl.id}
                            />
                        ))
                    )}
                </div>
            )}

            <CreateEntityDialog
                entityType="Pipeline"
                open={createDialogOpen}
                onClose={() => setCreateDialogOpen(false)}
                onConfirm={async (name) => {
                    const ok = await createPipeline(name);
                    if (ok) {
                        setCreateDialogOpen(false);
                    }
                }}
            />
        </PageLayout>
    );
};

export default PipelinesPage;
