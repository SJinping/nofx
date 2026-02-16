export interface SystemStatus {
  trader_id: string;
  trader_name: string;
  ai_model: string;
  is_running: boolean;
  is_paused?: boolean;  // 新增：是否暂停（可选）
  start_time: string;
  runtime_minutes: number;
  call_count: number;
  initial_balance: number;
  scan_interval: string;
  stop_until: string;
  last_reset_time: string;
  ai_provider: string;
}

export interface AccountInfo {
  total_equity: number;
  wallet_balance: number;
  unrealized_profit: number;
  available_balance: number;
  total_pnl: number;
  total_pnl_pct: number;
  total_unrealized_pnl: number;
  initial_balance: number;
  daily_pnl: number;
  position_count: number;
  margin_used: number;
  margin_used_pct: number;
}

export interface Position {
  symbol: string;
  side: string;
  entry_price: number;
  mark_price: number;
  quantity: number;
  leverage: number;
  unrealized_pnl: number;
  unrealized_pnl_pct: number;
  liquidation_price: number;
  margin_used: number;
}

export interface DecisionAction {
  action: string;
  symbol: string;
  quantity: number;
  leverage: number;
  price: number;
  order_id: number;
  timestamp: string;
  success: boolean;
  error?: string;
}

export interface AccountSnapshot {
  total_balance: number;
  available_balance: number;
  total_unrealized_profit: number;
  position_count: number;
  margin_used_pct: number;
}

export interface DecisionRecord {
  timestamp: string;
  cycle_number: number;
  input_prompt: string;
  cot_trace: string;
  decision_json: string;
  account_state: AccountSnapshot;
  positions: any[];
  candidate_coins: string[];
  decisions: DecisionAction[];
  execution_log: string[];
  success: boolean;
  error_message?: string;
}

export interface Statistics {
  total_cycles: number;
  successful_cycles: number;
  failed_cycles: number;
  total_open_positions: number;
  total_close_positions: number;
}

// ===== Traded Symbols (Symbol Performance) =====
export interface TradedSymbolCurrentPosition {
  entry_price: number;
  mark_price: number;
  quantity: number;
  leverage: number;
}

export type TradedSymbolStatus = 'holding_long' | 'holding_short' | 'closed';

export interface TradedSymbol {
  symbol: string;
  status: TradedSymbolStatus;

  // P&L
  total_pnl: number;
  realized_pnl: number;
  unrealized_pnl: number;
  avg_pnl: number;

  // Stats
  total_trades: number;
  win_rate: number;
  win_count: number;
  loss_count: number;

  // Action counters (best-effort; may be 0 if backend doesn't provide)
  open_long_count: number;
  open_short_count: number;
  close_long_count: number;
  close_short_count: number;
  partial_close_count: number;

  // Current position (if holding)
  current_position?: TradedSymbolCurrentPosition;

  // Time ranges (best-effort; may be empty if backend doesn't provide)
  first_trade_time: string;
  last_trade_time: string;
}

export interface TradedSymbolsSummary {
  total_symbols: number;
  holding_count: number;
  closed_count: number;
  total_realized_pnl: number;
  total_unrealized_pnl: number;
}

export interface TradedSymbolsResponse {
  symbols: TradedSymbol[];
  summary: TradedSymbolsSummary;
}

// ===== Exchange (Orders-based) =====

export interface ExchangeOrderRecord {
  symbol: string;
  order_id: number;
  client_order_id?: string;
  side: string; // BUY/SELL
  position_side: string; // LONG/SHORT/BOTH
  type: string; // MARKET/LIMIT/...
  status: string; // NEW/FILLED/...
  reduce_only: boolean;

  price: number;
  stop_price: number;
  avg_price: number;
  orig_qty: number;
  executed_qty: number;
  cum_quote: number;
  time_in_force: string;
  working_type: string;
  created_at: string;
  updated_at: string;
}

export interface ExchangeOrderStats {
  symbol: string;
  total_orders: number;
  filled_orders: number;
  total_executed_qty: number;
  total_notional: number;
  estimated_fee: number;
  realized_pnl: number;
  trades: number;
  win_count: number;
  loss_count: number;
  win_rate: number;
  avg_pnl: number;
  avg_hold_seconds: number;
  first_trade_time: string;
  last_trade_time: string;
  open_qty_remaining: number;
}

export interface ExchangeOrdersResponse {
  symbol: string;
  start_time: string;
  end_time: string;
  orders: ExchangeOrderRecord[];
  stats: ExchangeOrderStats;
}

export interface ExchangeTradedSymbolsSummary {
  total_symbols: number;
  total_realized_pnl: number;
  total_estimated_fee: number;
  total_trades: number;
}

export interface ExchangeTradedSymbolsResponse {
  symbols: ExchangeOrderStats[];
  summary: ExchangeTradedSymbolsSummary;
}

// 新增：竞赛相关类型
export interface TraderInfo {
  trader_id: string;
  trader_name: string;
  ai_model: string;
}

export interface CompetitionTraderData {
  trader_id: string;
  trader_name: string;
  ai_model: string;
  total_equity: number;
  total_pnl: number;
  total_pnl_pct: number;
  position_count: number;
  margin_used_pct: number;
  call_count: number;
  is_running: boolean;
  is_paused?: boolean;
}

export interface CompetitionData {
  traders: CompetitionTraderData[];
  count: number;
}

// K线数据
export interface KlineData {
  open_time: number;
  open: number;
  high: number;
  low: number;
  close: number;
  volume: number;
  close_time: number;
}
