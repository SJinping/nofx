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
