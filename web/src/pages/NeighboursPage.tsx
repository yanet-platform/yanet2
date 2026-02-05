import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Table, withTableSorting, Box } from '@gravity-ui/uikit';
import type { TableColumnConfig, TableSortState } from '@gravity-ui/uikit';
import { toaster } from '../utils';
import { API } from '../api';
import type { Neighbour } from '../api/neighbours';
import { formatMACAddress, getMACAddressValue, compareMACAddressValues } from '../utils/mac';
import { getUTCOffsetString } from '../utils/date';
import { getNUDStateString } from '../utils/nud';
import { PageLayout, PageLoader } from '../components';
import {
    compareNullableNumbers,
    compareNullableStrings,
    formatUnixSeconds,
    getUnixSecondsValue,
} from '../utils/sorting';
import './NeighboursPage.css';

const REFRESH_INTERVAL_MS = 5000;

const SortableTable = withTableSorting(Table);

// Helper functions for URL sort state management
const parseSortFromURL = (searchParams: URLSearchParams): TableSortState => {
    const sortColumn = searchParams.get('sortColumn');
    const sortOrder = searchParams.get('sortOrder');

    if (!sortColumn || !sortOrder || (sortOrder !== 'asc' && sortOrder !== 'desc')) {
        return [{ column: 'state', order: 'asc' }];
    }

    return [{ column: sortColumn, order: sortOrder as 'asc' | 'desc' }];
};

const serializeSortToURL = (sortState: TableSortState, searchParams: URLSearchParams): URLSearchParams => {
    const newParams = new URLSearchParams(searchParams);

    if (!sortState || sortState.length === 0) {
        newParams.set('sortColumn', 'state');
        newParams.set('sortOrder', 'asc');
    } else {
        const { column, order } = sortState[0];
        newParams.set('sortColumn', column);
        newParams.set('sortOrder', order);
    }

    return newParams;
};


const renderMacAddress = (addr?: Neighbour['link_addr']): string => {
    if (addr?.addr === undefined) {
        return '-';
    }

    return formatMACAddress(addr.addr);
};

const NeighboursPage = (): React.JSX.Element => {
    const [searchParams, setSearchParams] = useSearchParams();
    const [neighbours, setNeighbours] = useState<Neighbour[]>([]);
    const [loading, setLoading] = useState<boolean>(true);

    const utcOffsetString = useMemo(() => getUTCOffsetString(), []);

    // Get sort state from URL
    const sortState = useMemo(() => {
        return parseSortFromURL(searchParams);
    }, [searchParams]);

    // Handle sort state changes
    const handleSortStateChange = useCallback((newSortState: TableSortState) => {
        const newParams = serializeSortToURL(newSortState, searchParams);
        setSearchParams(newParams, { replace: true });
    }, [searchParams]);


    useEffect(() => {
        let isMounted = true;

        const loadNeighbours = async (withLoader: boolean): Promise<void> => {
            if (withLoader) {
                setLoading(true);
            }

            try {
                const data = await API.neighbours.list();
                if (!isMounted) {
                    return;
                }
                setNeighbours(data.neighbours || []);
            } catch (err) {
                if (!isMounted) {
                    return;
                }
                toaster.error('neighbours-error', 'Failed to fetch neighbours', err);
            } finally {
                if (withLoader && isMounted) {
                    setLoading(false);
                }
            }
        };

        loadNeighbours(true);

        const intervalId = window.setInterval(() => {
            loadNeighbours(false);
        }, REFRESH_INTERVAL_MS);

        return () => {
            isMounted = false;
            window.clearInterval(intervalId);
        };
    }, []);

    const columns: TableColumnConfig<Neighbour>[] = useMemo(() => [
        {
            id: 'next_hop',
            name: 'Next Hop',
            meta: {
                sort: (a: Neighbour, b: Neighbour) => compareNullableStrings(a.next_hop, b.next_hop),
            },
            template: (item: Neighbour) => item.next_hop || '-',
        },
        {
            id: 'link_addr',
            name: 'Neighbour MAC',
            meta: {
                sort: (a: Neighbour, b: Neighbour) => {
                    const valA = getMACAddressValue(a.link_addr?.addr);
                    const valB = getMACAddressValue(b.link_addr?.addr);
                    return compareMACAddressValues(valA, valB);
                },
            },
            template: (item: Neighbour) => renderMacAddress(item.link_addr),
        },
        {
            id: 'hardware_addr',
            name: 'Interface MAC',
            meta: {
                sort: (a: Neighbour, b: Neighbour) => {
                    const valA = getMACAddressValue(a.hardware_addr?.addr);
                    const valB = getMACAddressValue(b.hardware_addr?.addr);
                    return compareMACAddressValues(valA, valB);
                },
            },
            template: (item: Neighbour) => renderMacAddress(item.hardware_addr),
        },
        {
            id: 'state',
            name: 'State',
            meta: {
                sort: (a: Neighbour, b: Neighbour) => {
                    const stateA = a.state ?? 0;
                    const stateB = b.state ?? 0;
                    if (stateA !== stateB) {
                        return stateA - stateB;
                    }

                    return compareNullableStrings(a.next_hop, b.next_hop);
                },
            },
            template: (item: Neighbour) => getNUDStateString(item.state),
        },
        {
            id: 'updated_at',
            name: `Updated At (UTC${utcOffsetString})`,
            meta: {
                sort: (a: Neighbour, b: Neighbour) => compareNullableNumbers(
                    getUnixSecondsValue(a.updated_at),
                    getUnixSecondsValue(b.updated_at)
                ),
            },
            template: (item: Neighbour) => formatUnixSeconds(item.updated_at),
        },
    ], [utcOffsetString]);

    const getRowDescriptor = useCallback(() => ({ classNames: ['neighbours-row'] }), []);

    if (loading) {
        return (
            <PageLayout title="Neighbours">
                <PageLoader loading={loading} size="l" />
            </PageLayout>
        );
    }

    return (
        <PageLayout title="Neighbours">
            <Box spacing={{ p: 5 }} style={{ width: '100%', minWidth: 0 }}>
                <SortableTable
                    data={neighbours}
                    columns={columns}
                    width="max"
                    defaultSortState={sortState}
                    onSortStateChange={handleSortStateChange}
                    getRowDescriptor={getRowDescriptor}
                />
            </Box>
        </PageLayout>
    );
};

export default NeighboursPage;
