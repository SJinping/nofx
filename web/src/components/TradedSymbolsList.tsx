import { useState } from 'react';
import useSWR from 'swr';
import { api } from '../lib/api';
import type { ExchangeOrderStats, ExchangeOrdersResponse, TradedSymbol, TradedSymbolsResponse } from '../types';
import { useLanguage } from '../contexts/LanguageContext';
import type React from 'react';

// 翻译映射
const translations: Record<string, Record<string, string>> = {
  zh: {
    tradedSymbols: '交易币种',
    all: '全部',
    holding: '持仓中',
    closed: '已平仓',
    noTrades: '暂无交易记录',
    noTradesDesc: 'AI尚未执行任何交易',
    totalPnL: '总盈亏',
    realizedPnL: '已实现',
    unrealizedPnL: '未实现',
    winRate: '胜率',
    trades: '笔交易',
    holdingLong: '持有多',
    holdingShort: '持有空',
    closedStatus: '已平仓',
    openLong: '开多',
    openShort: '开空',
    closeLong: '平多',
    closeShort: '平空',
    partialClose: '部分平',
    avgPnL: '平均盈亏',
    firstTrade: '首次交易',
    lastTrade: '最后交易',
    summary: '汇总',
    symbols: '币种',
    loading: '加载中...',
  },
  en: {
    tradedSymbols: 'Traded Symbols',
    all: 'All',
    holding: 'Holding',
    closed: 'Closed',
    noTrades: 'No Trades Yet',
    noTradesDesc: 'AI has not executed any trades yet',
    totalPnL: 'Total P&L',
    realizedPnL: 'Realized',
    unrealizedPnL: 'Unrealized',
    winRate: 'Win Rate',
    trades: 'trades',
    holdingLong: 'Long',
    holdingShort: 'Short',
    closedStatus: 'Closed',
    openLong: 'Open Long',
    openShort: 'Open Short',
    closeLong: 'Close Long',
    closeShort: 'Close Short',
    partialClose: 'Partial',
    avgPnL: 'Avg P&L',
    firstTrade: 'First Trade',
    lastTrade: 'Last Trade',
    summary: 'Summary',
    symbols: 'Symbols',
    loading: 'Loading...',
  },
};

type FilterType = 'all' | 'holding' | 'closed';

interface TradedSymbolsListProps {
  traderId: string;
  onSymbolClick?: (symbol: string) => void;
}

export function TradedSymbolsList({ traderId, onSymbolClick }: TradedSymbolsListProps) {
  const { language } = useLanguage();
  const t = (key: string) => translations[language]?.[key] || key;
  
  const [filter, setFilter] = useState<FilterType>('all');
  const [expandedSymbol, setExpandedSymbol] = useState<string | null>(null);

  const { data, error, isLoading } = useSWR<TradedSymbolsResponse>(
    traderId ? `traded-symbols-${traderId}` : null,
    () => api.getTradedSymbols(traderId),
    { refreshInterval: 10000 }
  );

  // 订单汇总（按 symbol）：用于覆盖 realized_pnl / trades / win_rate 等统计
  const baseSymbols = data?.symbols || [];
  const baseSummary = data?.summary;
  const symbolList = baseSymbols.map((s) => s.symbol);

  const { data: exchangeAgg } = useSWR(
    traderId && symbolList.length > 0
      ? `exchange-traded-symbols-${traderId}-${symbolList.join(',')}`
      : null,
    () => api.getExchangeTradedSymbols(traderId, symbolList),
    { refreshInterval: 30000 }
  );

  const exchangeMap = new Map<string, ExchangeOrderStats>();
  (exchangeAgg?.symbols || []).forEach((st: ExchangeOrderStats) => {
    exchangeMap.set(st.symbol, st);
  });

  // 当前展开币种：订单明细
  const { data: exchangeOrders, isLoading: ordersLoading, error: ordersError } = useSWR<ExchangeOrdersResponse>(
    traderId && expandedSymbol ? `exchange-orders-${traderId}-${expandedSymbol}` : null,
    () => api.getExchangeOrders(traderId, expandedSymbol as string),
    { refreshInterval: 30000 }
  );

  if (isLoading) {
    return (
      <div className="binance-card p-6 animate-pulse">
        <div className="skeleton h-6 w-32 mb-4"></div>
        <div className="space-y-3">
          {[1, 2, 3].map((i) => (
            <div key={i} className="skeleton h-16 w-full"></div>
          ))}
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="binance-card p-6">
        <div className="text-center py-8" style={{ color: '#F6465D' }}>
          ❌ {t('loading')} Error: {error.message}
        </div>
      </div>
    );
  }

  // 合并：用交易所订单汇总覆盖部分字段（不动 unrealized_pnl / current_position）
  const symbols = baseSymbols.map((s) => {
    const ex = exchangeMap.get(s.symbol);
    if (!ex) return s;
    return {
      ...s,
      realized_pnl: ex.realized_pnl,
      avg_pnl: ex.avg_pnl,
      total_trades: ex.trades,
      win_rate: ex.win_rate,
      win_count: ex.win_count,
      loss_count: ex.loss_count,
      // total_pnl 重新计算（realized from exchange + unrealized from positions）
      total_pnl: ex.realized_pnl + (s.unrealized_pnl || 0),
      first_trade_time: ex.first_trade_time || s.first_trade_time,
      last_trade_time: ex.last_trade_time || s.last_trade_time,
      // attach exchange stats for expanded view
      exchange: ex,
    } as TradedSymbol & { exchange?: ExchangeOrderStats };
  });

  const summary = {
    ...(baseSummary || {
      total_symbols: 0,
      holding_count: 0,
      closed_count: 0,
      total_realized_pnl: 0,
      total_unrealized_pnl: 0,
    }),
    // 优先使用交易所订单聚合的 realized（best-effort）
    total_realized_pnl: exchangeAgg?.summary?.total_realized_pnl ?? (baseSummary?.total_realized_pnl ?? 0),
  };

  // 应用过滤
  const filteredSymbols = symbols.filter((s) => {
    if (filter === 'all') return true;
    if (filter === 'holding') return s.status.startsWith('holding');
    if (filter === 'closed') return s.status === 'closed';
    return true;
  });

  const getStatusStyle = (status: string) => {
    if (status === 'holding_long') {
      return { background: 'rgba(14, 203, 129, 0.1)', color: '#0ECB81', border: '1px solid rgba(14, 203, 129, 0.2)' };
    }
    if (status === 'holding_short') {
      return { background: 'rgba(246, 70, 93, 0.1)', color: '#F6465D', border: '1px solid rgba(246, 70, 93, 0.2)' };
    }
    return { background: 'rgba(132, 142, 156, 0.1)', color: '#848E9C', border: '1px solid rgba(132, 142, 156, 0.2)' };
  };

  const getStatusLabel = (status: string) => {
    if (status === 'holding_long') return t('holdingLong');
    if (status === 'holding_short') return t('holdingShort');
    return t('closedStatus');
  };

  const formatPnL = (value: number) => {
    const formatted = value >= 0 ? `+${value.toFixed(2)}` : value.toFixed(2);
    return formatted;
  };

  const formatTime = (timeStr: string) => {
    if (!timeStr) return '-';
    const date = new Date(timeStr);
    return date.toLocaleDateString() + ' ' + date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  };

  return (
    <div className="binance-card p-6">
      {/* 标题和过滤器 */}
      <div className="flex items-center justify-between mb-5">
        <h2 className="text-xl font-bold flex items-center gap-2" style={{ color: '#EAECEF' }}>
          📊 {t('tradedSymbols')}
        </h2>
        <div className="flex gap-1 rounded p-1" style={{ background: '#1E2329' }}>
          {(['all', 'holding', 'closed'] as FilterType[]).map((f) => (
            <button
              key={f}
              onClick={() => setFilter(f)}
              className="px-3 py-1.5 rounded text-xs font-semibold transition-all"
              style={filter === f
                ? { background: '#F0B90B', color: '#000' }
                : { background: 'transparent', color: '#848E9C' }
              }
            >
              {t(f)}
              {f === 'holding' && summary && ` (${summary.holding_count})`}
              {f === 'closed' && summary && ` (${summary.closed_count})`}
              {f === 'all' && summary && ` (${summary.total_symbols})`}
            </button>
          ))}
        </div>
      </div>

      {/* 汇总信息 */}
      {summary && summary.total_symbols > 0 && (
        <div className="grid grid-cols-4 gap-3 mb-5 p-4 rounded" style={{ background: '#1E2329' }}>
          <div className="text-center">
            <div className="text-xs mb-1" style={{ color: '#848E9C' }}>{t('symbols')}</div>
            <div className="text-lg font-bold" style={{ color: '#EAECEF' }}>{summary.total_symbols}</div>
          </div>
          <div className="text-center">
            <div className="text-xs mb-1" style={{ color: '#848E9C' }}>{t('holding')}</div>
            <div className="text-lg font-bold" style={{ color: '#F0B90B' }}>{summary.holding_count}</div>
          </div>
          <div className="text-center">
            <div className="text-xs mb-1" style={{ color: '#848E9C' }}>{t('realizedPnL')}</div>
            <div className="text-lg font-bold" style={{ color: summary.total_realized_pnl >= 0 ? '#0ECB81' : '#F6465D' }}>
              {formatPnL(summary.total_realized_pnl)}
            </div>
          </div>
          <div className="text-center">
            <div className="text-xs mb-1" style={{ color: '#848E9C' }}>{t('unrealizedPnL')}</div>
            <div className="text-lg font-bold" style={{ color: summary.total_unrealized_pnl >= 0 ? '#0ECB81' : '#F6465D' }}>
              {formatPnL(summary.total_unrealized_pnl)}
            </div>
          </div>
        </div>
      )}

      {/* 币种列表 */}
      {filteredSymbols.length > 0 ? (
        <div className="space-y-2">
          {filteredSymbols.map((symbol) => (
            <SymbolCard
              key={symbol.symbol}
              symbol={symbol as TradedSymbol & { exchange?: ExchangeOrderStats }}
              t={t}
              getStatusStyle={getStatusStyle}
              getStatusLabel={getStatusLabel}
              formatPnL={formatPnL}
              formatTime={formatTime}
              isExpanded={expandedSymbol === symbol.symbol}
              onToggleExpand={() => setExpandedSymbol(expandedSymbol === symbol.symbol ? null : symbol.symbol)}
              onClick={() => onSymbolClick?.(symbol.symbol)}
              exchangeOrders={expandedSymbol === symbol.symbol ? exchangeOrders : undefined}
              ordersLoading={expandedSymbol === symbol.symbol ? ordersLoading : false}
              ordersError={expandedSymbol === symbol.symbol ? (ordersError as any) : undefined}
            />
          ))}
        </div>
      ) : (
        <div className="text-center py-12" style={{ color: '#848E9C' }}>
          <div className="text-5xl mb-4 opacity-50">📊</div>
          <div className="text-lg font-semibold mb-2">{t('noTrades')}</div>
          <div className="text-sm">{t('noTradesDesc')}</div>
        </div>
      )}
    </div>
  );
}

// 单个币种卡片组件
interface SymbolCardProps {
  symbol: TradedSymbol & { exchange?: ExchangeOrderStats };
  t: (key: string) => string;
  getStatusStyle: (status: string) => React.CSSProperties;
  getStatusLabel: (status: string) => string;
  formatPnL: (value: number) => string;
  formatTime: (time: string) => string;
  isExpanded: boolean;
  onToggleExpand: () => void;
  onClick?: () => void;
  exchangeOrders?: ExchangeOrdersResponse;
  ordersLoading?: boolean;
  ordersError?: any;
}

function SymbolCard({
  symbol,
  t,
  getStatusStyle,
  getStatusLabel,
  formatPnL,
  formatTime,
  isExpanded,
  onToggleExpand,
  onClick,
  exchangeOrders,
  ordersLoading,
  ordersError,
}: SymbolCardProps) {
  return (
    <div
      className="rounded p-4 transition-all duration-200 hover:translate-y-[-1px] cursor-pointer"
      style={{ 
        background: '#1E2329', 
        border: '1px solid #2B3139',
        boxShadow: isExpanded ? '0 4px 12px rgba(0, 0, 0, 0.3)' : 'none'
      }}
      onClick={onToggleExpand}
    >
      {/* 主要信息行 */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          {/* 币种名称 */}
          <div className="font-bold text-lg" style={{ color: '#EAECEF' }}>
            {symbol.symbol.replace('USDT', '')}
            <span className="text-xs font-normal ml-1" style={{ color: '#848E9C' }}>USDT</span>
          </div>
          {/* 状态标签 */}
          <span
            className="px-2 py-1 rounded text-xs font-bold"
            style={getStatusStyle(symbol.status)}
          >
            {getStatusLabel(symbol.status)}
          </span>
        </div>

        <div className="flex items-center gap-6">
          {/* 总盈亏 */}
          <div className="text-right">
            <div className="text-xs mb-0.5" style={{ color: '#848E9C' }}>{t('totalPnL')}</div>
            <div 
              className="font-bold font-mono"
              style={{ color: symbol.total_pnl >= 0 ? '#0ECB81' : '#F6465D' }}
            >
              {formatPnL(symbol.total_pnl)} USDT
            </div>
          </div>

          {/* 胜率 */}
          <div className="text-right">
            <div className="text-xs mb-0.5" style={{ color: '#848E9C' }}>{t('winRate')}</div>
            <div className="font-bold font-mono" style={{ color: '#EAECEF' }}>
              {symbol.win_rate.toFixed(1)}%
            </div>
          </div>

          {/* 交易次数 */}
          <div className="text-right">
            <div className="text-xs mb-0.5" style={{ color: '#848E9C' }}>{t('trades')}</div>
            <div className="font-mono" style={{ color: '#EAECEF' }}>
              {symbol.total_trades}
            </div>
          </div>

          {/* 展开箭头 */}
          <div 
            className="transition-transform duration-200"
            style={{ 
              color: '#848E9C',
              transform: isExpanded ? 'rotate(180deg)' : 'rotate(0deg)'
            }}
          >
            ▼
          </div>
        </div>
      </div>

      {/* 展开的详细信息 */}
      {isExpanded && (
        <div className="mt-4 pt-4" style={{ borderTop: '1px solid #2B3139' }}>
          {/* 盈亏明细 */}
          <div className="grid grid-cols-3 gap-4 mb-4">
            <div className="rounded p-3" style={{ background: '#0B0E11' }}>
              <div className="text-xs mb-1" style={{ color: '#848E9C' }}>{t('realizedPnL')}</div>
              <div 
                className="font-bold font-mono"
                style={{ color: symbol.realized_pnl >= 0 ? '#0ECB81' : '#F6465D' }}
              >
                {formatPnL(symbol.realized_pnl)} USDT
              </div>
            </div>
            <div className="rounded p-3" style={{ background: '#0B0E11' }}>
              <div className="text-xs mb-1" style={{ color: '#848E9C' }}>{t('unrealizedPnL')}</div>
              <div 
                className="font-bold font-mono"
                style={{ color: symbol.unrealized_pnl >= 0 ? '#0ECB81' : '#F6465D' }}
              >
                {formatPnL(symbol.unrealized_pnl)} USDT
              </div>
            </div>
            <div className="rounded p-3" style={{ background: '#0B0E11' }}>
              <div className="text-xs mb-1" style={{ color: '#848E9C' }}>{t('avgPnL')}</div>
              <div 
                className="font-bold font-mono"
                style={{ color: symbol.avg_pnl >= 0 ? '#0ECB81' : '#F6465D' }}
              >
                {formatPnL(symbol.avg_pnl)} USDT
              </div>
            </div>
          </div>

          {/* 交易统计 */}
          <div className="flex gap-2 flex-wrap mb-4">
            {symbol.open_long_count > 0 && (
              <span className="px-2 py-1 rounded text-xs" style={{ background: 'rgba(14, 203, 129, 0.1)', color: '#0ECB81' }}>
                {t('openLong')}: {symbol.open_long_count}
              </span>
            )}
            {symbol.open_short_count > 0 && (
              <span className="px-2 py-1 rounded text-xs" style={{ background: 'rgba(246, 70, 93, 0.1)', color: '#F6465D' }}>
                {t('openShort')}: {symbol.open_short_count}
              </span>
            )}
            {symbol.close_long_count > 0 && (
              <span className="px-2 py-1 rounded text-xs" style={{ background: 'rgba(240, 185, 11, 0.1)', color: '#F0B90B' }}>
                {t('closeLong')}: {symbol.close_long_count}
              </span>
            )}
            {symbol.close_short_count > 0 && (
              <span className="px-2 py-1 rounded text-xs" style={{ background: 'rgba(240, 185, 11, 0.1)', color: '#F0B90B' }}>
                {t('closeShort')}: {symbol.close_short_count}
              </span>
            )}
            {symbol.partial_close_count > 0 && (
              <span className="px-2 py-1 rounded text-xs" style={{ background: 'rgba(132, 142, 156, 0.1)', color: '#848E9C' }}>
                {t('partialClose')}: {symbol.partial_close_count}
              </span>
            )}
            <span className="px-2 py-1 rounded text-xs" style={{ background: 'rgba(96, 165, 250, 0.1)', color: '#60a5fa' }}>
              Win: {symbol.win_count} / Loss: {symbol.loss_count}
            </span>
          </div>

          {/* 当前持仓信息 */}
          {symbol.current_position && (
            <div className="rounded p-3 mb-4" style={{ background: 'rgba(240, 185, 11, 0.05)', border: '1px solid rgba(240, 185, 11, 0.2)' }}>
              <div className="text-xs font-bold mb-2" style={{ color: '#F0B90B' }}>📍 当前持仓</div>
              <div className="grid grid-cols-4 gap-3 text-xs">
                <div>
                  <span style={{ color: '#848E9C' }}>入场价: </span>
                  <span className="font-mono" style={{ color: '#EAECEF' }}>{symbol.current_position.entry_price.toFixed(4)}</span>
                </div>
                <div>
                  <span style={{ color: '#848E9C' }}>标记价: </span>
                  <span className="font-mono" style={{ color: '#EAECEF' }}>{symbol.current_position.mark_price.toFixed(4)}</span>
                </div>
                <div>
                  <span style={{ color: '#848E9C' }}>数量: </span>
                  <span className="font-mono" style={{ color: '#EAECEF' }}>{symbol.current_position.quantity.toFixed(4)}</span>
                </div>
                <div>
                  <span style={{ color: '#848E9C' }}>杠杆: </span>
                  <span className="font-mono" style={{ color: '#F0B90B' }}>{symbol.current_position.leverage}x</span>
                </div>
              </div>
            </div>
          )}

          {/* 时间信息 */}
          <div className="flex justify-between text-xs" style={{ color: '#848E9C' }}>
            <span>{t('firstTrade')}: {formatTime(symbol.first_trade_time)}</span>
            <span>{t('lastTrade')}: {formatTime(symbol.last_trade_time)}</span>
          </div>

          {/* 交易所订单统计（best-effort） */}
          {symbol.exchange && (
            <div className="mt-4 rounded p-3" style={{ background: '#0B0E11', border: '1px solid #2B3139' }}>
              <div className="text-xs font-bold mb-2" style={{ color: '#EAECEF' }}>📑 交易所订单统计</div>
              <div className="grid grid-cols-4 gap-3 text-xs" style={{ color: '#848E9C' }}>
                <div>
                  订单数: <span className="font-mono" style={{ color: '#EAECEF' }}>{symbol.exchange.total_orders}</span>
                </div>
                <div>
                  成交订单: <span className="font-mono" style={{ color: '#EAECEF' }}>{symbol.exchange.filled_orders}</span>
                </div>
                <div>
                  {symbol.exchange.real_commission !== 0 ? '手续费' : '估算手续费'}:{' '}
                  <span className="font-mono" style={{ color: '#EAECEF' }}>
                    {symbol.exchange.real_commission !== 0
                      ? `${formatPnL(symbol.exchange.real_commission)} USDT`
                      : `${formatPnL(-symbol.exchange.estimated_fee).replace('+', '')} USDT`
                    }
                  </span>
                </div>
                <div>
                  平均持仓: <span className="font-mono" style={{ color: '#EAECEF' }}>{(symbol.exchange.avg_hold_seconds / 60).toFixed(1)} min</span>
                </div>
              </div>
              {/* 资金费用（仅在有数据时显示） */}
              {symbol.exchange.real_funding_fee !== 0 && (
                <div className="mt-2 grid grid-cols-4 gap-3 text-xs" style={{ color: '#848E9C' }}>
                  <div>
                    资金费用:{' '}
                    <span className="font-mono" style={{ color: symbol.exchange.real_funding_fee >= 0 ? '#0ECB81' : '#F6465D' }}>
                      {formatPnL(symbol.exchange.real_funding_fee)} USDT
                    </span>
                  </div>
                  <div>
                    交易所已实现:{' '}
                    <span className="font-mono" style={{ color: symbol.exchange.real_realized_pnl >= 0 ? '#0ECB81' : '#F6465D' }}>
                      {formatPnL(symbol.exchange.real_realized_pnl)} USDT
                    </span>
                  </div>
                  <div className="col-span-2">
                    净盈亏:{' '}
                    <span className="font-mono" style={{ color: (symbol.exchange.real_realized_pnl + symbol.exchange.real_funding_fee + symbol.exchange.real_commission) >= 0 ? '#0ECB81' : '#F6465D' }}>
                      {formatPnL(symbol.exchange.real_realized_pnl + symbol.exchange.real_funding_fee + symbol.exchange.real_commission)} USDT
                    </span>
                  </div>
                </div>
              )}
            </div>
          )}

          {/* 订单明细 */}
          <div className="mt-4">
            <div className="text-xs font-bold mb-2" style={{ color: '#EAECEF' }}>🧾 订单明细</div>
            {ordersLoading ? (
              <div className="text-xs" style={{ color: '#848E9C' }}>加载中...</div>
            ) : ordersError ? (
              <div className="text-xs" style={{ color: '#F6465D' }}>获取订单失败</div>
            ) : exchangeOrders?.orders?.length ? (
              <div className="overflow-x-auto rounded" style={{ border: '1px solid #2B3139' }}>
                <table className="w-full text-xs" style={{ background: '#0B0E11' }}>
                  <thead style={{ color: '#848E9C' }}>
                    <tr>
                      <th className="text-left p-2">Time</th>
                      <th className="text-left p-2">Side</th>
                      <th className="text-left p-2">Pos</th>
                      <th className="text-left p-2">Type</th>
                      <th className="text-right p-2">Avg</th>
                      <th className="text-right p-2">Qty</th>
                      <th className="text-left p-2">Status</th>
                    </tr>
                  </thead>
                  <tbody style={{ color: '#EAECEF' }}>
                    {exchangeOrders.orders.slice(0, 20).map((o) => (
                      <tr key={o.order_id} style={{ borderTop: '1px solid #1E2329' }}>
                        <td className="p-2 whitespace-nowrap">{formatTime(o.created_at)}</td>
                        <td className="p-2" style={{ color: o.side === 'BUY' ? '#0ECB81' : '#F6465D' }}>{o.side}</td>
                        <td className="p-2">{o.position_side}</td>
                        <td className="p-2">{o.type}{o.reduce_only ? ' RO' : ''}</td>
                        <td className="p-2 text-right font-mono">{(o.avg_price || o.price || 0).toFixed(6)}</td>
                        <td className="p-2 text-right font-mono">{(o.executed_qty || 0).toFixed(4)}</td>
                        <td className="p-2">{o.status}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                {exchangeOrders.orders.length > 20 && (
                  <div className="p-2 text-xs" style={{ color: '#848E9C' }}>
                    仅展示最近 20 条（可按需扩展分页/更多）
                  </div>
                )}
              </div>
            ) : (
              <div className="text-xs" style={{ color: '#848E9C' }}>暂无订单</div>
            )}
          </div>

          {/* 查看详情按钮 */}
          {onClick && (
            <button
              onClick={(e) => {
                e.stopPropagation();
                onClick();
              }}
              className="mt-4 w-full py-2 rounded text-sm font-bold transition-all hover:scale-[1.02]"
              style={{ 
                background: 'linear-gradient(135deg, #F0B90B 0%, #FCD535 100%)', 
                color: '#000',
                boxShadow: '0 2px 8px rgba(240, 185, 11, 0.3)'
              }}
            >
              查看价格曲线 →
            </button>
          )}
        </div>
      )}
    </div>
  );
}

export default TradedSymbolsList;

