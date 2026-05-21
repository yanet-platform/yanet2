import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Button, Flex, Icon, Text, TextInput } from '@gravity-ui/uikit';
import { Magnifier, Plus } from '@gravity-ui/icons';
import { PageLayout, PageLoader, ConfigTabStrip, BulkBar } from '../../../components';
import { useFIBDraft } from './useFIBDraft';
import { useUnsavedChangesBlocker } from '../../builtin/_shared/lane-editor';
import type { FIBRowItem } from './types';
import { FIBTable } from './FIBTable';
import FIBDrawer from './FIBDrawer';
import type { FIBDrawerHandle } from './FIBDrawer';
import FIBYamlIO from './FIBYamlIO';
import { FIBSaveDiffModal } from './FIBSaveDiffModal';
import '../../../styles/draft-page.scss';

let idCounter = 0;
const makeRowId = (): string => `new-${++idCounter}-${Date.now()}`;


const RoutePage: React.FC = () => {
    const {
        draftConfigs,
        loading,
        draftRows,
        serverRows,
        isDirty,
        anyDirty,
        dispatchDraft,
        commitConfig,
        discardConfig,
    } = useFIBDraft();

    const [activeConfig, setActiveConfig] = useState<string>('');
    const [search, setSearch] = useState('');
    const [activeRowId, setActiveRowId] = useState<string | null>(null);
    const [editingRowId, setEditingRowId] = useState<string | null>(null);
    const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
    const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
    const [diffModalOpen, setDiffModalOpen] = useState(false);
    const [addConfigOpen, setAddConfigOpen] = useState(false);
    const [newConfigName, setNewConfigName] = useState('');
    const [deleteConfigOpen, setDeleteConfigOpen] = useState(false);
    const [dragRowId, setDragRowId] = useState<string | null>(null);
    const [dragOverState, setDragOverState] = useState<{ id: string | null; where: 'top' | 'bottom' | null }>({ id: null, where: null });

    const searchRef = useRef<HTMLInputElement>(null);
    const drawerRef = useRef<FIBDrawerHandle>(null);

    useUnsavedChangesBlocker(anyDirty);

    const currentConfig = activeConfig || draftConfigs[0] || '';
    const rawRows: FIBRowItem[] = draftRows(currentConfig);
    const rawServerRows: FIBRowItem[] = serverRows(currentConfig);
    const currentIsDirty = isDirty(currentConfig);

    const routeCounts = useMemo((): Map<string, number> => {
        const m = new Map<string, number>();
        draftConfigs.forEach((c) => m.set(c, draftRows(c).length));
        return m;
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [draftConfigs, draftRows]);

    const dirtySet = useMemo((): Set<string> => {
        const s = new Set<string>();
        draftConfigs.forEach((c) => { if (isDirty(c)) s.add(c); });
        return s;
    }, [draftConfigs, isDirty]);

    const visibleRows = useMemo((): FIBRowItem[] => {
        const q = search.trim().toLowerCase();
        if (!q) return rawRows;
        return rawRows.filter((r) =>
            r.prefix.toLowerCase().includes(q) ||
            r.dst_mac.toLowerCase().includes(q) ||
            r.src_mac.toLowerCase().includes(q) ||
            r.device.toLowerCase().includes(q),
        );
    }, [rawRows, search]);

    const statusById = useMemo((): Map<string, import('./types').FIBRowStatus> => {
        const m = new Map<string, import('./types').FIBRowStatus>();
        const serverById = new Map(rawServerRows.map((r) => [r.id, r]));
        for (const r of rawRows) {
            const s = serverById.get(r.id);
            if (!s) {
                m.set(r.id, 'added');
            } else {
                const same = s.prefix === r.prefix && s.dst_mac === r.dst_mac
                    && s.src_mac === r.src_mac && s.device === r.device;
                m.set(r.id, same ? 'same' : 'changed');
            }
        }
        return m;
    }, [rawRows, rawServerRows]);

    const removedRows = useMemo((): FIBRowItem[] => {
        const localIds = new Set(rawRows.map((r) => r.id));
        return rawServerRows.filter((r) => !localIds.has(r.id));
    }, [rawRows, rawServerRows]);

    const editingIndex = editingRowId ? rawRows.findIndex((r) => r.id === editingRowId) : -1;
    const editingRow = editingIndex >= 0 ? rawRows[editingIndex] : null;

    const openAdd = useCallback((): void => {
        const newRow: FIBRowItem = { id: makeRowId(), prefix: '', dst_mac: '', src_mac: '', device: '' };
        dispatchDraft({ type: 'ADD_ROW', configName: currentConfig, row: newRow });
        setActiveRowId(newRow.id);
        setEditingRowId(newRow.id);
    }, [currentConfig, dispatchDraft]);

    const closeDrawer = useCallback((): void => {
        setEditingRowId(null);
    }, []);

    const handleRowChange = useCallback((updated: FIBRowItem): void => {
        dispatchDraft({ type: 'UPDATE_ROW', configName: currentConfig, id: updated.id, patch: updated });
    }, [currentConfig, dispatchDraft]);

    const handleDeleteRow = useCallback((row: FIBRowItem): void => {
        dispatchDraft({ type: 'REMOVE_ROW', configName: currentConfig, id: row.id });
        if (editingRowId === row.id) setEditingRowId(null);
        if (activeRowId === row.id) setActiveRowId(null);
    }, [currentConfig, dispatchDraft, editingRowId, activeRowId]);

    const handleBulkDelete = useCallback((): void => {
        dispatchDraft({ type: 'REMOVE_ROWS', configName: currentConfig, ids: Array.from(selectedIds) });
        setSelectedIds(new Set());
        setDeleteConfirmOpen(false);
    }, [selectedIds, currentConfig, dispatchDraft]);

    const handleRestoreRow = useCallback((row: FIBRowItem): void => {
        dispatchDraft({ type: 'ADD_ROW', configName: currentConfig, row: { ...row } });
    }, [currentConfig, dispatchDraft]);

    const handleJumpEdit = useCallback((delta: number): void => {
        const next = editingIndex + delta;
        if (next < 0 || next >= rawRows.length) return;
        setEditingRowId(rawRows[next].id);
        setActiveRowId(rawRows[next].id);
    }, [editingIndex, rawRows]);

    const handleImportYaml = useCallback((rows: FIBRowItem[], mode: 'replace' | 'append'): void => {
        if (mode === 'replace') {
            dispatchDraft({ type: 'REPLACE_ALL_ROWS', configName: currentConfig, rows });
        } else {
            for (const r of rows) {
                dispatchDraft({ type: 'ADD_ROW', configName: currentConfig, row: r });
            }
        }
    }, [currentConfig, dispatchDraft]);

    const handleCommitPress = useCallback((): void => {
        if (drawerRef.current) {
            drawerRef.current.flushAndApply();
        }
        setDiffModalOpen(true);
    }, []);

    const handleCommit = useCallback(async (): Promise<void> => {
        await commitConfig(currentConfig);
        setDiffModalOpen(false);
    }, [currentConfig, commitConfig]);

    const handleDiscard = useCallback((): void => {
        discardConfig(currentConfig);
    }, [currentConfig, discardConfig]);

    const handleAddConfig = useCallback((): void => {
        const name = newConfigName.trim();
        if (!name || draftConfigs.includes(name)) return;
        dispatchDraft({ type: 'ADD_CONFIG', configName: name });
        setActiveConfig(name);
        setNewConfigName('');
        setAddConfigOpen(false);
    }, [newConfigName, draftConfigs, dispatchDraft]);

    const handleDeleteConfig = useCallback((): void => {
        dispatchDraft({ type: 'DELETE_CONFIG', configName: currentConfig });
        setActiveConfig('');
        setDeleteConfigOpen(false);
    }, [currentConfig, dispatchDraft]);

    const handleDragStart = useCallback((id: string, e: React.DragEvent): void => {
        setDragRowId(id);
        e.dataTransfer.effectAllowed = 'move';
        try { e.dataTransfer.setData('text/plain', id); } catch (_) {}
    }, []);

    const handleDragOver = useCallback((id: string, e: React.DragEvent): void => {
        e.preventDefault();
        if (!dragRowId || dragRowId === id) return;
        const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
        const where: 'top' | 'bottom' = (e.clientY - rect.top) < rect.height / 2 ? 'top' : 'bottom';
        setDragOverState({ id, where });
    }, [dragRowId]);

    const handleDragLeave = useCallback((): void => {
        setDragOverState({ id: null, where: null });
    }, []);

    const handleDrop = useCallback((id: string, e: React.DragEvent): void => {
        e.preventDefault();
        if (!dragRowId || dragRowId === id) {
            setDragRowId(null);
            setDragOverState({ id: null, where: null });
            return;
        }
        const where = dragOverState.where ?? 'bottom';
        const rows = [...rawRows];
        const fromIdx = rows.findIndex((r) => r.id === dragRowId);
        if (fromIdx < 0) { setDragRowId(null); setDragOverState({ id: null, where: null }); return; }
        const [moved] = rows.splice(fromIdx, 1);
        let toIdx = rows.findIndex((r) => r.id === id);
        if (toIdx < 0) { setDragRowId(null); setDragOverState({ id: null, where: null }); return; }
        if (where === 'bottom') toIdx += 1;
        rows.splice(toIdx, 0, moved);
        dispatchDraft({ type: 'REORDER_ROWS', configName: currentConfig, rows });
        setDragRowId(null);
        setDragOverState({ id: null, where: null });
    }, [dragRowId, dragOverState.where, rawRows, currentConfig, dispatchDraft]);

    // Keyboard shortcuts
    useEffect(() => {
        const onKey = (e: KeyboardEvent): void => {
            if (e.key === 'Escape') {
                if (editingRowId) { setEditingRowId(null); return; }
            }
            const tag = (e.target as HTMLElement).tagName;
            if (tag === 'INPUT' || tag === 'TEXTAREA') return;
            if (e.key === '/' && !e.metaKey && !e.ctrlKey) {
                e.preventDefault();
                searchRef.current?.focus();
                return;
            }
            if (!rawRows.length) return;
            if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
                e.preventDefault();
                const idx = rawRows.findIndex((r) => r.id === activeRowId);
                const next = e.key === 'ArrowDown'
                    ? Math.min(rawRows.length - 1, idx + 1)
                    : Math.max(0, Math.max(0, idx - 1));
                if (rawRows[next]) setActiveRowId(rawRows[next].id);
                return;
            }
            if (e.key === 'Enter' && activeRowId) {
                setEditingRowId(activeRowId);
                return;
            }
            if ((e.key === 'd' || e.key === 'Backspace') && activeRowId) {
                e.preventDefault();
                const row = rawRows.find((r) => r.id === activeRowId);
                if (row) handleDeleteRow(row);
            }
        };
        window.addEventListener('keydown', onKey);
        return () => window.removeEventListener('keydown', onKey);
    }, [editingRowId, activeRowId, rawRows, handleDeleteRow]);

    const pageHeader = (
        <Flex alignItems="center" gap={4} style={{ width: '100%' }}>
            <Text variant="header-1">Route FIB</Text>
            <Flex grow />
            <div style={{ flexBasis: 380, flexShrink: 1 }}>
                <TextInput
                    controlRef={searchRef}
                    value={search}
                    onUpdate={setSearch}
                    placeholder="Search prefix, MAC or device… (/)"
                    startContent={
                        <Flex alignItems="center" justifyContent="center" style={{ paddingInline: 8, color: 'var(--g-color-text-hint)' }}>
                            <Icon data={Magnifier} size={16} />
                        </Flex>
                    }
                    hasClear
                    type="search"
                />
            </div>
            {currentConfig && (
                <FIBYamlIO
                    configName={currentConfig}
                    rows={rawRows}
                    onImport={handleImportYaml}
                />
            )}
            <Button view="action" onClick={openAdd}>
                <Icon data={Plus} size={16} />
                Add Route
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
            <div className="fw-page">
                {draftConfigs.length === 0 ? (
                    <div className="fw-empty-page">
                        <div className="fw-empty-page__message">No FIB configurations found.</div>
                        <Button view="action" onClick={() => { setNewConfigName(''); setAddConfigOpen(true); }}>
                            Add Config
                        </Button>
                    </div>
                ) : (
                    <>
                        <ConfigTabStrip
                            configs={draftConfigs}
                            activeConfig={currentConfig}
                            counts={routeCounts}
                            dirtyConfigs={dirtySet}
                            onSelect={(c) => {
                                setActiveConfig(c);
                                setActiveRowId(null);
                                setEditingRowId(null);
                                setSelectedIds(new Set());
                            }}
                            onAddConfig={() => { setNewConfigName(''); setAddConfigOpen(true); }}
                        />

                        <div className="fw-content">
                            <FIBTable
                                allRows={rawRows}
                                visibleRows={visibleRows}
                                statusById={statusById}
                                removedRows={search ? [] : removedRows}
                                activeRowId={activeRowId}
                                editingRowId={editingRowId}
                                selectedIds={selectedIds}
                                dragOverState={dragOverState}
                                onRowClick={setActiveRowId}
                                onEditRow={(id) => { setActiveRowId(id); setEditingRowId(id); }}
                                onRestoreRow={handleRestoreRow}
                                onSelectionChange={setSelectedIds}
                                onDragStart={handleDragStart}
                                onDragOver={handleDragOver}
                                onDragLeave={handleDragLeave}
                                onDrop={handleDrop}
                                currentIsDirty={currentIsDirty}
                                onSave={handleCommitPress}
                                onDiscard={handleDiscard}
                                onDeleteConfig={() => setDeleteConfigOpen(true)}
                            />
                        </div>
                    </>
                )}

                {selectedIds.size > 0 && (
                    <BulkBar
                        count={selectedIds.size}
                        itemNoun="route"
                        onDelete={() => setDeleteConfirmOpen(true)}
                        onClear={() => setSelectedIds(new Set())}
                    />
                )}

                {deleteConfirmOpen && (
                    <div className="fw-modal-backdrop" onClick={() => setDeleteConfirmOpen(false)}>
                        <div className="fw-modal fw-modal--sm" onClick={(e) => e.stopPropagation()}>
                            <header className="fw-modal__head">
                                <span className="fw-modal__title">Delete routes</span>
                                <button type="button" className="fw-icon-btn" onClick={() => setDeleteConfirmOpen(false)} aria-label="Close">✕</button>
                            </header>
                            <div className="fw-modal__body fw-modal__body--confirm">
                                <p>Delete <strong>{selectedIds.size}</strong> selected route(s) from <code>{currentConfig}</code>? This cannot be undone.</p>
                            </div>
                            <footer className="fw-modal__foot">
                                <span />
                                <div className="fw-modal__foot-actions">
                                    <button type="button" className="fw-btn fw-btn--ghost" onClick={() => setDeleteConfirmOpen(false)}>Cancel</button>
                                    <button type="button" className="fw-btn fw-btn--danger" onClick={handleBulkDelete}>
                                        Delete {selectedIds.size} route(s)
                                    </button>
                                </div>
                            </footer>
                        </div>
                    </div>
                )}

                <FIBDrawer
                    ref={drawerRef}
                    open={!!editingRow}
                    row={editingRow}
                    index={editingIndex}
                    total={rawRows.length}
                    onClose={closeDrawer}
                    onChange={handleRowChange}
                    onDelete={handleDeleteRow}
                    onJump={handleJumpEdit}
                />

                {diffModalOpen && (
                    <FIBSaveDiffModal
                        configName={currentConfig}
                        draftRows={rawRows}
                        serverRows={rawServerRows}
                        onClose={() => setDiffModalOpen(false)}
                        onApply={handleCommit}
                    />
                )}

                {addConfigOpen && (
                    <div className="fw-modal-backdrop" onClick={() => setAddConfigOpen(false)}>
                        <div className="fw-modal fw-modal--sm" onClick={(e) => e.stopPropagation()}>
                            <header className="fw-modal__head">
                                <span className="fw-modal__title">Add FIB config</span>
                                <button type="button" className="fw-icon-btn" onClick={() => setAddConfigOpen(false)} aria-label="Close">✕</button>
                            </header>
                            <div className="fw-modal__body fw-modal__body--confirm">
                                <div className="fw-field">
                                    <label className="fw-field__label" htmlFor="fib-new-config-name">
                                        Config name <span className="fw-field__req">*</span>
                                    </label>
                                    <input
                                        id="fib-new-config-name"
                                        className="fw-input"
                                        type="text"
                                        value={newConfigName}
                                        onChange={(e) => setNewConfigName(e.target.value)}
                                        onKeyDown={(e) => {
                                            if (e.key === 'Enter') handleAddConfig();
                                            if (e.key === 'Escape') setAddConfigOpen(false);
                                        }}
                                        placeholder="e.g. route0"
                                        autoFocus
                                    />
                                </div>
                            </div>
                            <footer className="fw-modal__foot">
                                <span />
                                <div className="fw-modal__foot-actions">
                                    <button type="button" className="fw-btn fw-btn--ghost" onClick={() => setAddConfigOpen(false)}>Cancel</button>
                                    <button
                                        type="button"
                                        className="fw-btn fw-btn--primary"
                                        onClick={handleAddConfig}
                                        disabled={!newConfigName.trim() || draftConfigs.includes(newConfigName.trim())}
                                    >
                                        Create
                                    </button>
                                </div>
                            </footer>
                        </div>
                    </div>
                )}

                {deleteConfigOpen && (
                    <div className="fw-modal-backdrop" onClick={() => setDeleteConfigOpen(false)}>
                        <div className="fw-modal fw-modal--sm" onClick={(e) => e.stopPropagation()}>
                            <header className="fw-modal__head">
                                <span className="fw-modal__title">Delete config</span>
                                <button type="button" className="fw-icon-btn" onClick={() => setDeleteConfigOpen(false)} aria-label="Close">✕</button>
                            </header>
                            <div className="fw-modal__body fw-modal__body--confirm">
                                <p>Delete config <code>{currentConfig}</code>? This cannot be undone.</p>
                            </div>
                            <footer className="fw-modal__foot">
                                <span />
                                <div className="fw-modal__foot-actions">
                                    <button type="button" className="fw-btn fw-btn--ghost" onClick={() => setDeleteConfigOpen(false)}>Cancel</button>
                                    <button type="button" className="fw-btn fw-btn--danger" onClick={handleDeleteConfig}>
                                        Delete
                                    </button>
                                </div>
                            </footer>
                        </div>
                    </div>
                )}
            </div>
        </PageLayout>
    );
};

export default RoutePage;
