import React, { useMemo, useState } from 'react';
import { Dialog, Flex, Text } from '@gravity-ui/uikit';
import { diffLines } from 'diff';
import type { Change } from 'diff';

/** One row in the side-by-side diff view. */
interface SbsRow {
    left: string | null;
    right: string | null;
    kind: 'added' | 'removed' | 'context';
    rowIdx: number;
}

const buildSbsRows = (changes: Change[]): SbsRow[] => {
    const rows: SbsRow[] = [];
    let rowIdx = 0;

    for (const change of changes) {
        const lines = change.value.split('\n');
        const trimmed = lines[lines.length - 1] === '' ? lines.slice(0, -1) : lines;

        if (change.removed) {
            for (const line of trimmed) {
                rows.push({ left: line, right: null, kind: 'removed', rowIdx: rowIdx++ });
            }
        } else if (change.added) {
            for (const line of trimmed) {
                rows.push({ left: null, right: line, kind: 'added', rowIdx: rowIdx++ });
            }
        } else {
            for (const line of trimmed) {
                rows.push({ left: line, right: line, kind: 'context', rowIdx: rowIdx++ });
            }
        }
    }

    return rows;
};

const CELL_STYLE: React.CSSProperties = {
    flex: 1,
    minWidth: 0,
    overflowX: 'auto',
    fontFamily: 'ui-monospace, monospace',
    fontSize: 12,
    lineHeight: '1.6',
    whiteSpace: 'pre',
    padding: '0 12px',
    userSelect: 'text',
};

const ROW_STYLE: React.CSSProperties = {
    display: 'flex',
    minHeight: '1.6em',
    borderBottom: '1px solid transparent',
};

const getRowBg = (kind: SbsRow['kind'], side: 'left' | 'right'): string => {
    if (kind === 'removed' && side === 'left') {
        return 'color-mix(in srgb, var(--g-color-text-danger) 10%, transparent)';
    }
    if (kind === 'added' && side === 'right') {
        return 'color-mix(in srgb, var(--g-color-text-positive) 10%, transparent)';
    }
    return 'transparent';
};

const getTextColor = (kind: SbsRow['kind'], side: 'left' | 'right'): string => {
    if (kind === 'removed' && side === 'left') return 'var(--g-color-text-danger)';
    if (kind === 'added' && side === 'right') return 'var(--g-color-text-positive)';
    if (kind === 'context') return 'var(--g-color-text-secondary)';
    return 'var(--g-color-text-hint)';
};

/** Side-by-side diff view of two YAML strings. */
export const SideBySideDiff = ({ changes }: { changes: Change[] }): React.JSX.Element => {
    const rows = useMemo(() => buildSbsRows(changes), [changes]);

    return (
        <div style={{
            display: 'flex',
            fontFamily: 'ui-monospace, monospace',
            fontSize: 12,
            lineHeight: '1.6',
            border: '1px solid var(--g-color-line-generic)',
            borderRadius: 4,
            overflow: 'hidden',
        }}>
            <div style={{ flex: 1, minWidth: 0, borderRight: '1px solid var(--g-color-line-generic)' }}>
                <div style={{
                    padding: '2px 12px',
                    background: 'var(--g-color-base-generic)',
                    borderBottom: '1px solid var(--g-color-line-generic)',
                    fontSize: 11,
                    color: 'var(--g-color-text-hint)',
                    fontWeight: 600,
                    letterSpacing: '0.3px',
                    textTransform: 'uppercase',
                }}>
                    Before (server)
                </div>
                {rows.map((row) => (
                    <div
                        key={`l-${row.rowIdx}`}
                        style={{ ...ROW_STYLE, background: getRowBg(row.kind, 'left') }}
                    >
                        <span style={{ ...CELL_STYLE, color: getTextColor(row.kind, 'left') }}>
                            {row.left ?? ''}
                        </span>
                    </div>
                ))}
            </div>
            <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{
                    padding: '2px 12px',
                    background: 'var(--g-color-base-generic)',
                    borderBottom: '1px solid var(--g-color-line-generic)',
                    fontSize: 11,
                    color: 'var(--g-color-text-hint)',
                    fontWeight: 600,
                    letterSpacing: '0.3px',
                    textTransform: 'uppercase',
                }}>
                    After (local)
                </div>
                {rows.map((row) => (
                    <div
                        key={`r-${row.rowIdx}`}
                        style={{ ...ROW_STYLE, background: getRowBg(row.kind, 'right') }}
                    >
                        <span style={{ ...CELL_STYLE, color: getTextColor(row.kind, 'right') }}>
                            {row.right ?? ''}
                        </span>
                    </div>
                ))}
            </div>
        </div>
    );
};

export interface SaveDiffModalProps {
    /** Config name shown in the dialog caption. */
    configName: string;
    /** Server-side YAML string (left / "before" pane). */
    beforeYaml: string;
    /** Draft YAML string (right / "after" pane). */
    afterYaml: string;
    /** Optional extra warning rendered above the diff (e.g. validation count). */
    warning?: React.ReactNode;
    /** Label for the apply button. Defaults to "Apply". */
    applyLabel?: string;
    onClose: () => void;
    /** Called when the user confirms. Should throw on failure so the error banner fires. */
    onApply: () => Promise<void>;
}

/**
 * Generic modal showing a side-by-side YAML diff with an Apply button.
 * Callers supply pre-rendered YAML strings; this component owns diff computation
 * and the Gravity Dialog chrome.
 */
export const SaveDiffModal: React.FC<SaveDiffModalProps> = ({
    configName,
    beforeYaml,
    afterYaml,
    warning,
    applyLabel = 'Apply',
    onClose,
    onApply,
}) => {
    const [applying, setApplying] = useState(false);
    const [applyError, setApplyError] = useState<string | null>(null);

    const changes = useMemo(() => diffLines(beforeYaml, afterYaml), [beforeYaml, afterYaml]);

    const handleApply = async (): Promise<void> => {
        setApplying(true);
        setApplyError(null);
        try {
            await onApply();
            onClose();
        } catch (err) {
            setApplyError(err instanceof Error ? err.message : String(err));
        } finally {
            setApplying(false);
        }
    };

    return (
        <Dialog
            open
            onClose={onClose}
            size="l"
            contentOverflow="auto"
        >
            <Dialog.Header caption={`Review changes — ${configName}`} />
            <Dialog.Body>
                <Flex direction="column" gap={3}>
                    {applyError && (
                        <Text variant="caption-1" color="danger">{applyError}</Text>
                    )}
                    {warning}
                    <div style={{ maxHeight: '60vh', overflowY: 'auto' }}>
                        <SideBySideDiff changes={changes} />
                    </div>
                </Flex>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonCancel={onClose}
                onClickButtonApply={handleApply}
                textButtonCancel="Cancel"
                textButtonApply={applying ? `${applyLabel}…` : applyLabel}
                loading={applying}
                propsButtonApply={{ disabled: applying }}
            />
        </Dialog>
    );
};
