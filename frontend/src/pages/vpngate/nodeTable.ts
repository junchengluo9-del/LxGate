/*
 * Pure helpers for the VPNGate node table, kept outside the page so the sort
 * order and number formatting can be tested without rendering the table.
 */

export interface LatencyCarrier {
  latency_ms?: number;
  ping?: number;
}

/** The measured latency of a node, or null when no probe answered yet. */
export function nodeLatency(node: LatencyCarrier): number | null {
  const value = Number(node.latency_ms || node.ping || 0);
  return Number.isFinite(value) && value > 0 ? value : null;
}

/**
 * antd Table sorter for latency. antd reverses the comparator result for a
 * descending sort, so a node without a measurement is pushed to the bottom
 * in both directions by flipping its sign with the active order.
 */
export function compareLatency(
  a: LatencyCarrier,
  b: LatencyCarrier,
  sortOrder?: 'ascend' | 'descend' | null,
): number {
  const left = nodeLatency(a);
  const right = nodeLatency(b);
  if (left === null && right === null) return 0;
  const last = sortOrder === 'descend' ? -1 : 1;
  if (left === null) return last;
  if (right === null) return -last;
  return left - right;
}

const countFormat = new Intl.NumberFormat('en-US');

/** Group digits (627366 → 627,366) so large VPNGate scores stay readable. */
export function formatCount(value: number | null | undefined): string {
  const n = Number(value || 0);
  return countFormat.format(Number.isFinite(n) ? n : 0);
}
