import { useEffect, useMemo, useRef, useState } from 'react';
import useSWR from 'swr';
import {
  createChart,
  LineSeries,
  type IChartApi,
  type ISeriesApi,
  type LineData,
  type Time,
} from 'lightweight-charts';
import { Modal } from './Modal';
import { api } from '../lib/api';
import { useLanguage } from '../contexts/LanguageContext';

interface EquityPoint {
  timestamp: string;
  total_equity: number;
  total_pnl: number;
  total_pnl_pct: number;
  position_count: number;
  margin_used_pct: number;
  cycle_number: number;
}

interface EquityChartModalProps {
  traderId?: string;
  isOpen: boolean;
  onClose: () => void;
  initialDisplayMode: 'dollar' | 'percent';
}

const LIMIT_OPTIONS = [10000, 20000];

export function EquityChartModal({ traderId, isOpen, onClose, initialDisplayMode }: EquityChartModalProps) {
  const { language } = useLanguage();
  const chartContainerRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const lineSeriesRef = useRef<ISeriesApi<'Line'> | null>(null);

  const [displayMode, setDisplayMode] = useState<'dollar' | 'percent'>(initialDisplayMode);
  const [limit, setLimit] = useState(10000);
  const [hoverPoint, setHoverPoint] = useState<EquityPoint | null>(null);

  useEffect(() => {
    if (isOpen) setDisplayMode(initialDisplayMode);
  }, [initialDisplayMode, isOpen]);

  const { data: history, error, isLoading, mutate } = useSWR<EquityPoint[]>(
    isOpen ? `equity-history-full-${traderId || 'default'}-${limit}` : null,
    () => api.getEquityHistory(traderId, limit),
    {
      refreshInterval: 0,
      revalidateOnFocus: false,
    }
  );

  const chartPoints = useMemo(() => {
    if (!history) return [];
    const usedTimes = new Set<number>();
    return history
      .map((point, index) => {
        const parsed = Date.parse(point.timestamp.replace(' ', 'T'));
        let time = Number.isFinite(parsed) ? Math.floor(parsed / 1000) : index;
        // lightweight-charts requires strictly unique times. Keep display stable if two records share a second.
        while (usedTimes.has(time)) time += 1;
        usedTimes.add(time);
        return { point, time };
      })
      .sort((a, b) => a.time - b.time);
  }, [history]);

  useEffect(() => {
    if (!isOpen || !chartContainerRef.current || chartPoints.length === 0) return;

    if (chartRef.current) {
      chartRef.current.remove();
      chartRef.current = null;
      lineSeriesRef.current = null;
    }

    const chart = createChart(chartContainerRef.current, {
      layout: {
        background: { color: '#0B0E11' },
        textColor: '#848E9C',
        fontFamily: "'Inter', 'SF Mono', monospace",
      },
      grid: {
        vertLines: { color: 'rgba(43, 49, 57, 0.5)' },
        horzLines: { color: 'rgba(43, 49, 57, 0.5)' },
      },
      crosshair: {
        mode: 0,
        vertLine: { color: 'rgba(240, 185, 11, 0.4)', labelBackgroundColor: '#F0B90B' },
        horzLine: { color: 'rgba(240, 185, 11, 0.4)', labelBackgroundColor: '#F0B90B' },
      },
      rightPriceScale: {
        borderColor: '#2B3139',
      },
      timeScale: {
        borderColor: '#2B3139',
        timeVisible: true,
        secondsVisible: false,
        rightOffset: 8,
        barSpacing: 4,
      },
      handleScroll: {
        mouseWheel: true,
        pressedMouseMove: true,
        horzTouchDrag: true,
        vertTouchDrag: true,
      },
      handleScale: {
        axisPressedMouseMove: true,
        mouseWheel: true,
        pinch: true,
      },
    });

    const lineSeries = chart.addSeries(LineSeries, {
      color: displayMode === 'dollar' ? '#F0B90B' : '#0ECB81',
      lineWidth: 2,
      priceLineVisible: true,
      lastValueVisible: true,
      priceFormat: {
        type: 'custom',
        formatter: (value: number) => displayMode === 'dollar' ? `${value.toFixed(2)} USDT` : `${value.toFixed(2)}%`,
      },
    });

    const pointByTime = new Map<number, EquityPoint>();
    const data: LineData<Time>[] = chartPoints.map(({ point, time }) => {
      pointByTime.set(time, point);
      return {
        time: time as Time,
        value: displayMode === 'dollar' ? point.total_equity : point.total_pnl_pct,
      };
    });

    lineSeries.setData(data);
    chart.timeScale().fitContent();
    chartRef.current = chart;
    lineSeriesRef.current = lineSeries;

    const handleCrosshairMove = (param: any) => {
      if (!param?.time) {
        setHoverPoint(null);
        return;
      }
      setHoverPoint(pointByTime.get(param.time as number) || null);
    };
    chart.subscribeCrosshairMove(handleCrosshairMove);

    const resizeObserver = new ResizeObserver(() => {
      if (chartContainerRef.current) {
        chart.applyOptions({
          width: chartContainerRef.current.clientWidth,
          height: chartContainerRef.current.clientHeight,
        });
      }
    });
    resizeObserver.observe(chartContainerRef.current);

    return () => {
      resizeObserver.disconnect();
      chart.unsubscribeCrosshairMove(handleCrosshairMove);
      chart.remove();
      chartRef.current = null;
      lineSeriesRef.current = null;
    };
  }, [chartPoints, displayMode, isOpen]);

  const latestPoint = history?.[history.length - 1] || null;
  const selectedPoint = hoverPoint || latestPoint;
  const isChinese = language === 'zh';

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={isChinese ? '账户净值曲线 - 全屏缩放' : 'Account Equity Curve - Fullscreen'}
      size="fullscreen"
    >
      <div className="h-full flex flex-col gap-3">
        <div className="flex flex-wrap items-center gap-3">
          <div className="flex gap-1 rounded p-1" style={{ background: '#0B0E11', border: '1px solid #2B3139' }}>
            <button
              onClick={() => setDisplayMode('dollar')}
              className="px-4 py-2 rounded text-sm font-bold transition-all"
              style={displayMode === 'dollar' ? { background: '#F0B90B', color: '#000' } : { background: 'transparent', color: '#848E9C' }}
            >
              USDT
            </button>
            <button
              onClick={() => setDisplayMode('percent')}
              className="px-4 py-2 rounded text-sm font-bold transition-all"
              style={displayMode === 'percent' ? { background: '#F0B90B', color: '#000' } : { background: 'transparent', color: '#848E9C' }}
            >
              %
            </button>
          </div>

          <div className="flex gap-1 rounded p-1" style={{ background: '#0B0E11', border: '1px solid #2B3139' }}>
            {LIMIT_OPTIONS.map((option) => (
              <button
                key={option}
                onClick={() => setLimit(option)}
                className="px-4 py-2 rounded text-sm font-bold transition-all"
                style={limit === option ? { background: '#F0B90B', color: '#000' } : { background: 'transparent', color: '#848E9C' }}
              >
                {isChinese ? `最近 ${option}` : `Last ${option}`}
              </button>
            ))}
          </div>

          <button
            onClick={() => chartRef.current?.timeScale().fitContent()}
            className="px-4 py-2 rounded text-sm font-bold transition-all"
            style={{ background: 'rgba(240, 185, 11, 0.1)', color: '#F0B90B', border: '1px solid rgba(240, 185, 11, 0.25)' }}
          >
            {isChinese ? '重置缩放' : 'Reset Zoom'}
          </button>

          <button
            onClick={() => mutate()}
            className="px-4 py-2 rounded text-sm font-bold transition-all"
            style={{ background: 'rgba(240, 185, 11, 0.1)', color: '#F0B90B', border: '1px solid rgba(240, 185, 11, 0.25)' }}
          >
            {isChinese ? '刷新' : 'Refresh'}
          </button>

          <div className="ml-auto text-xs" style={{ color: '#848E9C' }}>
            {isChinese ? '滚轮缩放，拖拽平移' : 'Mouse wheel to zoom, drag to pan'}
          </div>
        </div>

        {selectedPoint && (
          <div className="grid grid-cols-2 md:grid-cols-5 gap-2 text-xs">
            <Info label="Cycle" value={`#${selectedPoint.cycle_number}`} />
            <Info label={isChinese ? '时间' : 'Time'} value={selectedPoint.timestamp} />
            <Info label={isChinese ? '净值' : 'Equity'} value={`${selectedPoint.total_equity.toFixed(2)} USDT`} />
            <Info label="PnL" value={`${selectedPoint.total_pnl_pct >= 0 ? '+' : ''}${selectedPoint.total_pnl_pct.toFixed(2)}%`} valueColor={selectedPoint.total_pnl_pct >= 0 ? '#0ECB81' : '#F6465D'} />
            <Info label={isChinese ? '点数' : 'Points'} value={`${history?.length || 0} / ${limit}`} />
          </div>
        )}

        <div className="relative flex-1 rounded overflow-hidden" style={{ background: '#0B0E11', border: '1px solid #2B3139', minHeight: 0 }}>
          {isLoading && (
            <div className="absolute inset-0 z-10 flex items-center justify-center" style={{ background: 'rgba(11, 14, 17, 0.8)' }}>
              <div className="text-sm" style={{ color: '#848E9C' }}>{isChinese ? '加载完整曲线中...' : 'Loading full curve...'}</div>
            </div>
          )}
          {error && (
            <div className="absolute inset-0 z-10 flex items-center justify-center" style={{ background: 'rgba(11, 14, 17, 0.9)' }}>
              <div className="text-sm" style={{ color: '#F6465D' }}>{error.message || String(error)}</div>
            </div>
          )}
          <div ref={chartContainerRef} style={{ width: '100%', height: '100%' }} />
        </div>
      </div>
    </Modal>
  );
}

function Info({ label, value, valueColor = '#EAECEF' }: { label: string; value: string; valueColor?: string }) {
  return (
    <div className="p-2 rounded" style={{ background: 'rgba(240, 185, 11, 0.05)' }}>
      <div className="mb-1 uppercase tracking-wider" style={{ color: '#848E9C' }}>{label}</div>
      <div className="font-bold mono truncate" style={{ color: valueColor }}>{value}</div>
    </div>
  );
}
