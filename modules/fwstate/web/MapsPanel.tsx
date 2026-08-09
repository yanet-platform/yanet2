import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Button, Icon, Table, Text, TextInput, Tooltip } from '@gravity-ui/uikit';
import { Plus, TrashBin, Layers } from '@gravity-ui/icons';
import { API } from '@yanet/core/api';
import type { MapStats } from '@yanet/core/api/fwstate';
import { ConfirmDialog, FormDialog, FormField, PageLoader } from '@yanet/core/components';
import { formatBytes, toaster } from '@yanet/core/utils';
import {
    EMPTY_SIZING,
    MAX_WORKER_COUNT,
    toUint,
    validateWorkerCount,
    type MapSizingFields,
} from './mapForm';
import './fwstate.scss';

type MapStatsPair = { v4?: MapStats; v6?: MapStats };

const fmtCompact = (n: number): string => {
    if (n >= 1e6) return (n / 1e6).toFixed(n >= 1e7 ? 1 : 2) + 'M';
    if (n >= 1e3) return (n / 1e3).toFixed(n >= 1e4 ? 1 : 2) + 'k';
    return String(n);
};

const toNumberOrZero = (value: number | string | null | undefined): number => {
    if (value === undefined || value === null) return 0;
    if (typeof value === 'number') return Number.isFinite(value) ? value : 0;
    const trimmed = value.trim();
    if (!trimmed || !/^\d+$/.test(trimmed)) return 0;
    const parsed = Number(trimmed);
    return Number.isFinite(parsed) ? parsed : 0;
};

const formatMemoryBytes = (value: number | string | null | undefined): string => {
    try {
        if (value === undefined || value === null) return '-';
        const numeric = typeof value === 'number'
            ? (Number.isSafeInteger(value) ? value : NaN)
            : Number(value.trim());
        if (!Number.isFinite(numeric) || numeric < 0) return '-';
        return formatBytes(BigInt(Math.trunc(numeric)));
    } catch {
        return '-';
    }
};

const formatNsUtc = (value: number | string | null | undefined): string => {
    const numeric = toNumberOrZero(value);
    if (!numeric) return '-';
    try {
        const millis = Number(BigInt(numeric) / 1_000_000n);
        const date = new Date(millis);
        if (!Number.isFinite(date.getTime())) return '-';
        return date.toISOString();
    } catch {
        return '-';
    }
};

/** Renders a "v4 · v6" pair cell with the shared monospace table styling. */
const PairCell: React.FC<{ v4: string; v6: string }> = ({ v4, v6 }) => (
    <span className="fwstate-mono">{v4} <span style={{ color: 'var(--yn-text-3)' }}>·</span> {v6}</span>
);

interface MapRow {
    name: string;
    stats: MapStatsPair;
}

interface CreateMapDialogProps {
    open: boolean;
    onClose: () => void;
    existingNames: string[];
    onCreate: (request: { name: string; indexSize: number; extraBucketCount: number; workerCount: number }) => Promise<void>;
}

const CreateMapDialog: React.FC<CreateMapDialogProps> = ({ open, onClose, existingNames, onCreate }) => {
    const [name, setName] = useState('');
    const [sizing, setSizing] = useState<MapSizingFields>(EMPTY_SIZING);
    const [saving, setSaving] = useState(false);

    useEffect(() => {
        if (open) {
            setName('');
            setSizing(EMPTY_SIZING);
            setSaving(false);
        }
    }, [open]);

    const trimmedName = name.trim();
    const workerError = validateWorkerCount(sizing.workerCount);
    const nameError = !trimmedName
        ? 'Map name is required'
        : existingNames.includes(trimmedName)
            ? `Map "${trimmedName}" already exists`
            : undefined;
    const canSubmit = !saving && !nameError && !workerError;

    const handleConfirm = useCallback(async () => {
        if (!canSubmit) return;
        setSaving(true);
        try {
            await onCreate({
                name: trimmedName,
                indexSize: toUint(sizing.indexSize),
                extraBucketCount: toUint(sizing.extraBucketCount),
                workerCount: toUint(sizing.workerCount),
            });
        } catch {
            setSaving(false);
        }
    }, [canSubmit, onCreate, trimmedName, sizing]);

    return (
        <FormDialog
            open={open}
            onClose={onClose}
            onConfirm={handleConfirm}
            title="Create fwstate-map"
            confirmText="Create"
            loading={saving}
            disabled={!canSubmit}
            width="440px"
        >
            <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
                <FormField label="Map name" required hint="Unique identifier for the standalone fwstate-map.">
                    <TextInput
                        value={name}
                        onUpdate={setName}
                        placeholder="e.g. fwstate0"
                        validationState={nameError ? 'invalid' : undefined}
                        errorMessage={nameError}
                        autoFocus
                    />
                </FormField>
                <MapSizingFieldsInput sizing={sizing} setSizing={setSizing} workerError={workerError} />
            </div>
        </FormDialog>
    );
};

interface InsertLayerDialogProps {
    open: boolean;
    mapName: string;
    onClose: () => void;
    onInsert: (request: { indexSize: number; extraBucketCount: number; workerCount: number }) => Promise<void>;
}

const InsertLayerDialog: React.FC<InsertLayerDialogProps> = ({ open, mapName, onClose, onInsert }) => {
    const [sizing, setSizing] = useState<MapSizingFields>(EMPTY_SIZING);
    const [saving, setSaving] = useState(false);

    useEffect(() => {
        if (open) {
            setSizing(EMPTY_SIZING);
            setSaving(false);
        }
    }, [open]);

    const workerError = validateWorkerCount(sizing.workerCount);
    const canSubmit = !saving && !workerError;

    const handleConfirm = useCallback(async () => {
        if (!canSubmit) return;
        setSaving(true);
        try {
            await onInsert({
                indexSize: toUint(sizing.indexSize),
                extraBucketCount: toUint(sizing.extraBucketCount),
                workerCount: toUint(sizing.workerCount),
            });
        } catch {
            setSaving(false);
        }
    }, [canSubmit, onInsert, sizing]);

    return (
        <FormDialog
            open={open}
            onClose={onClose}
            onConfirm={handleConfirm}
            title={`Insert layer into "${mapName}"`}
            confirmText="Insert layer"
            loading={saving}
            disabled={!canSubmit}
            width="440px"
        >
            <MapSizingFieldsInput sizing={sizing} setSizing={setSizing} workerError={workerError} />
        </FormDialog>
    );
};

interface MapSizingFieldsInputProps {
    sizing: MapSizingFields;
    setSizing: React.Dispatch<React.SetStateAction<MapSizingFields>>;
    workerError?: string;
}

const MapSizingFieldsInput: React.FC<MapSizingFieldsInputProps> = ({ sizing, setSizing, workerError }) => (
    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
        <FormField label="Hash index slots" hint="0 = server default.">
            <TextInput
                type="number"
                value={sizing.indexSize}
                onUpdate={(value) => setSizing((prev) => ({ ...prev, indexSize: value }))}
            />
        </FormField>
        <FormField label="Overflow buckets" hint="0 = server default.">
            <TextInput
                type="number"
                value={sizing.extraBucketCount}
                onUpdate={(value) => setSizing((prev) => ({ ...prev, extraBucketCount: value }))}
            />
        </FormField>
        <FormField label="Worker count" required hint={`1..${MAX_WORKER_COUNT} (uint16 range).`}>
            <TextInput
                type="number"
                value={sizing.workerCount}
                onUpdate={(value) => setSizing((prev) => ({ ...prev, workerCount: value }))}
                validationState={workerError ? 'invalid' : undefined}
                errorMessage={workerError}
            />
        </FormField>
    </div>
);

export interface MapsPanelProps {
    /** Current list of fwstate-map names, owned by the parent page. */
    maps: string[];
    /** Called after create/delete so the parent refreshes its list (and the config dropdown). */
    onChanged: () => void;
}

export const MapsPanel: React.FC<MapsPanelProps> = ({ maps, onChanged }) => {
    const [statsByMap, setStatsByMap] = useState<Record<string, MapStatsPair>>({});
    const [statsLoading, setStatsLoading] = useState(false);
    const [createOpen, setCreateOpen] = useState(false);
    const [insertTarget, setInsertTarget] = useState<string | null>(null);
    const [deleteTarget, setDeleteTarget] = useState<string | null>(null);
    const [deleting, setDeleting] = useState(false);

    const refreshStats = useCallback(async (names: string[]): Promise<void> => {
        if (names.length === 0) {
            setStatsByMap({});
            return;
        }
        setStatsLoading(true);
        try {
            const results = await Promise.all(
                names.map(async (name): Promise<[string, MapStatsPair]> => {
                    try {
                        const res = await API.fwstate.getMapStats({ name });
                        return [name, { v4: res.ipv4_stats, v6: res.ipv6_stats }];
                    } catch (err) {
                        toaster.error('fwstate-map-stats', `Failed to load stats for "${name}"`, err);
                        return [name, {}];
                    }
                }),
            );
            const next: Record<string, MapStatsPair> = {};
            for (const [name, pair] of results) {
                next[name] = pair;
            }
            setStatsByMap(next);
        } finally {
            setStatsLoading(false);
        }
    }, []);

    useEffect(() => {
        void refreshStats(maps);
    }, [maps, refreshStats]);

    const rows: MapRow[] = useMemo(
        () => maps.map((name) => ({ name, stats: statsByMap[name] ?? {} })),
        [maps, statsByMap],
    );

    const handleCreate = useCallback(async (request: { name: string; indexSize: number; extraBucketCount: number; workerCount: number }): Promise<void> => {
        try {
            await API.fwstate.createMap({
                name: request.name,
                index_size: request.indexSize,
                extra_bucket_count: request.extraBucketCount,
                worker_count: request.workerCount,
            });
            toaster.success('fwstate-map-create', `Created fwstate-map "${request.name}".`);
            setCreateOpen(false);
            onChanged();
        } catch (err) {
            toaster.error('fwstate-map-create', 'Failed to create fwstate-map', err);
            throw err;
        }
    }, [onChanged]);

    const handleInsertLayer = useCallback(async (request: { indexSize: number; extraBucketCount: number; workerCount: number }): Promise<void> => {
        if (!insertTarget) return;
        try {
            await API.fwstate.insertLayer({
                name: insertTarget,
                index_size: request.indexSize,
                extra_bucket_count: request.extraBucketCount,
                worker_count: request.workerCount,
            });
            toaster.success('fwstate-map-layer', `Inserted layer into "${insertTarget}".`);
            setInsertTarget(null);
            await refreshStats(maps);
        } catch (err) {
            toaster.error('fwstate-map-layer', 'Failed to insert layer', err);
            throw err;
        }
    }, [insertTarget, maps, refreshStats]);

    const handleDelete = useCallback(async (): Promise<void> => {
        if (!deleteTarget) return;
        const target = deleteTarget;
        setDeleting(true);
        try {
            await API.fwstate.deleteMap({ name: target });
            toaster.success('fwstate-map-delete', `Deleted fwstate-map "${target}".`);
            setDeleteTarget(null);
            onChanged();
        } catch (err) {
            // The server returns "fwstate-map X is referenced by configs: [...]"
            // in err.message; the toaster surfaces the full detail so the user
            // can see which configs block deletion.
            toaster.error('fwstate-map-delete', `Failed to delete fwstate-map "${target}"`, err);
        } finally {
            setDeleting(false);
        }
    }, [deleteTarget, onChanged]);

    return (
        <section className="fwstate-acl-panel">
            <div className="fwstate-subtab-frame__head" style={{ paddingInline: 0 }}>
                <Text variant="subheader-2">FWState maps</Text>
                <div className="fwstate-subtab-frame__actions">
                    <Button view="action" onClick={() => setCreateOpen(true)}>
                        <Icon data={Plus} size={16} />
                        Create map
                    </Button>
                </div>
            </div>
            <p className="fws-link-note">
                A fwstate-map is a standalone {`{v4, v6}`} map pair referenced by name from FWState configs.
                Configs that reference a map block its deletion.
            </p>

            <div className="fwstate-table-shell fwstate-acl-table-shell">
                {statsLoading && rows.length === 0 ? (
                    <PageLoader loading size="m" />
                ) : rows.length === 0 ? (
                    <div className="fwstate-maps-empty">
                        <div>No fwstate-maps found.</div>
                        <Button size="s" view="outlined" onClick={() => setCreateOpen(true)}>Create map</Button>
                    </div>
                ) : (
                    <Table
                        data={rows}
                        columns={[
                            {
                                id: 'name',
                                name: 'Name',
                                template: (row) => <span className="fwstate-table-cell">{row.name}</span>,
                            },
                            {
                                id: 'layers',
                                name: 'Layers',
                                template: (row) => (
                                    <PairCell
                                        v4={String(row.stats.v4?.layer_count ?? '-')}
                                        v6={String(row.stats.v6?.layer_count ?? '-')}
                                    />
                                ),
                            },
                            {
                                id: 'elements',
                                name: 'State entries',
                                template: (row) => (
                                    <PairCell
                                        v4={fmtCompact(toNumberOrZero(row.stats.v4?.total_elements))}
                                        v6={fmtCompact(toNumberOrZero(row.stats.v6?.total_elements))}
                                    />
                                ),
                            },
                            {
                                id: 'chain',
                                name: 'Max chain',
                                template: (row) => (
                                    <PairCell
                                        v4={String(row.stats.v4?.max_chain_length ?? '-')}
                                        v6={String(row.stats.v6?.max_chain_length ?? '-')}
                                    />
                                ),
                            },
                            {
                                id: 'memory',
                                name: 'Memory',
                                template: (row) => (
                                    <PairCell
                                        v4={formatMemoryBytes(row.stats.v4?.memory_used)}
                                        v6={formatMemoryBytes(row.stats.v6?.memory_used)}
                                    />
                                ),
                            },
                            {
                                id: 'deadline',
                                name: 'Max deadline',
                                template: (row) => (
                                    <Tooltip
                                        content={`v4: ${formatNsUtc(row.stats.v4?.max_deadline)} · v6: ${formatNsUtc(row.stats.v6?.max_deadline)}`}
                                        openDelay={0}
                                    >
                                        <span className="fwstate-mono">
                                            {formatNsUtc(row.stats.v6?.max_deadline ?? row.stats.v4?.max_deadline)}
                                        </span>
                                    </Tooltip>
                                ),
                            },
                            {
                                id: 'action',
                                name: 'Action',
                                template: (row) => (
                                    <div style={{ display: 'flex', gap: 8 }}>
                                        <Button size="s" view="outlined" onClick={() => setInsertTarget(row.name)}>
                                            <Icon data={Layers} size={14} />
                                            Insert layer
                                        </Button>
                                        <Button size="s" view="outlined-danger" onClick={() => setDeleteTarget(row.name)}>
                                            <Icon data={TrashBin} size={14} />
                                            Delete
                                        </Button>
                                    </div>
                                ),
                            },
                        ]}
                    />
                )}
            </div>
            <div style={{ display: 'flex', gap: 8, padding: '8px 0', alignItems: 'center' }}>
                <span className="fwstate-maps-caption">v4 · v6 shown per metric</span>
                <div style={{ flex: 1 }} />
                <Button size="s" view="outlined" loading={statsLoading} onClick={() => void refreshStats(maps)}>
                    Refresh
                </Button>
            </div>

            <CreateMapDialog
                open={createOpen}
                onClose={() => setCreateOpen(false)}
                existingNames={maps}
                onCreate={handleCreate}
            />

            <InsertLayerDialog
                open={insertTarget !== null}
                mapName={insertTarget ?? ''}
                onClose={() => setInsertTarget(null)}
                onInsert={handleInsertLayer}
            />

            <ConfirmDialog
                open={deleteTarget !== null}
                onClose={() => setDeleteTarget(null)}
                onConfirm={handleDelete}
                loading={deleting}
                danger
                title="Delete fwstate-map"
                confirmText="Delete"
                message={deleteTarget ? <>Delete fwstate-map <code>{deleteTarget}</code>? This cannot be undone.</> : ''}
                secondaryMessage="If any FWState config references this map, the server will refuse and list the blocking configs."
            />
        </section>
    );
};
