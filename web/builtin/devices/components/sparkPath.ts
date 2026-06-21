/** Returns the SVG path `d` string for a sparkline polyline. */
export const pathFor = (values: number[], w: number, h: number, max: number, pad: number): string => {
    if (!values.length) return '';
    const stepX = (w - pad * 2) / (values.length - 1 || 1);
    let d = '';
    for (let idx = 0; idx < values.length; idx++) {
        const x = pad + idx * stepX;
        const y = h - pad - (values[idx] / max) * (h - pad * 2);
        d += (idx === 0 ? 'M' : 'L') + x.toFixed(2) + ' ' + y.toFixed(2) + ' ';
    }
    return d;
};

/** Returns the SVG path `d` string for a filled sparkline area. */
export const areaFor = (values: number[], w: number, h: number, max: number, pad: number): string => {
    if (!values.length) return '';
    const stepX = (w - pad * 2) / (values.length - 1 || 1);
    let d = `M${pad} ${h - pad} `;
    for (let idx = 0; idx < values.length; idx++) {
        const x = pad + idx * stepX;
        const y = h - pad - (values[idx] / max) * (h - pad * 2);
        d += 'L' + x.toFixed(2) + ' ' + y.toFixed(2) + ' ';
    }
    d += `L${w - pad} ${h - pad} Z`;
    return d;
};
