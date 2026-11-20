import type { EquityHistoryPoint } from '../types';

interface TraderHistory {
  traderId: string;
  points: EquityHistoryPoint[];
}

export interface ComparisonPoint {
  timestamp: number;
  samples: ReadonlyMap<string, EquityHistoryPoint>;
}

export function alignComparisonHistory(histories: TraderHistory[]): ComparisonPoint[] {
  const byTime = new Map<number, Map<string, EquityHistoryPoint>>();

  for (const { traderId, points } of histories) {
    for (const point of points) {
      const timestamp = point.timestamp_ms ?? Date.parse(point.timestamp.replace(' ', 'T'));
      if (!Number.isFinite(timestamp)) continue;
      let samples = byTime.get(timestamp);
      if (!samples) {
        samples = new Map();
        byTime.set(timestamp, samples);
      }
      samples.set(traderId, point);
    }
  }

  return Array.from(byTime, ([timestamp, samples]) => ({ timestamp, samples }))
    .sort((a, b) => a.timestamp - b.timestamp);
}

// Compare both traders as of the earlier of their latest sample times.
export function getCurrentComparison(points: ComparisonPoint[], traderIds: string[]) {
  if (traderIds.length !== 2) return null;
  const [firstId, secondId] = traderIds;
  const latest = new Map<string, number>();

  for (let i = points.length - 1; i >= 0; i--) {
    for (const id of traderIds) {
      if (!latest.has(id) && points[i].samples.has(id)) latest.set(id, i);
    }
    if (latest.size === 2) break;
  }

  const firstIndex = latest.get(firstId);
  const secondIndex = latest.get(secondId);
  if (firstIndex === undefined || secondIndex === undefined) return null;

  const end = Math.min(firstIndex, secondIndex);
  const asOf = points[end].timestamp;
  const samples = traderIds.map(id => {
    for (let i = end; i >= 0; i--) {
      const sample = points[i].samples.get(id);
      if (sample) return { timestamp: points[i].timestamp, pnlPct: sample.total_pnl_pct };
    }
    return undefined;
  });
  const [first, second] = samples;
  if (!first || !second) return null;

  return { asOf, gap: Math.abs(first.pnlPct - second.pnlPct), samples: [first, second] };
}
