import type {
  SystemStatus,
  AccountInfo,
  Position,
  DecisionRecord,
  Statistics,
  TraderInfo,
  CompetitionData,
  TradedSymbolsResponse,
  TradedSymbolStatus,
} from '../types';

const API_BASE = '/api';
// const LOG_API_BASE = import.meta.env.DEV ? 'http://localhost:8081/api/logs' : '/api/logs';

export const api = {
  // 竞赛相关接口
  async getCompetition(): Promise<CompetitionData> {
    const res = await fetch(`${API_BASE}/competition`);
    if (!res.ok) throw new Error('获取竞赛数据失败');
    return res.json();
  },

  async getTraders(): Promise<TraderInfo[]> {
    const res = await fetch(`${API_BASE}/traders`);
    if (!res.ok) throw new Error('获取trader列表失败');
    return res.json();
  },

  // 获取系统状态（支持trader_id）
  async getStatus(traderId?: string): Promise<SystemStatus> {
    const url = traderId
      ? `${API_BASE}/status?trader_id=${traderId}`
      : `${API_BASE}/status`;
    const res = await fetch(url);
    if (!res.ok) throw new Error('获取系统状态失败');
    return res.json();
  },

  // 获取账户信息（支持trader_id）
  async getAccount(traderId?: string): Promise<AccountInfo> {
    const url = traderId
      ? `${API_BASE}/account?trader_id=${traderId}`
      : `${API_BASE}/account`;
    const res = await fetch(url, {
      cache: 'no-store',
      headers: {
        'Cache-Control': 'no-cache',
      },
    });
    if (!res.ok) throw new Error('获取账户信息失败');
    const data = await res.json();
    console.log('Account data fetched:', data);
    return data;
  },

  // 获取持仓列表（支持trader_id）
  async getPositions(traderId?: string): Promise<Position[]> {
    const url = traderId
      ? `${API_BASE}/positions?trader_id=${traderId}`
      : `${API_BASE}/positions`;
    const res = await fetch(url);
    if (!res.ok) throw new Error('获取持仓列表失败');
    return res.json();
  },

  // 获取决策日志（支持trader_id）
  async getDecisions(traderId?: string): Promise<DecisionRecord[]> {
    const url = traderId
      ? `${API_BASE}/decisions?trader_id=${traderId}`
      : `${API_BASE}/decisions`;
    const res = await fetch(url);
    if (!res.ok) throw new Error('获取决策日志失败');
    return res.json();
  },

  // 获取最新决策（支持trader_id）
  async getLatestDecisions(traderId?: string): Promise<DecisionRecord[]> {
    const url = traderId
      ? `${API_BASE}/decisions/latest?trader_id=${traderId}`
      : `${API_BASE}/decisions/latest`;
    const res = await fetch(url);
    if (!res.ok) throw new Error('获取最新决策失败');
    return res.json();
  },

  // 获取统计信息（支持trader_id）
  async getStatistics(traderId?: string): Promise<Statistics> {
    const url = traderId
      ? `${API_BASE}/statistics?trader_id=${traderId}`
      : `${API_BASE}/statistics`;
    const res = await fetch(url);
    if (!res.ok) throw new Error('获取统计信息失败');
    return res.json();
  },

  // 获取收益率历史数据（支持trader_id）
  async getEquityHistory(traderId?: string): Promise<any[]> {
    const url = traderId
      ? `${API_BASE}/equity-history?trader_id=${traderId}`
      : `${API_BASE}/equity-history`;
    const res = await fetch(url);
    if (!res.ok) throw new Error('获取历史数据失败');
    return res.json();
  },

  // 获取AI学习表现分析（支持trader_id）
  async getPerformance(traderId?: string): Promise<any> {
    const url = traderId
      ? `${API_BASE}/performance?trader_id=${traderId}`
      : `${API_BASE}/performance`;
    const res = await fetch(url);
    if (!res.ok) throw new Error('获取AI学习数据失败');
    return res.json();
  },

  // 获取“交易币种”汇总（前端基于 /performance + /positions 做 best-effort 聚合）
  // 优先使用后端 /api/traded-symbols；若后端版本较旧（404），再降级为 best-effort 聚合。
  async getTradedSymbols(traderId: string): Promise<TradedSymbolsResponse> {
    // 1) Preferred: backend endpoint
    try {
      const url = `${API_BASE}/traded-symbols?trader_id=${traderId}`;
      const res = await fetch(url);
      if (res.ok) {
        return res.json();
      }
      // If backend doesn't have this endpoint yet, fall back.
      if (res.status !== 404) {
        const text = await res.text();
        throw new Error(text || `获取交易币种失败 (${res.status})`);
      }
    } catch (e: any) {
      // Network / parsing errors: fall back to best-effort aggregation below.
      console.warn('getTradedSymbols: fallback to best-effort aggregation:', e?.message || e);
    }

    // 复用现有接口
    const [performance, positions] = await Promise.all([
      api.getPerformance(traderId),
      api.getPositions(traderId),
    ]);

    const symbolStats: Record<
      string,
      {
        total_trades?: number;
        winning_trades?: number;
        losing_trades?: number;
        win_rate?: number;
        total_pn_l?: number;
        avg_pn_l?: number;
      }
    > = performance?.symbol_stats || {};

    const positionsBySymbol = new Map<string, Position>();
    (positions || []).forEach((p: Position) => {
      positionsBySymbol.set(p.symbol, p);
    });

    const allSymbols = new Set<string>([
      ...Object.keys(symbolStats),
      ...Array.from(positionsBySymbol.keys()),
    ]);

    let totalRealized = 0;
    let totalUnrealized = 0;

    const symbols = Array.from(allSymbols).map((sym) => {
      const stat = symbolStats[sym] || {};
      const pos = positionsBySymbol.get(sym);

      const realized = Number(stat.total_pn_l || 0);
      const unrealized = pos ? Number(pos.unrealized_pnl || 0) : 0;
      const total = realized + unrealized;

      totalRealized += realized;
      totalUnrealized += unrealized;

      const status: TradedSymbolStatus = pos
        ? pos.side === 'long'
          ? 'holding_long'
          : 'holding_short'
        : 'closed';

      return {
        symbol: sym,
        status,
        total_pnl: total,
        realized_pnl: realized,
        unrealized_pnl: unrealized,
        avg_pnl: Number(stat.avg_pn_l || 0),
        total_trades: Number(stat.total_trades || 0),
        win_rate: Number(stat.win_rate || 0),
        win_count: Number(stat.winning_trades || 0),
        loss_count: Number(stat.losing_trades || 0),
        open_long_count: 0,
        open_short_count: 0,
        close_long_count: 0,
        close_short_count: 0,
        partial_close_count: 0,
        current_position: pos
          ? {
              entry_price: pos.entry_price,
              mark_price: pos.mark_price,
              quantity: pos.quantity,
              leverage: pos.leverage,
            }
          : undefined,
        first_trade_time: '',
        last_trade_time: '',
      };
    });

    // 排序：持仓中的优先，然后按总盈亏降序
    symbols.sort((a, b) => {
      const aHolding = a.status !== 'closed';
      const bHolding = b.status !== 'closed';
      if (aHolding !== bHolding) return aHolding ? -1 : 1;
      return (b.total_pnl || 0) - (a.total_pnl || 0);
    });

    const holdingCount = symbols.filter((s) => s.status !== 'closed').length;

    return {
      symbols,
      summary: {
        total_symbols: symbols.length,
        holding_count: holdingCount,
        closed_count: symbols.length - holdingCount,
        total_realized_pnl: totalRealized,
        total_unrealized_pnl: totalUnrealized,
      },
    };
  },

  // 平掉所有模型的所有持仓
  async closeAllPositions(): Promise<any> {
    const res = await fetch(`${API_BASE}/close-all-positions`, {
      method: 'POST',
    });
    if (!res.ok) {
      const error = await res.json();
      throw new Error(error.error || '平仓操作失败');
    }
    return res.json();
  },

  // 平掉指定trader的所有持仓
  async closePositions(traderId: string): Promise<any> {
    const res = await fetch(`${API_BASE}/close-positions?trader_id=${traderId}`, {
      method: 'POST',
    });
    if (!res.ok) {
      const error = await res.json();
      throw new Error(error.error || '平仓操作失败');
    }
    return res.json();
  },

  // 设置系统暂停状态（全局）
  async setSystemPaused(paused: boolean): Promise<any> {
    const res = await fetch(`${API_BASE}/system/pause`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ paused }),
    });
    if (!res.ok) {
      const error = await res.json();
      throw new Error(error.error || '设置暂停状态失败');
    }
    return res.json();
  },

  // 日志查看器接口
  logViewer: {
    async getTraders(): Promise<string[]> {
      const res = await fetch(`${API_BASE}/logs/traders`);
      if (!res.ok) throw new Error('获取日志Trader列表失败');
      return res.json();
    },

    async getLogList(traderId: string): Promise<any[]> {
      const res = await fetch(`${API_BASE}/logs/list?trader_id=${traderId}`);
      if (!res.ok) throw new Error('获取日志列表失败');
      return res.json();
    },

    async getLogDetail(traderId: string, filename: string): Promise<any> {
      const res = await fetch(`${API_BASE}/logs/detail?trader_id=${traderId}&filename=${filename}`);
      if (!res.ok) throw new Error('获取日志详情失败');
      return res.json();
    },
  },
};
