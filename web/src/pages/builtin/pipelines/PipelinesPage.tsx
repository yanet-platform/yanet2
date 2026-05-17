import React, { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import { Button, Flex, Icon, Text, TextInput } from '@gravity-ui/uikit';
import { Magnifier, Plus } from '@gravity-ui/icons';
import { PageLayout, PageLoader, EmptyState } from '../../../components';
import { usePipelinesData } from './hooks/usePipelinesData';
import { useUnsavedChangesBlocker } from '../_shared/lane-editor';
import { PipelineCard } from './components/PipelineCard';
import { CreatePipelineDialog } from './dialogs';
import type { Pipeline } from './types';
import './PipelinesPage.scss';

/** Returns true if the pipeline matches the query string (case-insensitive substring). */
const matchesPipeline = (pl: Pipeline, query: string): boolean => {
    const q = query.toLowerCase();
    if (pl.id.toLowerCase().includes(q)) {
        return true;
    }
    for (const ref of pl.functions) {
        if (ref.name.toLowerCase().includes(q)) {
            return true;
        }
    }
    return false;
};

/**
 * Pipelines page: track editor with function references, drag-and-drop, and live counters.
 */
const PipelinesPage = (): React.JSX.Element => {
    const { pipelines, loading, isDirty, getServerPipeline, dispatch, savePipeline, discardPipeline, createPipeline, deletePipeline, loadFunctionList } = usePipelinesData();
    const [createDialogOpen, setCreateDialogOpen] = useState(false);
    const [searchQuery, setSearchQuery] = useState('');
    const searchRef = useRef<HTMLInputElement>(null);

    useEffect(() => {
        const handleKeyDown = (e: KeyboardEvent): void => {
            if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
                e.preventDefault();
                searchRef.current?.focus();
            }
        };
        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, []);

    const filteredPipelines = useMemo(() => {
        if (!searchQuery.trim()) {
            return pipelines;
        }
        return pipelines.filter(pl => matchesPipeline(pl, searchQuery));
    }, [pipelines, searchQuery]);

    const anyDirty = useMemo(
        () => pipelines.some(pl => isDirty(pl.id)),
        [pipelines, isDirty],
    );

    useUnsavedChangesBlocker(anyDirty);

    const handleSave = useCallback((pipelineId: string) => (): Promise<void> => savePipeline(pipelineId), [savePipeline]);
    const handleDiscard = useCallback((pipelineId: string) => (): void => discardPipeline(pipelineId), [discardPipeline]);
    const handleDelete = useCallback((pipelineId: string) => (): Promise<boolean> => deletePipeline(pipelineId), [deletePipeline]);

    const pageHeader = (
        <Flex alignItems="center" gap={4} style={{ width: '100%' }}>
            <Text variant="header-1">Pipelines</Text>
            <Flex grow />
            <div style={{ flexBasis: 380, flexShrink: 1 }}>
                <TextInput
                    controlRef={searchRef}
                    value={searchQuery}
                    onUpdate={setSearchQuery}
                    placeholder="Search pipelines, functions… (⌘K)"
                    startContent={
                        <Flex alignItems="center" justifyContent="center" style={{ paddingInline: 8, color: 'var(--g-color-text-hint)' }}>
                            <Icon data={Magnifier} size={16} />
                        </Flex>
                    }
                    hasClear
                    type="search"
                />
            </div>
            <Button
                view="action"
                onClick={() => setCreateDialogOpen(true)}
            >
                <Icon data={Plus} size={16} />
                Create pipeline
            </Button>
        </Flex>
    );

    if (loading) {
        return (
            <PageLayout header={pageHeader}>
                <PageLoader loading size="l" />
            </PageLayout>
        );
    }

    return (
        <PageLayout header={pageHeader}>
            <div className="pl-page">
                {filteredPipelines.length === 0 ? (
                    <EmptyState message={
                        searchQuery.trim()
                            ? `No pipelines match "${searchQuery}".`
                            : 'No pipelines found. Click "Create pipeline" to add one.'
                    } />
                ) : (
                    filteredPipelines.map(pl => (
                        <PipelineCard
                            key={pl.id}
                            pipeline={pl}
                            serverPipeline={getServerPipeline(pl.id)}
                            isDirty={isDirty(pl.id)}
                            dispatch={dispatch}
                            onSave={handleSave(pl.id)}
                            onDiscard={handleDiscard(pl.id)}
                            onDelete={handleDelete(pl.id)}
                            loadFunctionList={loadFunctionList}
                        />
                    ))
                )}
            </div>

            <CreatePipelineDialog
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
