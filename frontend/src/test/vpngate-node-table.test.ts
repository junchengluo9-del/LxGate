import { describe, expect, it } from 'vitest';

import { compareLatency, formatCount, nodeLatency } from '@/pages/vpngate/nodeTable';

const sortBy = (rows: Array<{ id: string; latency_ms?: number }>, order: 'ascend' | 'descend') => {
  const sorted = [...rows].sort((a, b) => compareLatency(a, b, order));
  // antd applies a descending sort by negating the comparator.
  if (order === 'descend') sorted.sort((a, b) => -compareLatency(a, b, order));
  return sorted.map((row) => row.id);
};

describe('VPNGate node table helpers', () => {
  const rows = [
    { id: 'unmeasured', latency_ms: 0 },
    { id: 'slow', latency_ms: 320 },
    { id: 'fast', latency_ms: 40 },
    { id: 'missing' },
  ];

  it('treats a missing or zero latency as unmeasured', () => {
    expect(nodeLatency({})).toBeNull();
    expect(nodeLatency({ latency_ms: 0 })).toBeNull();
    expect(nodeLatency({ ping: 12 })).toBe(12);
  });

  it('sorts unmeasured nodes last when ascending', () => {
    expect(sortBy(rows, 'ascend').slice(0, 2)).toEqual(['fast', 'slow']);
  });

  it('keeps unmeasured nodes last when descending', () => {
    expect(sortBy(rows, 'descend').slice(0, 2)).toEqual(['slow', 'fast']);
  });

  it('groups digits of large counts', () => {
    expect(formatCount(627366)).toBe('627,366');
    expect(formatCount(undefined)).toBe('0');
  });
});
