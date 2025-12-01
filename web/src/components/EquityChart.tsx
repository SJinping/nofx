import { useState } from 'react';
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  ReferenceLine,
} from 'recharts';
import useSWR from 'swr';
import { api } from '../lib/api';
import { useLanguage } from '../contexts/LanguageContext';
import { t } from '../i18n/translations';
import type { DecisionRecord, DecisionAction } from '../types';
import { RangeSlider } from './RangeSlider';

interface EquityPoint {
  timestamp: string;
  total_equity: number;
  pnl: number;
  pnl_pct: number;
  cycle_number: number;
}

interface EquityChartProps {
  traderId?: string;
}

export function EquityChart({ traderId }: EquityChartProps) {
  const { language } = useLanguage();
  const [displayMode, setDisplayMode] = useState<'dollar' | 'percent'>('dollar');
  
  // 滑块状态 - 必须在组件顶部声明
  const [rangeStart, setRangeStart] = useState(0);
  const [rangeEnd, setRangeEnd] = useState<number | null>(null);

  const { data: history, error } = useSWR<EquityPoint[]>(
    traderId ? `equity-history-${traderId}` : 'equity-history',
    () => api.getEquityHistory(traderId),
    {
      refreshInterval: 10000, // 每10秒刷新
    }
  );

  const { data: account } = useSWR(
    traderId ? `account-${traderId}` : 'account',
    () => api.getAccount(traderId),
    {
      refreshInterval: 5000,
    }
  );

  const { data: decisions } = useSWR<DecisionRecord[]>(
    traderId ? `decisions-${traderId}` : 'decisions',
    () => api.getDecisions(traderId),
    {
      refreshInterval: 10000, // 每10秒刷新
    }
  );

  if (error) {
    return (
      <div className="binance-card p-6">
        <div className="flex items-center gap-3 p-4 rounded" style={{ background: 'rgba(246, 70, 93, 0.1)', border: '1px solid rgba(246, 70, 93, 0.2)' }}>
          <div className="text-2xl">⚠️</div>
          <div>
            <div className="font-semibold" style={{ color: '#F6465D' }}>{t('loadingError', language)}</div>
            <div className="text-sm" style={{ color: '#848E9C' }}>{error.message}</div>
          </div>
        </div>
      </div>
    );
  }

  if (!history || history.length === 0) {
    return (
      <div className="binance-card p-6">
        <h3 className="text-lg font-semibold mb-6" style={{ color: '#EAECEF' }}>{t('accountEquityCurve', language)}</h3>
        <div className="text-center py-16" style={{ color: '#848E9C' }}>
          <div className="text-6xl mb-4 opacity-50">📊</div>
          <div className="text-lg font-semibold mb-2">{t('noHistoricalData', language)}</div>
          <div className="text-sm">{t('dataWillAppear', language)}</div>
        </div>
      </div>
    );
  }

  // 限制显示最近的数据点（性能优化）
  // 如果数据超过2000个点，只显示最近2000个
  const MAX_DISPLAY_POINTS = 2000;
  const baseDisplayHistory = history.length > MAX_DISPLAY_POINTS
    ? history.slice(-MAX_DISPLAY_POINTS)
    : history;

  // 计算有效的范围值 - 不使用 useEffect
  const effectiveRangeEnd = rangeEnd === null ? baseDisplayHistory.length : rangeEnd;
  const effectiveRangeStart = rangeStart;

  // 根据滑块范围过滤数据
  const displayHistory = (effectiveRangeStart === 0 && effectiveRangeEnd === baseDisplayHistory.length)
    ? baseDisplayHistory
    : baseDisplayHistory.slice(effectiveRangeStart, Math.min(effectiveRangeEnd, baseDisplayHistory.length));

  // 计算初始余额（使用第一个数据点，如果无数据则从account获取，最后才用默认值）
  const initialBalance = history[0]?.total_equity
    || account?.total_equity
    || 100;  // 默认值改为100，与常见配置一致

  // 将决策按 cycle_number 组织
  const decisionsByCycle = new Map<number, DecisionAction[]>();
  if (decisions) {
    decisions.forEach((record: DecisionRecord) => {
      if (record.decisions && record.decisions.length > 0) {
        decisionsByCycle.set(record.cycle_number, record.decisions);
      }
    });
  }

  // 转换数据格式
  const chartData = displayHistory.map((point: EquityPoint) => {
    const pnl = point.total_equity - initialBalance;
    const pnlPct = ((pnl / initialBalance) * 100).toFixed(2);
    const cycleDecisions = decisionsByCycle.get(point.cycle_number) || [];
    
    return {
      time: new Date(point.timestamp).toLocaleTimeString('zh-CN', {
        hour: '2-digit',
        minute: '2-digit',
      }),
      value: displayMode === 'dollar' ? point.total_equity : parseFloat(pnlPct),
      cycle: point.cycle_number,
      raw_equity: point.total_equity,
      raw_pnl: pnl,
      raw_pnl_pct: parseFloat(pnlPct),
      decisions: cycleDecisions, // 添加决策信息
    };
  });

  const currentValue = chartData[chartData.length - 1];
  const isProfit = currentValue.raw_pnl >= 0;

  // 计算Y轴范围
  const calculateYDomain = () => {
    if (displayMode === 'percent') {
      // 百分比模式：找到最大最小值，留20%余量
      const values = chartData.map((d: any) => d.value);
      const minVal = Math.min(...values);
      const maxVal = Math.max(...values);
      const range = Math.max(Math.abs(maxVal), Math.abs(minVal));
      const padding = Math.max(range * 0.2, 1); // 至少留1%余量
      return [Math.floor(minVal - padding), Math.ceil(maxVal + padding)];
    } else {
      // 美元模式：以初始余额为基准，上下留10%余量
      const values = chartData.map((d: any) => d.value);
      const minVal = Math.min(...values, initialBalance);
      const maxVal = Math.max(...values, initialBalance);
      const range = maxVal - minVal;
      const padding = Math.max(range * 0.15, initialBalance * 0.01); // 至少留1%余量
      return [
        Math.floor(minVal - padding),
        Math.ceil(maxVal + padding)
      ];
    }
  };

  // 判断是否包含真实交易（open/close），忽略 hold / wait
  const hasRealTrade = (list?: DecisionAction[]) =>
    !!list?.some(
      (d) =>
        d.action === 'open_long' ||
        d.action === 'close_short' ||
        d.action === 'open_short' ||
        d.action === 'close_long'
    );

  // 自定义Tooltip - Binance Style with Trading Actions
  const CustomTooltip = ({ active, payload }: any) => {
    if (active && payload && payload.length) {
      const data = payload[0].payload;
      const allDecisions = data.decisions as DecisionAction[] | undefined;
      const hasTradingDecisions = hasRealTrade(allDecisions);
      
      // 分类决策
      const buys =
        allDecisions?.filter(
          (d: DecisionAction) =>
            d.action === 'open_long' || d.action === 'close_short'
        ) || [];
      const sells =
        allDecisions?.filter(
          (d: DecisionAction) =>
            d.action === 'open_short' || d.action === 'close_long'
        ) || [];
      
      return (
        <div 
          className="rounded p-3 shadow-xl" 
          style={{ 
            background: '#1E2329', 
            border: '1px solid #2B3139',
            maxWidth: '320px',
          }}
        >
          <div className="text-xs mb-1" style={{ color: '#848E9C' }}>
            Cycle #{data.cycle}
          </div>
          <div className="font-bold mono" style={{ color: '#EAECEF' }}>
            {data.raw_equity.toFixed(2)} USDT
          </div>
          <div
            className="text-sm mono font-bold mb-2"
            style={{ color: data.raw_pnl >= 0 ? '#0ECB81' : '#F6465D' }}
          >
            {data.raw_pnl >= 0 ? '+' : ''}
            {data.raw_pnl.toFixed(2)} USDT ({data.raw_pnl_pct >= 0 ? '+' : ''}
            {data.raw_pnl_pct}%)
          </div>
          
          {/* 显示交易操作（仅在有真实 open/close 操作时展示） */}
          {hasTradingDecisions && (
            <div 
              className="mt-2 pt-2" 
              style={{ borderTop: '1px solid #2B3139' }}
            >
              <div className="text-xs font-semibold mb-1" style={{ color: '#848E9C' }}>
                📊 交易操作
              </div>
              
              {/* 买入操作 */}
              {buys.length > 0 && (
                <div className="mb-1">
                  {buys.map((buy: DecisionAction, idx: number) => (
                    <div 
                      key={idx}
                      className="text-xs px-2 py-1 rounded mb-1"
                      style={{ 
                        background: 'rgba(14, 203, 129, 0.1)',
                        border: '1px solid rgba(14, 203, 129, 0.3)'
                      }}
                    >
                      <div className="flex items-center justify-between">
                        <span style={{ color: '#0ECB81' }} className="font-semibold">
                          🟢 {buy.action === 'open_long' ? '做多' : '平空'}
                        </span>
                        <span style={{ color: buy.success ? '#0ECB81' : '#F6465D' }}>
                          {buy.success ? '✓' : '✗'}
                        </span>
                      </div>
                      <div style={{ color: '#EAECEF' }}>
                        {buy.symbol} @ ${buy.price?.toFixed(2) || 'N/A'}
                      </div>
                      <div style={{ color: '#848E9C' }}>
                        数量: {buy.quantity?.toFixed(4) || 'N/A'} | 杠杆: {buy.leverage || 1}x
                      </div>
                    </div>
                  ))}
                </div>
              )}
              
              {/* 卖出操作 */}
              {sells.length > 0 && (
                <div>
                  {sells.map((sell: DecisionAction, idx: number) => (
                    <div 
                      key={idx}
                      className="text-xs px-2 py-1 rounded mb-1"
                      style={{ 
                        background: 'rgba(246, 70, 93, 0.1)',
                        border: '1px solid rgba(246, 70, 93, 0.3)'
                      }}
                    >
                      <div className="flex items-center justify-between">
                        <span style={{ color: '#F6465D' }} className="font-semibold">
                          🔴 {sell.action === 'open_short' ? '做空' : '平多'}
                        </span>
                        <span style={{ color: sell.success ? '#0ECB81' : '#F6465D' }}>
                          {sell.success ? '✓' : '✗'}
                        </span>
                      </div>
                      <div style={{ color: '#EAECEF' }}>
                        {sell.symbol} @ ${sell.price?.toFixed(2) || 'N/A'}
                      </div>
                      <div style={{ color: '#848E9C' }}>
                        数量: {sell.quantity?.toFixed(4) || 'N/A'} | 杠杆: {sell.leverage || 1}x
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
        </div>
      );
    }
    return null;
  };

  // 自定义圆点：如果该 cycle 有交易操作，则使用高亮颜色
  const CustomDot = (props: any) => {
    const { cx, cy, payload } = props;
    if (cx == null || cy == null) return null;

    const decisions = payload?.decisions as DecisionAction[] | undefined;
    const hasTradingDecisions = hasRealTrade(decisions);

    // 没有真实交易则不渲染圆点
    if (!hasTradingDecisions) return null;

    const fillColor = '#F0B90B';
    const strokeColor = '#FCD535';

    return (
      <circle
        cx={cx}
        cy={cy}
        r={3}
        fill={fillColor}
        stroke={strokeColor}
        strokeWidth={1.5}
      />
    );
  };

  return (
    <div className="binance-card p-5 animate-fade-in">
      {/* Header */}
      <div className="flex items-center justify-between mb-4">
        <div>
          <h3 className="text-lg font-bold mb-2" style={{ color: '#EAECEF' }}>{t('accountEquityCurve', language)}</h3>
          <div className="flex items-baseline gap-4">
            <span className="text-3xl font-bold mono" style={{ color: '#EAECEF' }}>
              {account?.total_equity.toFixed(2) || '0.00'}
              <span className="text-lg ml-1" style={{ color: '#848E9C' }}>USDT</span>
            </span>
            <div className="flex items-center gap-2">
              <span
                className="text-lg font-bold mono px-3 py-1 rounded"
                style={{
                  color: isProfit ? '#0ECB81' : '#F6465D',
                  background: isProfit ? 'rgba(14, 203, 129, 0.1)' : 'rgba(246, 70, 93, 0.1)',
                  border: `1px solid ${isProfit ? 'rgba(14, 203, 129, 0.2)' : 'rgba(246, 70, 93, 0.2)'}`
                }}
              >
                {isProfit ? '▲' : '▼'} {isProfit ? '+' : ''}
                {currentValue.raw_pnl_pct}%
              </span>
              <span className="text-sm mono" style={{ color: '#848E9C' }}>
                ({isProfit ? '+' : ''}{currentValue.raw_pnl.toFixed(2)} USDT)
              </span>
            </div>
          </div>
        </div>

        {/* Display Mode Toggle */}
        <div className="flex gap-1 rounded p-1" style={{ background: '#0B0E11', border: '1px solid #2B3139' }}>
          <button
            onClick={() => setDisplayMode('dollar')}
            className="px-4 py-2 rounded text-sm font-bold transition-all"
            style={displayMode === 'dollar'
              ? { background: '#F0B90B', color: '#000', boxShadow: '0 2px 8px rgba(240, 185, 11, 0.4)' }
              : { background: 'transparent', color: '#848E9C' }
            }
          >
            💵 USDT
          </button>
          <button
            onClick={() => setDisplayMode('percent')}
            className="px-4 py-2 rounded text-sm font-bold transition-all"
            style={displayMode === 'percent'
              ? { background: '#F0B90B', color: '#000', boxShadow: '0 2px 8px rgba(240, 185, 11, 0.4)' }
              : { background: 'transparent', color: '#848E9C' }
            }
          >
            📊 %
          </button>
        </div>
      </div>

      {/* Chart */}
      <div className="my-2" style={{ borderRadius: '8px', overflow: 'hidden' }}>
        <ResponsiveContainer width="100%" height={300}>
          <LineChart
            data={chartData}
            margin={{ top: 10, right: 20, left: 5, bottom: 40 }}
          >
            <defs>
              <linearGradient id="colorGradient" x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor="#F0B90B" stopOpacity={0.8} />
                <stop offset="95%" stopColor="#FCD535" stopOpacity={0.2} />
              </linearGradient>
            </defs>
            <CartesianGrid strokeDasharray="3 3" stroke="#2B3139" />
            <XAxis
              dataKey="time"
              stroke="#5E6673"
              tick={{ fill: '#848E9C', fontSize: 11 }}
              tickLine={{ stroke: '#2B3139' }}
              interval={Math.floor(chartData.length / 10)}
              angle={-15}
              textAnchor="end"
              height={60}
            />
            <YAxis
              stroke="#5E6673"
              tick={{ fill: '#848E9C', fontSize: 12 }}
              tickLine={{ stroke: '#2B3139' }}
              domain={calculateYDomain()}
              tickFormatter={(value: number) =>
                displayMode === 'dollar' ? `$${value.toFixed(0)}` : `${value}%`
              }
            />
            <Tooltip content={<CustomTooltip />} />
            <ReferenceLine
              y={displayMode === 'dollar' ? initialBalance : 0}
              stroke="#474D57"
              strokeDasharray="3 3"
              label={{
                value:
                  displayMode === 'dollar'
                    ? t('initialBalance', language).split(' ')[0]
                    : '0%',
                fill: '#848E9C',
                fontSize: 12,
              }}
            />
            <Line
              type="monotone"
              dataKey="value"
              stroke="url(#colorGradient)"
              strokeWidth={2.5}
              // 始终挂载自定义圆点组件，但只在有交易时渲染
              dot={(props: any) => <CustomDot {...props} />}
              activeDot={{
                r: 6,
                fill: '#FCD535',
                stroke: '#F0B90B',
                strokeWidth: 2,
              }}
            />
          </LineChart>
        </ResponsiveContainer>
      </div>

      {/* Range Slider */}
      {baseDisplayHistory.length > 1 && (
        <div className="mt-4" style={{ borderTop: '1px solid #2B3139' }}>
          <RangeSlider
            min={0}
            max={baseDisplayHistory.length}
            leftValue={effectiveRangeStart}
            rightValue={effectiveRangeEnd}
            onLeftChange={setRangeStart}
            onRightChange={setRangeEnd}
            step={1}
          />
        </div>
      )}

      {/* Footer Stats */}
      <div className="mt-3 grid grid-cols-4 gap-3 pt-3" style={{ borderTop: '1px solid #2B3139' }}>
        <div className="p-2 rounded transition-all hover:bg-opacity-50" style={{ background: 'rgba(240, 185, 11, 0.05)' }}>
          <div className="text-xs mb-1 uppercase tracking-wider" style={{ color: '#848E9C' }}>{t('initialBalance', language)}</div>
          <div className="text-sm font-bold mono" style={{ color: '#EAECEF' }}>
            {initialBalance.toFixed(2)} USDT
          </div>
        </div>
        <div className="p-2 rounded transition-all hover:bg-opacity-50" style={{ background: 'rgba(240, 185, 11, 0.05)' }}>
          <div className="text-xs mb-1 uppercase tracking-wider" style={{ color: '#848E9C' }}>{t('currentEquity', language)}</div>
          <div className="text-sm font-bold mono" style={{ color: '#EAECEF' }}>
            {currentValue.raw_equity.toFixed(2)} USDT
          </div>
        </div>
        <div className="p-2 rounded transition-all hover:bg-opacity-50" style={{ background: 'rgba(240, 185, 11, 0.05)' }}>
          <div className="text-xs mb-1 uppercase tracking-wider" style={{ color: '#848E9C' }}>{t('historicalCycles', language)}</div>
          <div className="text-sm font-bold mono" style={{ color: '#EAECEF' }}>{history.length} {t('cycles', language)}</div>
        </div>
        <div className="p-2 rounded transition-all hover:bg-opacity-50" style={{ background: 'rgba(240, 185, 11, 0.05)' }}>
          <div className="text-xs mb-1 uppercase tracking-wider" style={{ color: '#848E9C' }}>{t('displayRange', language)}</div>
          <div className="text-sm font-bold mono" style={{ color: '#EAECEF' }}>
            {displayHistory.length} / {baseDisplayHistory.length}
          </div>
        </div>
      </div>
    </div>
  );
}
