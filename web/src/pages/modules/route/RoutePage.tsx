import React, { useCallback, useMemo, useRef } from 'react';
import { Button, Icon } from '@gravity-ui/uikit';
import { useConfigListCache, useListNavigation, usePageContribution, useTabCycle } from '../../../hooks';
import { Funnel, Plus } from '@gravity-ui/icons';
import { PageLayout, PageLoader, ConfigTabStrip, BulkBar, SearchInput, EmptyPagePlaceholder, RowCountDisplay } from '../../../components';
import { useFIBDraft } from './useFIBDraft';
import type { FIBRowItem } from './types';
import { FIBTable } from './FIBTable';
import FIBDrawer from './FIBDrawer';
import type { FIBDrawerHandle } from './FIBDrawer';
import FIBYamlIO from './FIBYamlIO';
import { FIBSaveDiffModal } from './FIBSaveDiffModal';
import { AddConfigModal, DeleteConfigModal, BulkDeleteModal, CommandPaletteHeader } from '../../../components';
import { useDraftPageHandlers, useDraftPageState, useDraftPageDerived } from '../../../components/draft';
import { isValidCidr as isValidCIDR } from '../../../utils';
import type { Command, RowAdapter, PagePaletteContribution } from '../../../components/command-palette';
import { buildConfigCommands } from '../../../components/command-palette';
import '../../../styles/chrome.scss';
import './route.scss';

const QP_CONFIG = 'config';
const QP_SEARCH = 'search';

let idCounter = 0;
const makeRowId = (): string => `new-${++idCounter}-${Date.now()}`;

const matchesFIBSearch = (r: FIBRowItem, q: string): boolean =>
    r.prefix.toLowerCase().includes(q) ||
    r.dst_mac.toLowerCase().includes(q) ||
    r.src_mac.toLowerCase().includes(q) ||
    r.device.toLowerCase().includes(q);

const fibRowsEqual = (s: FIBRowItem, r: FIBRowItem): boolean =>
    s.prefix === r.prefix &&
    s.dst_mac === r.dst_mac &&
    s.src_mac === r.src_mac &&
    s.device === r.device;

const RoutePage: React.FC = () => {
    const {
        draftConfigs, loading, loadFailed, draftRows, serverRows, isDirty, anyDirty,
        dispatchDraft, commitConfig, discardConfig,
    } = useFIBDraft();

    const { configs: cachedConfigs, counts: cachedCounts } = useConfigListCache('route');

    const pageState = useDraftPageState({
        loading,
        draftConfigs,
        anyDirty,
        configParamKey: QP_CONFIG,
        searchParamKey: QP_SEARCH,
    });
    const {
        search, activeRowId, setActiveRowId, editingRowId, setEditingRowId,
        selectedIds, setSelectedIds, deleteConfirmOpen, setDeleteConfirmOpen,
        diffModalOpen, setDiffModalOpen, addConfigOpen, setAddConfigOpen,
        deleteConfigOpen, setDeleteConfigOpen, dragDrop, updateParams,
        setActiveConfig, currentConfig,
    } = pageState;

    const drawerRef = useRef<FIBDrawerHandle>(null);

    const derived = useDraftPageDerived<FIBRowItem>({
        pageState,
        draftRows,
        serverRows,
        isDirty,
        draftConfigs,
        loading,
        configParamKey: QP_CONFIG,
        matchesSearch: matchesFIBSearch,
        rowsEqual: fibRowsEqual,
    });
    const {
        rawRows, rawServerRows, currentIsDirty, rowCounts: routeCounts,
        dirtySet, visibleRows, statusById, removedRows,
    } = derived;

    const editingIndex = editingRowId ? rawRows.findIndex((r) => r.id === editingRowId) : -1;
    const editingRow = editingIndex >= 0 ? rawRows[editingIndex] : null;

    const handlers = useDraftPageHandlers<FIBRowItem>({
        currentConfig, rawRows, editingIndex, activeRowId, editingRowId, selectedIds,
        dispatchDraft, commitConfig, discardConfig,
        drawerFlush: () => drawerRef.current?.flushAndApply(),
        setActiveConfig, setActiveRowId, setEditingRowId, setSelectedIds,
        setDiffModalOpen, setDeleteConfirmOpen, setDeleteConfigOpen,
        dragDrop,
    });

    const openAdd = useCallback((prefix = ''): void => {
        const newRow: FIBRowItem = { id: makeRowId(), prefix, dst_mac: '', src_mac: '', device: '' };
        dispatchDraft({ type: 'ADD_ROW', configName: currentConfig, row: newRow });
        setActiveRowId(newRow.id);
        setEditingRowId(newRow.id);
    }, [currentConfig, dispatchDraft]);

    const handleSearchChange = useCallback((value: string): void => {
        updateParams({ [QP_SEARCH]: value || null });
    }, [updateParams]);

    const handleConfigSelect = useCallback((cfg: string): void => {
        setActiveConfig(cfg);
    }, [setActiveConfig]);

    useTabCycle({
        tabs: draftConfigs,
        activeTab: currentConfig,
        onSelect: handleConfigSelect,
        enabled: !loading,
    });

    const navRows = useMemo(() => rawRows.map((r) => ({ id: r.id })), [rawRows]);
    useListNavigation({
        rows: navRows,
        activeId: activeRowId,
        setActiveId: setActiveRowId,
        onActivate: (row) => { setActiveRowId(row.id); setEditingRowId(row.id); },
        onDelete: (row) => {
            const r = rawRows.find((x) => x.id === row.id);
            if (r) handlers.handleDeleteRow(r);
        },
        enabled: !editingRowId,
    });

    const canCreate = !loading && !loadFailed;

    const commands = useMemo((): Command[] => {
        const list: Command[] = [
            {
                id: '__add',
                icon: '+',
                label: 'Add route',
                keywords: 'add route insert new',
                onSelect: () => openAdd(),
            },
        ];
        if (currentIsDirty) {
            list.push({
                id: '__save',
                icon: '✓',
                label: 'Save changes',
                onSelect: () => handlers.handleCommitPress(),
            });
            list.push({
                id: '__discard',
                icon: '⟲',
                label: 'Discard changes',
                onSelect: () => handlers.handleDiscard(),
            });
        }
        list.push(...buildConfigCommands({
            currentConfig,
            draftConfigs,
            dirtySet,
            onAddConfig: () => setAddConfigOpen(true),
            addConfigDisabled: !canCreate,
            onDeleteConfig: () => setDeleteConfigOpen(true),
            onSwitchConfig: (name) => handleConfigSelect(name),
        }));
        if (search) {
            list.push({
                id: '__clear',
                icon: '✕',
                label: 'Clear search',
                keywords: 'clear reset search',
                onSelect: () => handleSearchChange(''),
            });
        }
        return list;
    }, [canCreate, currentIsDirty, currentConfig, draftConfigs, dirtySet, search, handlers, handleConfigSelect, handleSearchChange, openAdd]);

    const dynamicCommands = useCallback((q: string): Command[] => {
        if (isValidCIDR(q.trim())) {
            return [
                {
                    id: '__add_cidr',
                    icon: '⌖',
                    label: `Add route for ${q.trim()}`,
                    sub: 'Pre-fill a new route with this prefix',
                    onSelect: () => openAdd(q.trim()),
                },
            ];
        }
        return [];
    }, [openAdd]);

    const rowAdapter = useMemo((): RowAdapter<FIBRowItem> => ({
        rows: rawRows,
        getId: (r) => r.id,
        getLabel: (r) => r.prefix || '(no prefix)',
        getSub: (r) => `${r.dst_mac || '—'} · ${r.device || '—'}`,
        searchText: (r) => [r.prefix, r.dst_mac, r.src_mac, r.device].join(' '),
        onSelect: (id) => { setActiveRowId(id); setEditingRowId(id); },
        icon: '→',
        max: 7,
    }), [rawRows]);

    const contribution = useMemo<PagePaletteContribution>(() => ({
        commands,
        dynamicCommands,
        rowAdapter: rowAdapter as RowAdapter<unknown>,
        placeholder: 'Search routes or run an action…',
    }), [commands, dynamicCommands, rowAdapter]);
    usePageContribution(contribution);

    const pageHeader = (
        <CommandPaletteHeader
            title="Route FIB"
            placeholder="Search routes or run an action…"
            actions={<>
                <FIBYamlIO
                    key={currentConfig || '__none'}
                    configName={currentConfig}
                    rows={rawRows}
                    onImport={handlers.handleImportYaml}
                    disabled={!currentConfig}
                />
                <Button view="action" onClick={() => openAdd()}>
                    <Icon data={Plus} size={16} />
                    Add Route
                </Button>
            </>}
        />
    );

    // While a warm cache exists, keep the tab strip mounted from cached names
    // and counts so it does not blink on remount; only the rows below reload.
    const tabConfigs = loading ? cachedConfigs : draftConfigs;
    const tabCounts = loading ? cachedCounts : routeCounts;

    if (loading && cachedConfigs.length === 0) {
        return (
            <PageLayout header={pageHeader} className="yn-flat-layout">
                <PageLoader loading size="l" />
            </PageLayout>
        );
    }

    return (
        <PageLayout header={pageHeader} className="yn-flat-layout">
            <div className="yn-page yn-flat-page">
                {tabConfigs.length === 0 ? (
                    <EmptyPagePlaceholder
                        message="No FIB configurations found."
                        actionLabel="Add Config"
                        onAction={() => setAddConfigOpen(true)}
                        actionDisabled={!canCreate}
                    />
                ) : (
                    <>
                        <ConfigTabStrip
                            configs={tabConfigs}
                            activeConfig={currentConfig}
                            counts={tabCounts}
                            dirtyConfigs={dirtySet}
                            onSelect={handleConfigSelect}
                            onAddConfig={() => setAddConfigOpen(true)}
                            addConfigDisabled={!canCreate}
                        />
                        {loading ? (
                            <PageLoader loading size="l" />
                        ) : (
                            <>
                                <div className="yn-toolbar-bordered">
                                    <div style={{ flex: 1 }} />
                                    <div style={{ flexBasis: 230, flexShrink: 1 }}>
                                        <SearchInput
                                            value={search}
                                            onUpdate={handleSearchChange}
                                            placeholder="Filter routes…"
                                            enableFocusShortcut={false}
                                            showShortcutHint={false}
                                            icon={Funnel}
                                        />
                                    </div>
                                    <RowCountDisplay filtered={visibleRows.length} total={rawRows.length} />
                                </div>
                                <div className="yn-content">
                                    <FIBTable
                                        allRows={rawRows}
                                        visibleRows={visibleRows}
                                        statusById={statusById}
                                        removedRows={search ? [] : removedRows}
                                        activeRowId={activeRowId}
                                        editingRowId={editingRowId}
                                        selectedIds={selectedIds}
                                        dragOverState={dragDrop.dragOverState}
                                        onRowClick={setActiveRowId}
                                        onEditRow={(id) => { setActiveRowId(id); setEditingRowId(id); }}
                                        onRestoreRow={handlers.handleRestoreRow}
                                        onSelectionChange={setSelectedIds}
                                        onDragStart={dragDrop.handleDragStart}
                                        onDragOver={dragDrop.handleDragOver}
                                        onDragLeave={dragDrop.handleDragLeave}
                                        onDrop={handlers.handleDrop}
                                        currentIsDirty={currentIsDirty}
                                        onSave={handlers.handleCommitPress}
                                        onDiscard={handlers.handleDiscard}
                                        onDeleteConfig={() => setDeleteConfigOpen(true)}
                                    />
                                </div>
                            </>
                        )}
                    </>
                )}

                {selectedIds.size > 0 && (
                    <BulkBar count={selectedIds.size} itemNoun="route" onDelete={() => setDeleteConfirmOpen(true)} onClear={() => setSelectedIds(new Set())} />
                )}

                <BulkDeleteModal open={deleteConfirmOpen} count={selectedIds.size} itemNoun="route" configName={currentConfig} onClose={() => setDeleteConfirmOpen(false)} onConfirm={handlers.handleBulkDelete} />

                <FIBDrawer ref={drawerRef} open={!!editingRow} row={editingRow} index={editingIndex} total={rawRows.length} onClose={handlers.closeDrawer} onChange={handlers.handleRowChange} onDelete={handlers.handleDeleteRow} onJump={handlers.handleJumpEdit} />

                {diffModalOpen && (
                    <FIBSaveDiffModal configName={currentConfig} draftRows={rawRows} serverRows={rawServerRows} onClose={() => setDiffModalOpen(false)} onApply={handlers.handleCommit} />
                )}

                <AddConfigModal
                    open={addConfigOpen}
                    onClose={() => setAddConfigOpen(false)}
                    onCreate={(name) => { dispatchDraft({ type: 'ADD_CONFIG', configName: name }); setActiveConfig(name); setAddConfigOpen(false); }}
                    title="Add FIB config"
                    placeholder="e.g. route0"
                    existingNames={draftConfigs}
                />

                <DeleteConfigModal open={deleteConfigOpen} configName={currentConfig} onClose={() => setDeleteConfigOpen(false)} onConfirm={handlers.handleDeleteConfig} />

            </div>
        </PageLayout>
    );
};

export default RoutePage;
