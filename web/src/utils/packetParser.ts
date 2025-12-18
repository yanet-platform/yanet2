// Packet parser for Ethernet/IPv4/IPv6/TCP/UDP/ICMP

export interface EthernetHeader {
    dstMac: string;
    srcMac: string;
    etherType: number;
    etherTypeName: string;
}

export interface IPv4Header {
    version: number;
    ihl: number;
    dscp: number;
    ecn: number;
    totalLength: number;
    identification: number;
    flags: {
        reserved: boolean;
        dontFragment: boolean;
        moreFragments: boolean;
    };
    fragmentOffset: number;
    ttl: number;
    protocol: number;
    protocolName: string;
    headerChecksum: number;
    srcAddr: string;
    dstAddr: string;
}

export interface IPv6Header {
    version: number;
    trafficClass: number;
    flowLabel: number;
    payloadLength: number;
    nextHeader: number;
    nextHeaderName: string;
    hopLimit: number;
    srcAddr: string;
    dstAddr: string;
}

export interface TCPHeader {
    srcPort: number;
    dstPort: number;
    seqNum: number;
    ackNum: number;
    dataOffset: number;
    flags: {
        fin: boolean;
        syn: boolean;
        rst: boolean;
        psh: boolean;
        ack: boolean;
        urg: boolean;
        ece: boolean;
        cwr: boolean;
    };
    windowSize: number;
    checksum: number;
    urgentPointer: number;
}

export interface UDPHeader {
    srcPort: number;
    dstPort: number;
    length: number;
    checksum: number;
}

export interface ICMPHeader {
    type: number;
    code: number;
    checksum: number;
    typeName: string;
}

export interface ParsedPacket {
    ethernet?: EthernetHeader;
    ipv4?: IPv4Header;
    ipv6?: IPv6Header;
    tcp?: TCPHeader;
    udp?: UDPHeader;
    icmp?: ICMPHeader;
    payloadOffset: number;
    payloadLength: number;
    raw: Uint8Array;
}

// EtherType constants
const ETHERTYPE_IPV4 = 0x0800;
const ETHERTYPE_IPV6 = 0x86dd;
const ETHERTYPE_ARP = 0x0806;
const ETHERTYPE_VLAN = 0x8100;

// IP Protocol constants
const IPPROTO_ICMP = 1;
const IPPROTO_TCP = 6;
const IPPROTO_UDP = 17;
const IPPROTO_ICMPV6 = 58;

const formatMac = (bytes: Uint8Array, offset: number): string => {
    const parts: string[] = [];
    for (let i = 0; i < 6; i++) {
        parts.push(bytes[offset + i].toString(16).padStart(2, '0'));
    }
    return parts.join(':');
};

const formatIPv4 = (bytes: Uint8Array, offset: number): string => {
    return `${bytes[offset]}.${bytes[offset + 1]}.${bytes[offset + 2]}.${bytes[offset + 3]}`;
};

const formatIPv6 = (bytes: Uint8Array, offset: number): string => {
    const parts: string[] = [];
    for (let i = 0; i < 8; i++) {
        const val = (bytes[offset + i * 2] << 8) | bytes[offset + i * 2 + 1];
        parts.push(val.toString(16));
    }
    // Simple compression: find longest run of zeros
    let result = parts.join(':');
    // Replace longest run of :0:0:... with ::
    result = result.replace(/(?:^|:)0(?::0)+(?::|$)/, '::');
    return result;
};

const getEtherTypeName = (etherType: number): string => {
    switch (etherType) {
        case ETHERTYPE_IPV4: return 'IPv4';
        case ETHERTYPE_IPV6: return 'IPv6';
        case ETHERTYPE_ARP: return 'ARP';
        case ETHERTYPE_VLAN: return 'VLAN';
        default: return `0x${etherType.toString(16)}`;
    }
};

const getIPProtocolName = (protocol: number): string => {
    switch (protocol) {
        case IPPROTO_ICMP: return 'ICMP';
        case IPPROTO_TCP: return 'TCP';
        case IPPROTO_UDP: return 'UDP';
        case IPPROTO_ICMPV6: return 'ICMPv6';
        default: return `${protocol}`;
    }
};

const getICMPTypeName = (type: number, isV6: boolean): string => {
    if (isV6) {
        switch (type) {
            case 1: return 'Destination Unreachable';
            case 2: return 'Packet Too Big';
            case 3: return 'Time Exceeded';
            case 4: return 'Parameter Problem';
            case 128: return 'Echo Request';
            case 129: return 'Echo Reply';
            case 133: return 'Router Solicitation';
            case 134: return 'Router Advertisement';
            case 135: return 'Neighbor Solicitation';
            case 136: return 'Neighbor Advertisement';
            default: return `Type ${type}`;
        }
    }
    switch (type) {
        case 0: return 'Echo Reply';
        case 3: return 'Destination Unreachable';
        case 4: return 'Source Quench';
        case 5: return 'Redirect';
        case 8: return 'Echo Request';
        case 11: return 'Time Exceeded';
        case 12: return 'Parameter Problem';
        case 13: return 'Timestamp Request';
        case 14: return 'Timestamp Reply';
        default: return `Type ${type}`;
    }
};

const parseEthernet = (data: Uint8Array): EthernetHeader | null => {
    if (data.length < 14) return null;

    const etherType = (data[12] << 8) | data[13];

    return {
        dstMac: formatMac(data, 0),
        srcMac: formatMac(data, 6),
        etherType,
        etherTypeName: getEtherTypeName(etherType),
    };
};

const parseIPv4 = (data: Uint8Array, offset: number): IPv4Header | null => {
    if (data.length < offset + 20) return null;

    const versionIhl = data[offset];
    const version = versionIhl >> 4;
    const ihl = versionIhl & 0x0f;

    if (version !== 4) return null;

    const dscpEcn = data[offset + 1];
    const totalLength = (data[offset + 2] << 8) | data[offset + 3];
    const identification = (data[offset + 4] << 8) | data[offset + 5];
    const flagsFragment = (data[offset + 6] << 8) | data[offset + 7];
    const ttl = data[offset + 8];
    const protocol = data[offset + 9];
    const headerChecksum = (data[offset + 10] << 8) | data[offset + 11];

    return {
        version,
        ihl,
        dscp: dscpEcn >> 2,
        ecn: dscpEcn & 0x03,
        totalLength,
        identification,
        flags: {
            reserved: (flagsFragment & 0x8000) !== 0,
            dontFragment: (flagsFragment & 0x4000) !== 0,
            moreFragments: (flagsFragment & 0x2000) !== 0,
        },
        fragmentOffset: flagsFragment & 0x1fff,
        ttl,
        protocol,
        protocolName: getIPProtocolName(protocol),
        headerChecksum,
        srcAddr: formatIPv4(data, offset + 12),
        dstAddr: formatIPv4(data, offset + 16),
    };
};

const parseIPv6 = (data: Uint8Array, offset: number): IPv6Header | null => {
    if (data.length < offset + 40) return null;

    const vtcfl = (data[offset] << 24) | (data[offset + 1] << 16) |
                  (data[offset + 2] << 8) | data[offset + 3];
    const version = vtcfl >> 28;

    if (version !== 6) return null;

    const trafficClass = (vtcfl >> 20) & 0xff;
    const flowLabel = vtcfl & 0xfffff;
    const payloadLength = (data[offset + 4] << 8) | data[offset + 5];
    const nextHeader = data[offset + 6];
    const hopLimit = data[offset + 7];

    return {
        version,
        trafficClass,
        flowLabel,
        payloadLength,
        nextHeader,
        nextHeaderName: getIPProtocolName(nextHeader),
        hopLimit,
        srcAddr: formatIPv6(data, offset + 8),
        dstAddr: formatIPv6(data, offset + 24),
    };
};

const parseTCP = (data: Uint8Array, offset: number): TCPHeader | null => {
    if (data.length < offset + 20) return null;

    const srcPort = (data[offset] << 8) | data[offset + 1];
    const dstPort = (data[offset + 2] << 8) | data[offset + 3];
    const seqNum = (data[offset + 4] << 24) | (data[offset + 5] << 16) |
                   (data[offset + 6] << 8) | data[offset + 7];
    const ackNum = (data[offset + 8] << 24) | (data[offset + 9] << 16) |
                   (data[offset + 10] << 8) | data[offset + 11];
    const dataOffsetFlags = (data[offset + 12] << 8) | data[offset + 13];
    const dataOffset = dataOffsetFlags >> 12;
    const flags = dataOffsetFlags & 0x1ff;
    const windowSize = (data[offset + 14] << 8) | data[offset + 15];
    const checksum = (data[offset + 16] << 8) | data[offset + 17];
    const urgentPointer = (data[offset + 18] << 8) | data[offset + 19];

    return {
        srcPort,
        dstPort,
        seqNum: seqNum >>> 0, // Convert to unsigned
        ackNum: ackNum >>> 0,
        dataOffset,
        flags: {
            fin: (flags & 0x01) !== 0,
            syn: (flags & 0x02) !== 0,
            rst: (flags & 0x04) !== 0,
            psh: (flags & 0x08) !== 0,
            ack: (flags & 0x10) !== 0,
            urg: (flags & 0x20) !== 0,
            ece: (flags & 0x40) !== 0,
            cwr: (flags & 0x80) !== 0,
        },
        windowSize,
        checksum,
        urgentPointer,
    };
};

const parseUDP = (data: Uint8Array, offset: number): UDPHeader | null => {
    if (data.length < offset + 8) return null;

    return {
        srcPort: (data[offset] << 8) | data[offset + 1],
        dstPort: (data[offset + 2] << 8) | data[offset + 3],
        length: (data[offset + 4] << 8) | data[offset + 5],
        checksum: (data[offset + 6] << 8) | data[offset + 7],
    };
};

const parseICMP = (data: Uint8Array, offset: number, isV6: boolean): ICMPHeader | null => {
    if (data.length < offset + 4) return null;

    const type = data[offset];
    const code = data[offset + 1];
    const checksum = (data[offset + 2] << 8) | data[offset + 3];

    return {
        type,
        code,
        checksum,
        typeName: getICMPTypeName(type, isV6),
    };
};

export const parsePacket = (data: Uint8Array): ParsedPacket => {
    const result: ParsedPacket = {
        payloadOffset: 0,
        payloadLength: data.length,
        raw: data,
    };

    let offset = 0;

    // Parse Ethernet
    const ethernet = parseEthernet(data);
    if (!ethernet) return result;
    result.ethernet = ethernet;
    offset = 14;

    // Handle VLAN tag
    let etherType = ethernet.etherType;
    if (etherType === ETHERTYPE_VLAN) {
        if (data.length < offset + 4) return result;
        etherType = (data[offset + 2] << 8) | data[offset + 3];
        offset += 4;
    }

    // Parse IP layer
    let ipProtocol: number | null = null;
    let isV6 = false;

    if (etherType === ETHERTYPE_IPV4) {
        const ipv4 = parseIPv4(data, offset);
        if (ipv4) {
            result.ipv4 = ipv4;
            ipProtocol = ipv4.protocol;
            offset += ipv4.ihl * 4;
        }
    } else if (etherType === ETHERTYPE_IPV6) {
        const ipv6 = parseIPv6(data, offset);
        if (ipv6) {
            result.ipv6 = ipv6;
            ipProtocol = ipv6.nextHeader;
            isV6 = true;
            offset += 40;
        }
    }

    if (ipProtocol === null) {
        result.payloadOffset = offset;
        result.payloadLength = data.length - offset;
        return result;
    }

    // Parse transport layer
    if (ipProtocol === IPPROTO_TCP) {
        const tcp = parseTCP(data, offset);
        if (tcp) {
            result.tcp = tcp;
            offset += tcp.dataOffset * 4;
        }
    } else if (ipProtocol === IPPROTO_UDP) {
        const udp = parseUDP(data, offset);
        if (udp) {
            result.udp = udp;
            offset += 8;
        }
    } else if (ipProtocol === IPPROTO_ICMP || ipProtocol === IPPROTO_ICMPV6) {
        const icmp = parseICMP(data, offset, isV6);
        if (icmp) {
            result.icmp = icmp;
            offset += 8;
        }
    }

    result.payloadOffset = offset;
    result.payloadLength = Math.max(0, data.length - offset);

    return result;
};

// Format TCP flags as a string like "[SYN, ACK]"
export const formatTCPFlags = (flags: TCPHeader['flags']): string => {
    const parts: string[] = [];
    if (flags.syn) parts.push('SYN');
    if (flags.ack) parts.push('ACK');
    if (flags.fin) parts.push('FIN');
    if (flags.rst) parts.push('RST');
    if (flags.psh) parts.push('PSH');
    if (flags.urg) parts.push('URG');
    if (flags.ece) parts.push('ECE');
    if (flags.cwr) parts.push('CWR');
    return parts.length > 0 ? `[${parts.join(', ')}]` : '';
};

// Format packet as a tcpdump-like line
export const formatPacketLine = (packet: ParsedPacket): string => {
    const parts: string[] = [];

    // Source and destination
    let src = '';
    let dst = '';
    let proto = '';

    if (packet.ipv4) {
        src = packet.ipv4.srcAddr;
        dst = packet.ipv4.dstAddr;
        proto = packet.ipv4.protocolName;
    } else if (packet.ipv6) {
        src = packet.ipv6.srcAddr;
        dst = packet.ipv6.dstAddr;
        proto = packet.ipv6.nextHeaderName;
    } else if (packet.ethernet) {
        src = packet.ethernet.srcMac;
        dst = packet.ethernet.dstMac;
        proto = packet.ethernet.etherTypeName;
    }

    // Add ports for TCP/UDP
    if (packet.tcp) {
        src = `${src}:${packet.tcp.srcPort}`;
        dst = `${dst}:${packet.tcp.dstPort}`;
        proto = 'TCP';
    } else if (packet.udp) {
        src = `${src}:${packet.udp.srcPort}`;
        dst = `${dst}:${packet.udp.dstPort}`;
        proto = 'UDP';
    } else if (packet.icmp) {
        proto = packet.icmp.typeName;
    }

    parts.push(`${src} > ${dst}`);
    parts.push(proto);

    // Add flags for TCP
    if (packet.tcp) {
        const flags = formatTCPFlags(packet.tcp.flags);
        if (flags) parts.push(flags);
        parts.push(`seq ${packet.tcp.seqNum}`);
        if (packet.tcp.flags.ack) {
            parts.push(`ack ${packet.tcp.ackNum}`);
        }
        parts.push(`win ${packet.tcp.windowSize}`);
    }

    // Add length
    parts.push(`length ${packet.raw.length}`);

    return parts.join(' ');
};

// Format bytes as hex dump
export const formatHexDump = (data: Uint8Array, bytesPerLine: number = 16): string => {
    const lines: string[] = [];

    for (let i = 0; i < data.length; i += bytesPerLine) {
        const slice = data.slice(i, Math.min(i + bytesPerLine, data.length));

        // Offset
        const offset = i.toString(16).padStart(4, '0');

        // Hex bytes
        const hexParts: string[] = [];
        for (let j = 0; j < bytesPerLine; j++) {
            if (j < slice.length) {
                hexParts.push(slice[j].toString(16).padStart(2, '0'));
            } else {
                hexParts.push('  ');
            }
        }
        const hex = hexParts.join(' ');

        // ASCII representation
        const ascii = Array.from(slice)
            .map(b => (b >= 32 && b < 127) ? String.fromCharCode(b) : '.')
            .join('');

        lines.push(`${offset}  ${hex}  ${ascii}`);
    }

    return lines.join('\n');
};

// Decode base64 to Uint8Array
export const base64ToUint8Array = (base64: string): Uint8Array => {
    const binary = atob(base64);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) {
        bytes[i] = binary.charCodeAt(i);
    }
    return bytes;
};

