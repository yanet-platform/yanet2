import type { PdumpConfig, PdumpRecord } from '../../../api/pdump';
import type { ParsedPacket } from '../../../utils';

export interface PdumpConfigInfo {
    name: string;
    config?: PdumpConfig;
}

export interface CapturedPacket {
    id: number;
    timestamp: Date;
    record: PdumpRecord;
    parsed: ParsedPacket;
}

export type PacketSortColumn = 'index' | 'time' | 'source' | 'destination' | 'protocol' | 'length';
export type SortDirection = 'asc' | 'desc';

export interface PacketSortState {
    column: PacketSortColumn | null;
    direction: SortDirection;
}
