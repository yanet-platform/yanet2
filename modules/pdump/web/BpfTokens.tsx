import React, { useMemo } from 'react';

interface BpfTokensProps {
    expr: string;
}

type TokenClass = 'kw' | 'proto' | 'num' | 'host' | 'txt';

interface Token {
    text: string;
    cls: TokenClass;
}

const KW_SET = new Set(['and', 'or', 'not', 'src', 'dst', '&&', '||', '!']);
const PROTO_SET = new Set([
    'tcp', 'udp', 'icmp', 'icmp6', 'ip', 'ip6', 'ether', 'arp', 'port', 'host', 'net',
    'portrange', 'proto', 'broadcast', 'multicast', 'less', 'greater',
]);

const RE_NUM = /^(?:0x[0-9a-fA-F]+|\d+)$/;
const RE_HOST = /^(?:[0-9]{1,3}\.){3}[0-9]{1,3}$|^(?:[0-9a-fA-F]{0,4}:){2,7}[0-9a-fA-F]{0,4}$|^(?:[0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}$/;

const classify = (word: string): TokenClass => {
    const lw = word.toLowerCase();
    if (KW_SET.has(lw)) return 'kw';
    if (PROTO_SET.has(lw)) return 'proto';
    if (RE_NUM.test(word)) return 'num';
    if (RE_HOST.test(word)) return 'host';
    return 'txt';
};

const tokenize = (expr: string): Token[] => {
    if (!expr.trim()) return [];
    const parts = expr.split(/(\s+|[()[\]])/);
    const tokens: Token[] = [];
    for (const part of parts) {
        if (part === '') continue;
        if (/^\s+$/.test(part)) {
            tokens.push({ text: part, cls: 'txt' });
        } else if (part === '(' || part === ')' || part === '[' || part === ']') {
            tokens.push({ text: part, cls: 'txt' });
        } else {
            tokens.push({ text: part, cls: classify(part) });
        }
    }
    return tokens;
};

/**
 * Renders a BPF expression with syntax-highlighted coloured spans.
 */
const BpfTokens: React.FC<BpfTokensProps> = ({ expr }) => {
    const tokens = useMemo(() => tokenize(expr), [expr]);

    if (!expr) {
        return <span className="pdump-bpf-empty">(no filter)</span>;
    }
    return (
        <>
            {tokens.map((tok, idx) => (
                <span key={idx} className={`pdump-bpf-tok pdump-bpf-tok--${tok.cls}`}>
                    {tok.text}
                </span>
            ))}
        </>
    );
};

export default BpfTokens;
