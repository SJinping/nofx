import type { ReconciliationStatus } from '../types';
import { useLanguage } from '../contexts/LanguageContext';

export function ReconciliationNotice({ info }: { info?: ReconciliationStatus }) {
  const { language } = useLanguage();
  if (!info) return null;

  const zh = language === 'zh';
  const format = (value: string) => new Date(value).toLocaleString(zh ? 'zh-CN' : 'en', {
    timeZone: 'Asia/Shanghai',
    month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
  });

  if (info.source === 'local') {
    return <p className="mb-3 text-xs text-gray-400">{info.status === 'pending'
      ? (zh ? '正在同步币安成交，暂显示 NOFX 本地记录。' : 'Syncing Binance fills. Showing local NOFX records.')
      : (zh ? '币安对账暂不可用，当前显示 NOFX 本地记录。' : 'Binance reconciliation is unavailable. Showing local NOFX records.')}</p>;
  }

  return (
    <p className="mb-3 text-xs text-gray-400">
      {zh ? '币安成交 · 已平仓 USDT 净额' : 'Binance fills · closed net P&L in USDT'}
      {info.from && info.through && ` · ${format(info.from)} – ${format(info.through)} (UTC+8)`}
      {info.status === 'stale' && (zh ? ' · 上次成功同步' : ' · Last successful sync')}
      {!!info.unmatched_closes && (zh ? ` · ${info.unmatched_closes} 条平仓成交未配对` : ` · ${info.unmatched_closes} unmatched closing fills`)}
      {!!info.unallocated_funding && (zh ? ' · 部分资金费未归入单笔交易' : ' · Some funding could not be attributed')}
      {info.non_usdt_commission && (zh ? ' · 非 USDT 手续费未折算' : ' · Non-USDT commissions are not converted')}
    </p>
  );
}
