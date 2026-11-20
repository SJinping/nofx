import { useMemo, useState } from 'react';
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  ReferenceLine,
  Legend,
} from 'recharts';
import useSWR from 'swr';
import { api } from '../lib/api';
import { alignComparisonHistory, getCurrentComparison } from '../lib/comparisonHistory';
import type { ComparisonPoint } from '../lib/comparisonHistory';
import type { CompetitionTraderData } from '../types';
import { RangeSlider } from './RangeSlider';

interface ComparisonChartProps {
  traders: CompetitionTraderData[];
}

const axisClockFormat = new Intl.DateTimeFormat('zh-CN', {
  hour: '2-digit', minute: '2-digit',
});
const axisDateTimeFormat = new Intl.DateTimeFormat('zh-CN', {
  month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
});
const axisDateFormat = new Intl.DateTimeFormat('zh-CN', {
  month: '2-digit', day: '2-digit',
});
const sampleTimeFormat = new Intl.DateTimeFormat('zh-CN', {
  year: 'numeric', month: '2-digit', day: '2-digit',
  hour: '2-digit', minute: '2-digit', second: '2-digit',
});
const traderColors = ['#60a5fa', '#c084fc', '#34d399', '#fb923c', '#f472b6'];

export function ComparisonChart({ traders }: ComparisonChartProps) {
  const [rangeStart, setRangeStart] = useState(0);
  const [rangeEnd, setRangeEnd] = useState<number | null>(null);
  const traderIds = traders.map(t => t.trader_id).sort();

  const { data: histories, isLoading } = useSWR(
    traderIds.length > 0 ? ['comparison-equity-history', traderIds] as const : null,
    async ([, ids]) => Promise.allSettled(ids.map(async traderId => ({
      traderId,
      points: await api.getEquityHistory(traderId),
    }))),
    { refreshInterval: 10000, revalidateOnFocus: false }
  );

  const combinedData = useMemo(() => alignComparisonHistory(
    (histories ?? []).flatMap(result => result.status === 'fulfilled' ? [result.value] : [])
  ), [histories]);
  const hasUnavailableHistory = histories?.some(result => result.status === 'rejected');

  if (isLoading) {
    return (
      <div className="text-center py-16" style={{ color: '#848E9C' }}>
        <div className="spinner mx-auto mb-4"></div>
        <div className="text-sm font-semibold">Loading comparison data...</div>
      </div>
    );
  }

  if (combinedData.length === 0) {
    return (
      <div className="text-center py-16" style={{ color: '#848E9C' }}>
        <div className="text-6xl mb-4 opacity-50">📊</div>
        <div className="text-lg font-semibold mb-2">暂无历史数据</div>
        <div className="text-sm">{hasUnavailableHistory ? '部分历史数据暂不可用，正在重试' : '运行几个周期后将显示对比曲线'}</div>
      </div>
    );
  }

  // 限制显示数据点
  const MAX_DISPLAY_POINTS = 2000;
  const baseDisplayData =
    combinedData.length > MAX_DISPLAY_POINTS
      ? combinedData.slice(-MAX_DISPLAY_POINTS)
      : combinedData;

  // 计算有效的范围值 - 不使用 useEffect
  const effectiveRangeEnd = Math.min(rangeEnd ?? baseDisplayData.length, baseDisplayData.length);
  const effectiveRangeStart = Math.min(rangeStart, effectiveRangeEnd - 1);

  // 根据滑块范围过滤数据
  const displayData = (effectiveRangeStart === 0 && effectiveRangeEnd === baseDisplayData.length)
    ? baseDisplayData
    : baseDisplayData.slice(effectiveRangeStart, Math.min(effectiveRangeEnd, baseDisplayData.length));

  // 计算Y轴范围
  const calculateYDomain = () => {
    const allValues = displayData.flatMap(point =>
      Array.from(point.samples.values(), sample => sample.total_pnl_pct)
    );

    if (allValues.length === 0) return [-5, 5];

    const minVal = Math.min(...allValues);
    const maxVal = Math.max(...allValues);
    const range = Math.max(Math.abs(maxVal), Math.abs(minVal));
    const padding = Math.max(range * 0.2, 1); // 至少留1%余量

    return [
      Math.floor(minVal - padding),
      Math.ceil(maxVal + padding)
    ];
  };

  const getTraderColor = (traderId: string) =>
    traderColors[traderIds.indexOf(traderId) % traderColors.length];
  const visibleSampleCounts = new Map<string, number>();
  for (const point of displayData) {
    for (const traderId of point.samples.keys()) {
      visibleSampleCounts.set(traderId, (visibleSampleCounts.get(traderId) ?? 0) + 1);
    }
  }

  const firstTime = displayData[0].timestamp;
  const lastTime = displayData[displayData.length - 1].timestamp;
  const timeDomain = firstTime === lastTime ? [firstTime - 60000, lastTime + 60000] : [firstTime, lastTime];
  const daysVisible = (lastTime - firstTime) / 86_400_000;
  const sameDay = axisDateFormat.format(firstTime) === axisDateFormat.format(lastTime);
  const axisTimeFormat = daysVisible >= 4 ? axisDateFormat : sameDay ? axisClockFormat : axisDateTimeFormat;

  const CustomTooltip = ({ active, payload }: { active?: boolean; payload?: { payload: ComparisonPoint }[] }) => {
    const point = payload?.[0]?.payload;
    if (!active || !point) return null;

    return (
      <div className="rounded p-3 shadow-xl" style={{ background: '#1E2329', border: '1px solid #2B3139' }}>
        <div className="text-xs mb-2" style={{ color: '#848E9C' }}>
          {sampleTimeFormat.format(point.timestamp)}
        </div>
        {traders.map((trader) => {
          const sample = point.samples.get(trader.trader_id);
          if (!sample) return null;
          const pnlPct = sample.total_pnl_pct;

          return (
            <div key={trader.trader_id} className="mb-1.5 last:mb-0">
              <div className="text-xs font-semibold mb-0.5" style={{ color: getTraderColor(trader.trader_id) }}>
                {trader.trader_name} · #{sample.cycle_number}
              </div>
              <div className="text-sm mono font-bold" style={{ color: pnlPct >= 0 ? '#0ECB81' : '#F6465D' }}>
                {pnlPct >= 0 ? '+' : ''}{pnlPct.toFixed(2)}%
                <span className="text-xs ml-2 font-normal" style={{ color: '#848E9C' }}>
                  ({sample.total_equity.toFixed(2)} USDT)
                </span>
              </div>
            </div>
          );
        })}
      </div>
    );
  };

  const comparisonIds = traders.map(trader => trader.trader_id);
  const currentComparison = getCurrentComparison(combinedData, comparisonIds);

  return (
    <div>
      {hasUnavailableHistory && (
        <div className="text-xs mb-3" style={{ color: '#848E9C' }}>部分历史数据暂不可用，正在重试</div>
      )}
      <div style={{ borderRadius: '8px', overflow: 'hidden' }}>
        <ResponsiveContainer width="100%" height={520}>
        <LineChart data={displayData} margin={{ top: 20, right: 30, left: 20, bottom: 40 }}>
          <CartesianGrid strokeDasharray="3 3" stroke="#2B3139" />

          <XAxis
            dataKey="timestamp"
            type="number"
            scale="time"
            domain={timeDomain}
            tickCount={5}
            minTickGap={28}
            interval="preserveStartEnd"
            tickFormatter={(timestamp: number) => axisTimeFormat.format(timestamp)}
            stroke="#5E6673"
            tick={{ fill: '#848E9C', fontSize: 11 }}
            tickLine={{ stroke: '#2B3139' }}
            angle={0}
            textAnchor="middle"
            height={60}
          />

          <YAxis
            stroke="#5E6673"
            tick={{ fill: '#848E9C', fontSize: 12 }}
            tickLine={{ stroke: '#2B3139' }}
            domain={calculateYDomain()}
            tickFormatter={(value) => `${value.toFixed(1)}%`}
            width={60}
          />

          <Tooltip content={<CustomTooltip />} />

          <ReferenceLine
            y={0}
            stroke="#474D57"
            strokeDasharray="5 5"
            strokeWidth={1.5}
            label={{
              value: 'Break Even',
              fill: '#848E9C',
              fontSize: 11,
              position: 'right',
            }}
          />

          {traders.map((trader) => (
            <Line
              key={trader.trader_id}
              type="linear"
              dataKey={(point: ComparisonPoint) => point.samples.get(trader.trader_id)?.total_pnl_pct}
              stroke={getTraderColor(trader.trader_id)}
              strokeWidth={3}
              dot={(visibleSampleCounts.get(trader.trader_id) ?? 0) < 50 ? { fill: getTraderColor(trader.trader_id), r: 3 } : false}
              activeDot={{ r: 6, fill: getTraderColor(trader.trader_id), stroke: '#fff', strokeWidth: 2 }}
              name={`${trader.trader_name} (${trader.ai_model.toUpperCase()})`}
              connectNulls
            />
          ))}

          <Legend
            wrapperStyle={{ paddingTop: '20px' }}
            iconType="line"
            formatter={(value, entry: any) => {
              return (
                <span style={{ color: entry.color, fontWeight: 600, fontSize: '14px' }}>
                  {value}
                </span>
              );
            }}
          />
        </LineChart>
      </ResponsiveContainer>
      </div>

      {/* Range Slider */}
      {baseDisplayData.length > 1 && (
        <div className="mt-4" style={{ borderTop: '1px solid #2B3139' }}>
          <RangeSlider
            min={0}
            max={baseDisplayData.length}
            leftValue={effectiveRangeStart}
            rightValue={effectiveRangeEnd}
            onLeftChange={setRangeStart}
            onRightChange={setRangeEnd}
            step={1}
          />
        </div>
      )}

      {/* Stats */}
      <div className="mt-6 grid grid-cols-4 gap-4 pt-5" style={{ borderTop: '1px solid #2B3139' }}>
        <div className="p-3 rounded transition-all hover:bg-opacity-50" style={{ background: 'rgba(240, 185, 11, 0.05)' }}>
          <div className="text-xs mb-1 uppercase tracking-wider" style={{ color: '#848E9C' }}>对比模式</div>
          <div className="text-base font-bold" style={{ color: '#EAECEF' }}>PnL %</div>
        </div>
        <div className="p-3 rounded transition-all hover:bg-opacity-50" style={{ background: 'rgba(240, 185, 11, 0.05)' }}>
          <div className="text-xs mb-1 uppercase tracking-wider" style={{ color: '#848E9C' }}>数据点数</div>
          <div className="text-base font-bold mono" style={{ color: '#EAECEF' }}>{combinedData.length} 个</div>
        </div>
        <div className="p-3 rounded transition-all hover:bg-opacity-50" style={{ background: 'rgba(240, 185, 11, 0.05)' }}>
          <div className="text-xs mb-1 uppercase tracking-wider" style={{ color: '#848E9C' }}>当前差距</div>
          <div className="text-base font-bold mono" style={{ color: (currentComparison?.gap ?? 0) > 1 ? '#F0B90B' : '#EAECEF' }}>
            {currentComparison ? `${currentComparison.gap.toFixed(2)} 个百分点` : '—'}
          </div>
          {currentComparison && (
            <div className="mt-1 text-xs" style={{ color: '#848E9C' }}>
              截至 {sampleTimeFormat.format(currentComparison.asOf)}
              {currentComparison.samples.map((sample, i) => (
                <div key={comparisonIds[i]}>{traders[i].trader_name} · {sampleTimeFormat.format(sample.timestamp)}</div>
              ))}
            </div>
          )}
        </div>
        <div className="p-3 rounded transition-all hover:bg-opacity-50" style={{ background: 'rgba(240, 185, 11, 0.05)' }}>
          <div className="text-xs mb-1 uppercase tracking-wider" style={{ color: '#848E9C' }}>显示范围</div>
          <div className="text-base font-bold mono" style={{ color: '#EAECEF' }}>
            {displayData.length} / {baseDisplayData.length}
          </div>
        </div>
      </div>
    </div>
  );
}
