import { useState, useEffect } from 'react';
import useSWR from 'swr';
import { api } from '../lib/api';
import { useLanguage } from '../contexts/LanguageContext';

interface LogEntry {
  filename: string;
  cycle: number;
  timestamp: string;
  size: number;
}

export function LogViewer() {
  useLanguage(); // keep context initialization (language not used in this page yet)
  const [selectedTrader, setSelectedTrader] = useState<string>('');
  const [selectedLog, setSelectedLog] = useState<LogEntry | null>(null);
  const [logContent, setLogContent] = useState<any | null>(null);

  // 获取 Trader 列表
  const { data: traders } = useSWR('log-traders', api.logViewer.getTraders);

  // 获取日志列表
  const { data: logs } = useSWR(
    selectedTrader ? `logs-${selectedTrader}` : null,
    () => api.logViewer.getLogList(selectedTrader)
  );

  // 自动选择第一个 Trader
  useEffect(() => {
    if (traders && traders.length > 0 && !selectedTrader) {
      setSelectedTrader(traders[0]);
    }
  }, [traders, selectedTrader]);

  // 加载日志详情
  useEffect(() => {
    async function fetchLog() {
      if (selectedTrader && selectedLog) {
        try {
          const content = await api.logViewer.getLogDetail(selectedTrader, selectedLog.filename);
          setLogContent(content);
        } catch (err) {
          console.error(err);
        }
      } else {
        setLogContent(null);
      }
    }
    fetchLog();
  }, [selectedTrader, selectedLog]);

  return (
    <div className="flex h-[calc(100vh-100px)] gap-6">
      {/* 左侧边栏 */}
      <div className="w-80 flex flex-col gap-4 shrink-0">
        {/* Trader 选择 */}
        <div className="binance-card p-4">
          <label className="block text-xs text-gray-500 mb-2 uppercase tracking-wider font-bold">
            Select Trader
          </label>
          <select
            value={selectedTrader}
            onChange={(e) => {
              setSelectedTrader(e.target.value);
              setSelectedLog(null);
            }}
            className="w-full rounded px-3 py-2 text-sm font-medium cursor-pointer transition-colors"
            style={{ background: '#0B0E11', border: '1px solid #2B3139', color: '#EAECEF' }}
          >
            {traders?.map((trader: string) => (
              <option key={trader} value={trader}>
                {trader}
              </option>
            ))}
          </select>
        </div>

        {/* 日志列表 */}
        <div className="binance-card flex-1 overflow-hidden flex flex-col">
          <div className="p-4 border-b border-gray-800">
            <h3 className="text-sm font-bold text-gray-300">History Logs</h3>
          </div>
          <div className="overflow-y-auto p-2 space-y-1 flex-1">
            {logs?.map((log: LogEntry) => (
              <button
                key={log.filename}
                onClick={() => setSelectedLog(log)}
                className={`w-full text-left px-3 py-2 rounded text-sm transition-all ${
                  selectedLog?.filename === log.filename
                    ? 'bg-blue-500/20 text-blue-400 border border-blue-500/30'
                    : 'hover:bg-gray-800 text-gray-400 border border-transparent'
                }`}
              >
                <div className="flex justify-between items-center">
                  <span className="font-mono font-bold">Cycle #{log.cycle}</span>
                  <span className="text-xs opacity-70">
                    {new Date(log.timestamp).toLocaleTimeString()}
                  </span>
                </div>
                <div className="text-xs mt-1 opacity-50 truncate">{log.filename}</div>
              </button>
            ))}
          </div>
        </div>
      </div>

      {/* 右侧详情 */}
      <div className="flex-1 binance-card overflow-hidden flex flex-col">
        {logContent ? (
          <div className="flex-1 overflow-y-auto p-6 space-y-8">
            {/* Header Info */}
            <div className="flex justify-between items-start border-b border-gray-800 pb-4">
              <div>
                <h2 className="text-2xl font-bold text-gray-100">
                  Decision Details
                  <span className="ml-3 text-lg font-mono text-gray-500">Cycle #{logContent.cycle_number}</span>
                </h2>
                <div className="mt-1 text-sm text-gray-500">
                  Timestamp: {new Date(logContent.timestamp).toLocaleString()}
                </div>
              </div>
              {logContent.account_state && (
                <div className="text-right text-sm font-mono text-gray-400">
                  <div>Equity: {logContent.account_state.total_balance?.toFixed(2)} USDT</div>
                  <div>Positions: {logContent.account_state.position_count}</div>
                </div>
              )}
            </div>

            {/* Decisions Table */}
            {logContent.decisions && logContent.decisions.length > 0 && (
              <div className="space-y-3">
                <h3 className="text-lg font-bold text-gray-300 flex items-center gap-2">
                  <span>📊</span> Decisions
                </h3>
                <div className="grid gap-3">
                  {logContent.decisions.map((d: any, i: number) => (
                    <div key={i} className="bg-gray-900/50 rounded p-4 border border-gray-800">
                      <div className="flex items-center gap-3 mb-2">
                        <span className={`text-xs font-bold px-2 py-1 rounded ${
                          d.action.includes('open') ? 'bg-blue-500/20 text-blue-400' : 'bg-orange-500/20 text-orange-400'
                        }`}>
                          {d.action.toUpperCase()}
                        </span>
                        <span className="font-mono font-bold text-gray-200">{d.symbol}</span>
                        {d.price > 0 && <span className="text-sm text-gray-500">@ {d.price}</span>}
                      </div>
                      <p className="text-sm text-gray-400 italic">"{d.reasoning || d.error}"</p>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* CoT / Reasoning */}
            <div className="space-y-3">
              <h3 className="text-lg font-bold text-gray-300 flex items-center gap-2">
                <span>🧠</span> Reasoning Chain
              </h3>
              <div className="bg-gray-900 rounded p-5 border border-gray-800 font-mono text-sm text-gray-300 whitespace-pre-wrap leading-relaxed">
                {logContent.cot_trace || "No reasoning trace available."}
              </div>
            </div>

            {/* Input Prompt (Collapsed) */}
            <details className="group bg-gray-900 rounded border border-gray-800">
              <summary className="p-4 font-bold text-gray-400 cursor-pointer hover:text-gray-200 select-none flex items-center gap-2">
                <span>📝</span> Raw Input Prompt
              </summary>
              <div className="p-4 border-t border-gray-800 font-mono text-xs text-gray-500 whitespace-pre-wrap overflow-x-auto">
                {logContent.input_prompt}
              </div>
            </details>

             {/* Raw JSON (Collapsed) */}
             <details className="group bg-gray-900 rounded border border-gray-800">
              <summary className="p-4 font-bold text-gray-400 cursor-pointer hover:text-gray-200 select-none flex items-center gap-2">
                <span>🔍</span> Raw JSON Source
              </summary>
              <div className="p-4 border-t border-gray-800 font-mono text-xs text-gray-500 whitespace-pre-wrap overflow-x-auto">
                {JSON.stringify(logContent, null, 2)}
              </div>
            </details>
          </div>
        ) : (
          <div className="flex-1 flex flex-col items-center justify-center text-gray-600">
            <div className="text-6xl mb-4 opacity-20">📄</div>
            <div className="text-lg">Select a log to view details</div>
          </div>
        )}
      </div>
    </div>
  );
}

