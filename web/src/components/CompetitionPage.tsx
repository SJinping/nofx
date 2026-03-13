import useSWR, { useSWRConfig } from 'swr';
import { api } from '../lib/api';
import type { CompetitionData, MarketOverviewResponse } from '../types';
import { ComparisonChart } from './ComparisonChart';
import { useLanguage } from '../contexts/LanguageContext';
import { t } from '../i18n/translations';

export function CompetitionPage() {
  const { language } = useLanguage();
  const { mutate } = useSWRConfig();
  const { data: competition } = useSWR<CompetitionData>(
    'competition',
    api.getCompetition,
    {
      refreshInterval: 5000,
      revalidateOnFocus: true,
    }
  );

  const { data: marketOverview } = useSWR<MarketOverviewResponse>(
    'market-overview',
    api.getMarketOverview,
    { refreshInterval: 15000 }
  );

  if (!competition || !competition.traders) {
    return (
      <div className="space-y-6">
        <div className="binance-card p-8 animate-pulse">
          <div className="flex items-center justify-between mb-6">
            <div className="space-y-3 flex-1">
              <div className="skeleton h-8 w-64"></div>
              <div className="skeleton h-4 w-48"></div>
            </div>
            <div className="skeleton h-12 w-32"></div>
          </div>
        </div>
        <div className="binance-card p-6">
          <div className="skeleton h-6 w-40 mb-4"></div>
          <div className="space-y-3">
            <div className="skeleton h-20 w-full rounded"></div>
            <div className="skeleton h-20 w-full rounded"></div>
          </div>
        </div>
      </div>
    );
  }

  // 按收益率排序
  const sortedTraders = [...competition.traders].sort(
    (a, b) => b.total_pnl_pct - a.total_pnl_pct
  );

  // 找出领先者
  const leader = sortedTraders[0];

  // 全局暂停状态（所有模型共享一个暂停状态，这里取第一个trader的 is_paused）
  const globalPaused = sortedTraders[0]?.is_paused ?? false;

  const handleTogglePause = async () => {
    const isPaused = globalPaused;
    const confirmMsg = isPaused
      ? t('confirmResume', language)
      : t('confirmPause', language);

    if (!window.confirm(confirmMsg)) {
      return;
    }

    try {
      await api.setSystemPaused(!isPaused);
      mutate('competition');
    } catch (error: any) {
      alert(t('operationFailed', language) + `: ${error.message}`);
    }
  };

  return (
    <div className="space-y-5 animate-fade-in">
      {/* Competition Header - 精简版 + 全局启动/暂停开关 */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-4">
          <div className="w-12 h-12 rounded-xl flex items-center justify-center text-2xl" style={{
            background: 'linear-gradient(135deg, #F0B90B 0%, #FCD535 100%)',
            boxShadow: '0 4px 14px rgba(240, 185, 11, 0.4)'
          }}>
            🏆
          </div>
          <div>
            <h1 className="text-2xl font-bold flex items-center gap-2" style={{ color: '#EAECEF' }}>
              {t('aiCompetition', language)}
              <span className="text-xs font-normal px-2 py-1 rounded" style={{ background: 'rgba(240, 185, 11, 0.15)', color: '#F0B90B' }}>
                {competition.count} {t('traders', language)}
              </span>
            </h1>
            <p className="text-xs" style={{ color: '#848E9C' }}>
              {t('liveBattle', language)}
            </p>
          </div>
        </div>
        <div className="flex flex-col items-end gap-2">
          <div className="text-right">
            <div className="text-xs mb-1" style={{ color: '#848E9C' }}>{t('leader', language)}</div>
            <div className="text-lg font-bold" style={{ color: '#F0B90B' }}>{leader?.trader_name}</div>
            <div className="text-sm font-semibold" style={{ color: (leader?.total_pnl ?? 0) >= 0 ? '#0ECB81' : '#F6465D' }}>
              {(leader?.total_pnl ?? 0) >= 0 ? '+' : ''}{leader?.total_pnl_pct?.toFixed(2) || '0.00'}%
            </div>
          </div>
          {/* 全局启动 / 暂停按钮 */}
          <button
            onClick={handleTogglePause}
            className="flex items-center gap-2 px-3 py-2 rounded text-xs font-semibold transition-all hover:bg-opacity-80"
            style={!globalPaused
              ? { background: 'rgba(14, 203, 129, 0.12)', color: '#0ECB81', border: '1px solid rgba(14, 203, 129, 0.3)' }
              : { background: 'rgba(246, 70, 93, 0.12)', color: '#F6465D', border: '1px solid rgba(246, 70, 93, 0.3)' }
            }
            title={globalPaused ? t('resume', language) : t('pause', language)}
          >
            <div
              className={`w-2 h-2 rounded-full ${!globalPaused ? 'pulse-glow' : ''}`}
              style={{ background: !globalPaused ? '#0ECB81' : '#F6465D' }}
            />
            <span className="mono">
              {t(!globalPaused ? 'running' : 'stopped', language)}
            </span>
          </button>
        </div>
      </div>

      {/* Market Overview Bar */}
      {marketOverview && (
        <div className="binance-card px-5 py-3 animate-slide-in" style={{ animationDelay: '0.05s' }}>
          <div className="flex items-center justify-between flex-wrap gap-3">
            {/* Coins */}
            <div className="flex items-center gap-6">
              {marketOverview.coins.map((coin) => {
                const name = coin.symbol.replace('USDT', '');
                const changeColor = coin.change_24h >= 0 ? '#0ECB81' : '#F6465D';
                const arrow = coin.change_24h >= 0 ? '▲' : '▼';
                const frPct = (coin.funding_rate * 100).toFixed(4);
                const frColor = coin.funding_rate > 0 ? '#F6465D' : coin.funding_rate < 0 ? '#0ECB81' : '#848E9C';
                return (
                  <div key={coin.symbol} className="flex items-center gap-3">
                    <span className="text-sm font-bold" style={{ color: '#EAECEF' }}>{name}</span>
                    <span className="font-mono text-sm font-semibold" style={{ color: '#EAECEF' }}>
                      ${coin.price < 1 ? coin.price.toFixed(4) : coin.price < 100 ? coin.price.toFixed(2) : coin.price.toLocaleString(undefined, { maximumFractionDigits: 0 })}
                    </span>
                    <span className="font-mono text-xs font-semibold" style={{ color: changeColor }}>
                      {arrow} {Math.abs(coin.change_24h).toFixed(2)}%
                    </span>
                    <span className="text-xs" style={{ color: '#848E9C' }}>
                      FR <span className="font-mono" style={{ color: frColor }}>{frPct}%</span>
                    </span>
                  </div>
                );
              })}
            </div>

            {/* Fear & Greed */}
            {marketOverview.fear_greed.value >= 0 && (() => {
              const v = marketOverview.fear_greed.value;
              const fgColor = v <= 25 ? '#F6465D' : v <= 45 ? '#FF8F00' : v <= 55 ? '#848E9C' : v <= 75 ? '#0ECB81' : '#00E676';
              const labelMap: Record<string, string> = {
                'Extreme Fear': '极度恐惧',
                'Fear': '恐惧',
                'Neutral': '中性',
                'Greed': '贪婪',
                'Extreme Greed': '极度贪婪',
              };
              const label = language === 'zh' ? (labelMap[marketOverview.fear_greed.label] || marketOverview.fear_greed.label) : marketOverview.fear_greed.label;
              return (
                <div className="flex items-center gap-2">
                  <span className="text-xs" style={{ color: '#848E9C' }}>
                    {language === 'zh' ? '情绪' : 'Sentiment'}
                  </span>
                  <span className="font-mono text-sm font-bold" style={{ color: fgColor }}>{v}</span>
                  <span className="text-xs font-semibold px-1.5 py-0.5 rounded" style={{ background: `${fgColor}18`, color: fgColor }}>
                    {label}
                  </span>
                </div>
              );
            })()}
          </div>
        </div>
      )}

      {/* Left/Right Split: Performance Chart + Leaderboard */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-5">
        {/* Left: Performance Comparison Chart */}
        <div className="binance-card p-5 animate-slide-in" style={{ animationDelay: '0.1s' }}>
          <div className="flex items-center justify-between mb-4">
            <h2 className="text-lg font-bold flex items-center gap-2" style={{ color: '#EAECEF' }}>
              {t('performanceComparison', language)}
            </h2>
            <div className="text-xs" style={{ color: '#848E9C' }}>
              {t('realTimePnL', language)}
            </div>
          </div>
          <ComparisonChart traders={sortedTraders} />
        </div>

        {/* Right: Leaderboard */}
        <div className="binance-card p-5 animate-slide-in" style={{ animationDelay: '0.1s' }}>
          <div className="flex items-center justify-between mb-4">
            <h2 className="text-lg font-bold flex items-center gap-2" style={{ color: '#EAECEF' }}>
              {t('leaderboard', language)}
            </h2>
            <div className="text-xs px-2 py-1 rounded" style={{ background: 'rgba(240, 185, 11, 0.1)', color: '#F0B90B', border: '1px solid rgba(240, 185, 11, 0.2)' }}>
              {t('live', language)}
            </div>
          </div>
          <div className="space-y-2">
            {sortedTraders.map((trader, index) => {
              const isLeader = index === 0;
              const aiModelColor = trader.ai_model === 'qwen' ? '#c084fc' : '#60a5fa';

              return (
                <div
                  key={trader.trader_id}
                  className="rounded p-3 transition-all duration-300 hover:translate-y-[-1px]"
                  style={{
                    background: isLeader ? 'linear-gradient(135deg, rgba(240, 185, 11, 0.08) 0%, #0B0E11 100%)' : '#0B0E11',
                    border: `1px solid ${isLeader ? 'rgba(240, 185, 11, 0.4)' : '#2B3139'}`,
                    boxShadow: isLeader ? '0 3px 15px rgba(240, 185, 11, 0.12), 0 0 0 1px rgba(240, 185, 11, 0.15)' : '0 1px 4px rgba(0, 0, 0, 0.3)'
                  }}
                >
                  <div className="flex items-center justify-between">
                    {/* Rank & Name */}
                    <div className="flex items-center gap-3">
                      <div className="text-2xl w-6">
                        {index === 0 ? '🥇' : index === 1 ? '🥈' : '🥉'}
                      </div>
                      <div>
                        <div className="font-bold text-sm" style={{ color: '#EAECEF' }}>{trader.trader_name}</div>
                        <div className="text-xs mono font-semibold" style={{ color: aiModelColor }}>
                          {trader.ai_model.toUpperCase()}
                        </div>
                      </div>
                    </div>

                    {/* Stats */}
                    <div className="flex items-center gap-3">
                      {/* Total Equity */}
                      <div className="text-right">
                        <div className="text-xs" style={{ color: '#848E9C' }}>{t('equity', language)}</div>
                        <div className="text-sm font-bold mono" style={{ color: '#EAECEF' }}>
                          {trader.total_equity?.toFixed(2) || '0.00'}
                        </div>
                      </div>

                      {/* P&L */}
                      <div className="text-right min-w-[90px]">
                        <div className="text-xs" style={{ color: '#848E9C' }}>{t('pnl', language)}</div>
                        <div
                          className="text-lg font-bold mono"
                          style={{ color: (trader.total_pnl ?? 0) >= 0 ? '#0ECB81' : '#F6465D' }}
                        >
                          {(trader.total_pnl ?? 0) >= 0 ? '+' : ''}
                          {trader.total_pnl_pct?.toFixed(2) || '0.00'}%
                        </div>
                        <div className="text-xs mono" style={{ color: '#848E9C' }}>
                          {(trader.total_pnl ?? 0) >= 0 ? '+' : ''}{trader.total_pnl?.toFixed(2) || '0.00'}
                        </div>
                      </div>

                      {/* Positions */}
                      <div className="text-right">
                        <div className="text-xs" style={{ color: '#848E9C' }}>{t('pos', language)}</div>
                        <div className="text-sm font-bold mono" style={{ color: '#EAECEF' }}>
                          {trader.position_count}
                        </div>
                        <div className="text-xs" style={{ color: '#848E9C' }}>
                          {trader.margin_used_pct.toFixed(1)}%
                        </div>
                      </div>

                      {/* Status */}
                      <div>
                        <div
                          className="px-2 py-1 rounded text-xs font-bold"
                          style={trader.is_running
                            ? { background: 'rgba(14, 203, 129, 0.1)', color: '#0ECB81' }
                            : { background: 'rgba(246, 70, 93, 0.1)', color: '#F6465D' }
                          }
                        >
                          {trader.is_running ? '●' : '○'}
                        </div>
                      </div>
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      </div>

      {/* Head-to-Head Stats */}
      {competition.traders.length === 2 && (
        <div className="binance-card p-5 animate-slide-in" style={{ animationDelay: '0.3s' }}>
          <h2 className="text-lg font-bold mb-4 flex items-center gap-2" style={{ color: '#EAECEF' }}>
            {t('headToHead', language)}
          </h2>
          <div className="grid grid-cols-2 gap-4">
            {sortedTraders.map((trader, index) => {
              const isWinning = index === 0;
              const opponent = sortedTraders[1 - index];
              const gap = trader.total_pnl_pct - opponent.total_pnl_pct;

              return (
                <div
                  key={trader.trader_id}
                  className="p-4 rounded transition-all duration-300 hover:scale-[1.02]"
                  style={isWinning
                    ? {
                        background: 'linear-gradient(135deg, rgba(14, 203, 129, 0.08) 0%, rgba(14, 203, 129, 0.02) 100%)',
                        border: '2px solid rgba(14, 203, 129, 0.3)',
                        boxShadow: '0 3px 15px rgba(14, 203, 129, 0.12)'
                      }
                    : {
                        background: '#0B0E11',
                        border: '1px solid #2B3139',
                        boxShadow: '0 1px 4px rgba(0, 0, 0, 0.3)'
                      }
                  }
                >
                  <div className="text-center">
                    <div
                      className="text-base font-bold mb-2"
                      style={{ color: trader.ai_model === 'qwen' ? '#c084fc' : '#60a5fa' }}
                    >
                      {trader.trader_name}
                    </div>
                    <div className="text-2xl font-bold mono mb-1" style={{ color: (trader.total_pnl ?? 0) >= 0 ? '#0ECB81' : '#F6465D' }}>
                      {(trader.total_pnl ?? 0) >= 0 ? '+' : ''}{trader.total_pnl_pct?.toFixed(2) || '0.00'}%
                    </div>
                    {isWinning && gap > 0 && (
                      <div className="text-xs font-semibold" style={{ color: '#0ECB81' }}>
                        {t('leadingBy', language, { gap: gap.toFixed(2) })}
                      </div>
                    )}
                    {!isWinning && gap < 0 && (
                      <div className="text-xs font-semibold" style={{ color: '#F6465D' }}>
                        {t('behindBy', language, { gap: Math.abs(gap).toFixed(2) })}
                      </div>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}
