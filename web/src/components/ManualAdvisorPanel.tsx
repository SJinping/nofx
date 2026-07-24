import { useEffect, useState } from 'react';
import { api } from '../lib/api';
import type {
  AdvisorAnalyzeResponse,
  AdvisorIntent,
  ManagementCandidatesResponse,
  TraderInfo,
} from '../types';

interface ManualAdvisorPanelProps {
  selectedTrader: TraderInfo;
}

// ManualAdvisorPanel is intentionally a scaffold rather than a trading widget.
// It proves the UI/API contract for:
//   1) human-entered symbol/question,
//   2) deterministic management_trader_id assignment,
//   3) backend context/prompt preview,
// while explicitly avoiding direct order placement. Future work should add a
// "Fill Manual Order" action after backend validation returns real SL/TP/RR.
export function ManualAdvisorPanel({ selectedTrader }: ManualAdvisorPanelProps) {
  const [symbol, setSymbol] = useState('');
  const [question, setQuestion] = useState('');
  const [intent, setIntent] = useState<AdvisorIntent>('analyze_symbol');
  const [candidates, setCandidates] = useState<ManagementCandidatesResponse | null>(null);
  const [managementTraderId, setManagementTraderId] = useState('');
  const [loadingCandidates, setLoadingCandidates] = useState(false);
  const [analyzing, setAnalyzing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<AdvisorAnalyzeResponse | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoadingCandidates(true);
    api.getAdvisorManagementCandidates()
      .then((resp) => {
        if (cancelled) return;
        setCandidates(resp);
        if (resp.default_trader_id) {
          setManagementTraderId(resp.default_trader_id);
        }
      })
      .catch((e: any) => {
        if (!cancelled) setError(e?.message || String(e));
      })
      .finally(() => {
        if (!cancelled) setLoadingCandidates(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const selectedManager = candidates?.candidates.find((c) => c.trader_id === managementTraderId);
  const requiresChoice = Boolean(candidates?.requires_choice && !managementTraderId);

  const runAdvisor = async () => {
    setError(null);
    setResult(null);

    if (!symbol.trim()) {
      setError('请输入交易对，例如 ETHUSDT');
      return;
    }
    if (!question.trim()) {
      setError('请输入你想问 LLM 的交易问题');
      return;
    }
    if (requiresChoice) {
      setError('当前有多个可管理策略，请先选择开仓后的管理 trader');
      return;
    }

    setAnalyzing(true);
    try {
      const resp = await api.analyzeWithAdvisor({
        advisor_trader_id: selectedTrader.trader_id,
        management_trader_id: managementTraderId || undefined,
        symbol: symbol.trim().toUpperCase(),
        question: question.trim(),
        intent,
        horizon: 'short_term',
      });
      setResult(resp);
    } catch (e: any) {
      setError(e?.message || String(e));
    } finally {
      setAnalyzing(false);
    }
  };

  return (
    <div className="mb-6 rounded-lg p-4 animate-slide-in" style={{ background: '#1E2329', border: '1px solid #2B3139' }}>
      <div className="flex items-start justify-between gap-4 mb-4">
        <div>
          <h2 className="text-lg font-bold flex items-center gap-2" style={{ color: '#EAECEF' }}>
            🧭 Manual LLM Advisor
          </h2>
          <p className="text-xs mt-1" style={{ color: '#848E9C' }}>
            人工开仓前咨询；当前分支只搭框架/上下文/prompt preview，不会自动下单。
          </p>
        </div>
        <div className="text-xs px-2 py-1 rounded" style={{ background: 'rgba(240,185,11,0.10)', color: '#F0B90B', border: '1px solid rgba(240,185,11,0.2)' }}>
          scaffold
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-4 gap-3 mb-3">
        <input
          value={symbol}
          onChange={(e) => setSymbol(e.target.value.toUpperCase())}
          placeholder="ETHUSDT"
          className="rounded px-3 py-2 text-sm outline-none"
          style={{ background: '#0B0E11', border: '1px solid #2B3139', color: '#EAECEF' }}
        />
        <select
          value={intent}
          onChange={(e) => setIntent(e.target.value as AdvisorIntent)}
          className="rounded px-3 py-2 text-sm outline-none"
          style={{ background: '#0B0E11', border: '1px solid #2B3139', color: '#EAECEF' }}
        >
          <option value="analyze_symbol">让 LLM 判断方向</option>
          <option value="evaluate_long">评估做多想法</option>
          <option value="evaluate_short">评估做空想法</option>
          <option value="validate_plan">校验已有计划</option>
        </select>
        <select
          value={managementTraderId}
          onChange={(e) => setManagementTraderId(e.target.value)}
          disabled={loadingCandidates || !candidates || candidates.candidates.length === 0}
          className="rounded px-3 py-2 text-sm outline-none disabled:opacity-60"
          style={{ background: '#0B0E11', border: '1px solid #2B3139', color: '#EAECEF' }}
        >
          <option value="">{loadingCandidates ? '加载管理策略...' : '不开启自动管理'}</option>
          {candidates?.candidates.map((c) => (
            <option key={c.trader_id} value={c.trader_id} disabled={!c.can_manage_positions}>
              {c.trader_name} ({c.ai_model}){c.can_manage_positions ? '' : ' - unavailable'}
            </option>
          ))}
        </select>
        <button
          onClick={runAdvisor}
          disabled={analyzing}
          className="rounded px-4 py-2 text-sm font-bold transition-all hover:scale-[1.01] disabled:opacity-60"
          style={{ background: '#F0B90B', color: '#000' }}
        >
          {analyzing ? 'Analyzing...' : 'Ask LLM'}
        </button>
      </div>

      <textarea
        value={question}
        onChange={(e) => setQuestion(e.target.value)}
        placeholder="例如：我想做空 ETH，帮我评估现在是否合适，并给出止损/止盈建议。"
        rows={3}
        className="w-full rounded px-3 py-2 text-sm outline-none resize-y"
        style={{ background: '#0B0E11', border: '1px solid #2B3139', color: '#EAECEF' }}
      />

      <div className="mt-3 text-xs" style={{ color: '#848E9C' }}>
        Advisor trader: <span style={{ color: '#EAECEF' }}>{selectedTrader.trader_name}</span>
        {' '}| Management: <span style={{ color: selectedManager ? '#0ECB81' : '#F0B90B' }}>
          {selectedManager ? `${selectedManager.trader_name} (${selectedManager.trader_id})` : 'none / user must choose if multiple'}
        </span>
        {candidates?.reason && <span> | {candidates.reason}</span>}
      </div>

      {error && (
        <div className="mt-3 rounded p-3 text-sm" style={{ background: 'rgba(246,70,93,0.1)', color: '#F6465D', border: '1px solid rgba(246,70,93,0.2)' }}>
          {error}
        </div>
      )}

      {result && (
        <div className="mt-4 rounded p-3 text-sm" style={{ background: '#0B0E11', border: '1px solid #2B3139' }}>
          <div className="font-semibold mb-2" style={{ color: '#EAECEF' }}>
            {result.status}: {result.message}
          </div>
          <div className="grid grid-cols-1 lg:grid-cols-3 gap-3 text-xs" style={{ color: '#848E9C' }}>
            <div>Advisor ID: <span style={{ color: '#EAECEF' }}>{result.advisor_id}</span></div>
            <div>Symbol: <span style={{ color: '#EAECEF' }}>{result.symbol}</span></div>
            <div>Manager: <span style={{ color: '#EAECEF' }}>{result.management_trader_id || 'none'}</span></div>
          </div>
          <details className="mt-3">
            <summary className="cursor-pointer text-xs" style={{ color: '#F0B90B' }}>查看 prompt preview / TODO</summary>
            <pre className="mt-2 overflow-auto text-xs whitespace-pre-wrap" style={{ color: '#A7B1BC', maxHeight: 360 }}>
{JSON.stringify({ recommendation: result.recommendation, next_todo: result.next_todo, prompt_preview: result.prompt_preview }, null, 2)}
            </pre>
          </details>
        </div>
      )}
    </div>
  );
}
