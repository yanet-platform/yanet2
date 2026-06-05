/** Result of a fuzzy match — score and matched char spans. */
export interface FuzzyMatchResult {
    score: number;
    ranges: Array<[number, number]>;
}

/**
 * Case-insensitive fuzzy subsequence matcher.
 *
 * Returns null when query is not a subsequence of text. Returns a result with
 * score 0 and empty ranges for an empty query. Higher scores indicate better
 * matches. Scoring rewards: contiguous runs of matched characters, matches at
 * word boundaries (start of word / after separator), and prefix matches.
 */
export const fuzzyMatch = (query: string, text: string): FuzzyMatchResult | null => {
    if (query.length === 0) {
        return { score: 0, ranges: [] };
    }

    const q = query.toLowerCase();
    const t = text.toLowerCase();

    let qi = 0;
    let ti = 0;
    const matchedIndices: number[] = [];

    while (qi < q.length && ti < t.length) {
        if (q[qi] === t[ti]) {
            matchedIndices.push(ti);
            qi++;
        }
        ti++;
    }

    if (qi < q.length) {
        return null;
    }

    let score = 0;

    let runLength = 1;
    for (let k = 1; k < matchedIndices.length; k++) {
        if (matchedIndices[k] === matchedIndices[k - 1] + 1) {
            runLength++;
            score += runLength * 2;
        } else {
            runLength = 1;
        }
    }

    for (const idx of matchedIndices) {
        if (idx === 0) {
            score += 10;
        } else {
            const prev = t[idx - 1];
            if (prev === ' ' || prev === '-' || prev === '_' || prev === '.' || prev === '/') {
                score += 8;
            }
        }
    }

    if (matchedIndices[0] === 0) {
        score += 5;
    }

    const ranges: Array<[number, number]> = [];
    if (matchedIndices.length > 0) {
        let start = matchedIndices[0];
        let end = matchedIndices[0] + 1;
        for (let k = 1; k < matchedIndices.length; k++) {
            if (matchedIndices[k] === matchedIndices[k - 1] + 1) {
                end = matchedIndices[k] + 1;
            } else {
                ranges.push([start, end]);
                start = matchedIndices[k];
                end = matchedIndices[k] + 1;
            }
        }
        ranges.push([start, end]);
    }

    return { score, ranges };
};
