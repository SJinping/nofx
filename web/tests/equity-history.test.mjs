import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import ts from 'typescript';

const root = new URL('../src/', import.meta.url);
const apiSource = readFileSync(new URL('lib/api.ts', root), 'utf8');
const js = ts.transpileModule(apiSource, { compilerOptions: { module: ts.ModuleKind.ESNext } }).outputText;
const { api } = await import(`data:text/javascript;base64,${Buffer.from(js).toString('base64')}`);

test('equity API opts into lightweight actions, with no full decisions request', async () => {
  const originalFetch = globalThis.fetch;
  const urls = [];
  globalThis.fetch = async url => { urls.push(String(url)); return { ok: true, json: async () => [] }; };
  try {
    await api.getEquityHistory('a b', 2000, true);
    const url = new URL(urls[0], 'http://test');
    assert.equal(url.searchParams.get('include_actions'), 'true');
    assert.equal(url.searchParams.get('trader_id'), 'a b');
    await api.getEquityHistory('a b', 2000);
    assert.equal(new URL(urls[1], 'http://test').searchParams.has('include_actions'), false);
  } finally { globalThis.fetch = originalFetch; }
});

test('equity chart markers consume indexed actions instead of /decisions polling', () => {
  const chart = readFileSync(new URL('components/EquityChart.tsx', root), 'utf8');
  assert.doesNotMatch(chart, /api\.getDecisions|useSWR<DecisionRecord/);
  assert.match(chart, /getEquityHistory\(traderId, 2000, true\)/);
  assert.match(chart, /point\.actions/);
  assert.match(chart, /hasRealTrade/);
});

const comparisonSource = readFileSync(new URL('lib/comparisonHistory.ts', root), 'utf8');
const comparisonJs = ts.transpileModule(comparisonSource, { compilerOptions: { module: ts.ModuleKind.ESNext } }).outputText;
const { alignComparisonHistory, getCurrentComparison } = await import(
  `data:text/javascript;base64,${Buffer.from(comparisonJs).toString('base64')}`
);

const point = (time, cycle, pnl) => ({
  timestamp: '2026-11-20 00:00:00',
  timestamp_ms: Date.parse(`2026-11-20T08:${time}Z`),
  cycle_number: cycle,
  total_pnl_pct: pnl,
});

test('comparison places independent cycles on the same clock without inventing samples', () => {
  const a = [point('10:00', 1000, 2), point('00:00', 999, 1)];
  const v = [point('03:00', 999, -1), point('06:00', 1000, 1), point('06:00.250', 1001, 3)];
  const aligned = alignComparisonHistory([{ traderId: 'v', points: v }, { traderId: 'a', points: a }]);
  assert.deepEqual(aligned.map(row => row.timestamp), [a[1], ...v, a[0]].map(row => row.timestamp_ms));
  assert.equal(aligned[1].samples.get('v').cycle_number, 999);
  assert.equal(aligned[1].samples.has('a'), false);
  assert.deepEqual(getCurrentComparison(aligned, ['a', 'v']), {
    asOf: v[2].timestamp_ms,
    gap: 2,
    samples: [{ timestamp: a[1].timestamp_ms, pnlPct: 1 }, { timestamp: v[2].timestamp_ms, pnlPct: 3 }],
  });
});

test('comparison groups simultaneous samples and leaves the gap empty before overlap', () => {
  const a = point('00:00', 1000, 4);
  const v = point('03:00', 1, 0);
  const histories = [{ traderId: 'a', points: [a] }, { traderId: 'v', points: [v] }];
  assert.equal(getCurrentComparison(alignComparisonHistory(histories), ['a', 'v']), null);
  const sameTime = [{ traderId: 'a', points: [{ ...a, timestamp_ms: v.timestamp_ms }] }, histories[1]];
  const aligned = alignComparisonHistory(sameTime);
  assert.equal(aligned.length, 1);
  assert.equal(getCurrentComparison(aligned, ['v', 'a']).gap, 4);
});
