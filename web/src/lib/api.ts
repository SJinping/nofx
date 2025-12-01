import type {
  SystemStatus,
  AccountInfo,
  Position,
  DecisionRecord,
  Statistics,
  TraderInfo,
  CompetitionData,
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
