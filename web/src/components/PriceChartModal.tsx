import { useEffect, useRef, useState, useCallback } from 'react';
import {
  createChart,
  CandlestickSeries,
  HistogramSeries,
  createSeriesMarkers,
  type IChartApi,
  type ISeriesApi,
  type CandlestickData,
  type SeriesMarker,
  type Time,
} from 'lightweight-charts';
import { Modal } from './Modal';
import { api } from '../lib/api';
import type { KlineData, ExchangeOrderRecord } from '../types';

const INTERVALS = [
  { label: '5m', value: '5m' },
  { label: '15m', value: '15m' },
  { label: '1h', value: '1h' },
  { label: '4h', value: '4h' },
  { label: '1D', value: '1d' },
];

interface PriceChartModalProps {
  symbol: string | null;
  traderId: string;
  isOpen: boolean;
  onClose: () => void;
}

export function PriceChartModal({ symbol, traderId, isOpen, onClose }: PriceChartModalProps) {
  const chartContainerRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const candleSeriesRef = useRef<ISeriesApi<'Candlestick'> | null>(null);

  const [interval, setInterval] = useState('15m');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadData = useCallback(async () => {
    if (!symbol || !chartContainerRef.current) return;

    setLoading(true);
    setError(null);

    try {
      const [klines, ordersResp] = await Promise.all([
        api.getKlines(symbol, interval, 500),
        api.getExchangeOrders(traderId, symbol),
      ]);

      renderChart(klines, ordersResp.orders);
    } catch (e: any) {
      setError(e.message || 'Failed to load data');
    } finally {
      setLoading(false);
    }
  }, [symbol, interval, traderId]);

  const renderChart = useCallback(
    (klines: KlineData[], orders: ExchangeOrderRecord[]) => {
      if (!chartContainerRef.current) return;

      // Destroy previous chart
      if (chartRef.current) {
        chartRef.current.remove();
        chartRef.current = null;
        candleSeriesRef.current = null;
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
          scaleMargins: { top: 0.1, bottom: 0.25 },
        },
        timeScale: {
          borderColor: '#2B3139',
          timeVisible: true,
          secondsVisible: false,
        },
      });

      chartRef.current = chart;

      // Candlestick series
      const candleSeries = chart.addSeries(CandlestickSeries, {
        upColor: '#0ECB81',
        downColor: '#F6465D',
        borderUpColor: '#0ECB81',
        borderDownColor: '#F6465D',
        wickUpColor: '#0ECB81',
        wickDownColor: '#F6465D',
      });
      candleSeriesRef.current = candleSeries;

      // Volume series (using histogram)
      const volumeSeries = chart.addSeries(HistogramSeries, {
        priceFormat: { type: 'volume' },
        priceScaleId: 'volume',
      });
      volumeSeries.priceScale().applyOptions({
        scaleMargins: { top: 0.8, bottom: 0 },
      });

      // Map kline data
      const candleData: CandlestickData<Time>[] = klines.map((k) => ({
        time: (k.open_time / 1000) as Time,
        open: k.open,
        high: k.high,
        low: k.low,
        close: k.close,
      }));

      const volumeData = klines.map((k) => ({
        time: (k.open_time / 1000) as Time,
        value: k.volume,
        color: k.close >= k.open ? 'rgba(14, 203, 129, 0.3)' : 'rgba(246, 70, 93, 0.3)',
      }));

      candleSeries.setData(candleData);
      volumeSeries.setData(volumeData);

      // Add trade markers
      const filledOrders = orders.filter((o) => o.status === 'FILLED' && o.avg_price > 0);
      if (filledOrders.length > 0) {
        const markers: SeriesMarker<Time>[] = filledOrders
          .map((order) => {
            const orderTime = new Date(order.updated_at || order.created_at).getTime() / 1000;
            const isBuy = order.side === 'BUY';

            return {
              time: orderTime as Time,
              position: isBuy ? ('belowBar' as const) : ('aboveBar' as const),
              color: isBuy ? '#0ECB81' : '#F6465D',
              shape: isBuy ? ('arrowUp' as const) : ('arrowDown' as const),
              text: `${isBuy ? 'B' : 'S'} ${order.executed_qty}`,
            };
          })
          .sort((a, b) => (a.time as number) - (b.time as number));

        createSeriesMarkers(candleSeries, markers);
      }

      // Fit content
      chart.timeScale().fitContent();

      // Handle resize
      const resizeObserver = new ResizeObserver(() => {
        if (chartContainerRef.current) {
          chart.applyOptions({
            width: chartContainerRef.current.clientWidth,
            height: chartContainerRef.current.clientHeight,
          });
        }
      });
      resizeObserver.observe(chartContainerRef.current!);

      return () => resizeObserver.disconnect();
    },
    []
  );

  // Load data when modal opens or interval changes
  useEffect(() => {
    if (isOpen && symbol) {
      loadData();
    }
    return () => {
      if (chartRef.current) {
        chartRef.current.remove();
        chartRef.current = null;
        candleSeriesRef.current = null;
      }
    };
  }, [isOpen, symbol, interval, loadData]);

  return (
    <Modal isOpen={isOpen} onClose={onClose} title={symbol ? `${symbol} Price Chart` : ''}>
      {/* Interval selector */}
      <div className="flex items-center gap-2 mb-4">
        <span className="text-xs font-semibold" style={{ color: '#848E9C' }}>
          Interval:
        </span>
        <div className="flex gap-1 rounded p-1" style={{ background: '#0B0E11' }}>
          {INTERVALS.map((iv) => (
            <button
              key={iv.value}
              onClick={() => setInterval(iv.value)}
              className="px-3 py-1.5 rounded text-xs font-semibold transition-all"
              style={
                interval === iv.value
                  ? { background: '#F0B90B', color: '#000' }
                  : { background: 'transparent', color: '#848E9C' }
              }
            >
              {iv.label}
            </button>
          ))}
        </div>

        {/* Legend */}
        <div className="ml-auto flex items-center gap-4 text-xs" style={{ color: '#848E9C' }}>
          <span className="flex items-center gap-1">
            <span style={{ color: '#0ECB81' }}>&#9650;</span> Buy
          </span>
          <span className="flex items-center gap-1">
            <span style={{ color: '#F6465D' }}>&#9660;</span> Sell
          </span>
        </div>
      </div>

      {/* Chart container */}
      <div className="relative rounded overflow-hidden" style={{ background: '#0B0E11', border: '1px solid #2B3139' }}>
        {loading && (
          <div className="absolute inset-0 z-10 flex items-center justify-center" style={{ background: 'rgba(11, 14, 17, 0.8)' }}>
            <div className="flex flex-col items-center gap-3">
              <div
                className="w-8 h-8 rounded-full border-2 animate-spin"
                style={{ borderColor: '#2B3139', borderTopColor: '#F0B90B' }}
              />
              <span className="text-sm" style={{ color: '#848E9C' }}>
                Loading...
              </span>
            </div>
          </div>
        )}

        {error && (
          <div className="absolute inset-0 z-10 flex items-center justify-center" style={{ background: 'rgba(11, 14, 17, 0.9)' }}>
            <div className="text-center">
              <div className="text-4xl mb-3 opacity-50">&#x26A0;</div>
              <div className="text-sm" style={{ color: '#F6465D' }}>
                {error}
              </div>
              <button
                onClick={loadData}
                className="mt-3 px-4 py-1.5 rounded text-xs font-semibold transition-all"
                style={{ background: '#F0B90B', color: '#000' }}
              >
                Retry
              </button>
            </div>
          </div>
        )}

        <div ref={chartContainerRef} style={{ width: '100%', height: '60vh', minHeight: '400px' }} />
      </div>
    </Modal>
  );
}
