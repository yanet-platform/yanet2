import type { ParsedPacket } from '../../utils/packetParser';

/** A generic displayable packet — id for virtual-list keying, parsed for rendering. */
export interface PacketRow {
    id: number;
    parsed: ParsedPacket;
    /** Optional timestamp to show in the Time column. */
    timestamp?: Date;
}

/** Columns that can be used to sort the packet table. */
export type PacketSortColumn = 'index' | 'time' | 'source' | 'destination' | 'protocol' | 'length';
export type PacketSortDirection = 'asc' | 'desc';

export interface PacketSortState {
    column: PacketSortColumn | null;
    direction: PacketSortDirection;
}
