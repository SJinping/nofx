#!/usr/bin/env python3
"""NOFX Decision Replay / Wait Audit.

Read-only analyzer for nofx decision_logs. It parses StrategyA wait decisions,
replays later cycles over several horizons, classifies the wait reason, and
emits Markdown + CSV reports. It never writes into decision_logs and never
calls trading APIs.
"""
import argparse
import csv
import glob
import json
import math
import os
import re
import statistics
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional, Tuple

TZ = timezone(timedelta(hours=8))
DEFAULT_WINDOWS = [6, 12, 24, 48]

SECRET_KEY_RE = re.compile(r"(?i)(api[_-]?key|secret|token|password|passwd|credential|authorization)")


def parse_dt(s) -> Optional[datetime]:
    if not s:
        return None
    s = s.strip()
    if not s:
        return None
    # Go emits RFC3339 with nanoseconds; Python accepts max microseconds.
    if s.endswith("Z"):
        s = s[:-1] + "+00:00"
    m = re.match(r"^(.*\.\d{6})\d+(.*)$", s)
    if m:
        s = m.group(1) + m.group(2)
    for fmt in (None, "%Y-%m-%d %H:%M:%S", "%Y-%m-%d", "%Y%m%d_%H%M%S"):
        try:
            if fmt is None:
                dt = datetime.fromisoformat(s)
            else:
                dt = datetime.strptime(s, fmt)
            if dt.tzinfo is None:
                dt = dt.replace(tzinfo=TZ)
            return dt.astimezone(TZ)
        except Exception:
            pass
    raise ValueError(f"cannot parse datetime: {s}")


def parse_range_text(text, now: Optional[datetime] = None) -> Tuple[Optional[datetime], Optional[datetime], str]:
    if now is None:
        now = datetime.now(TZ)
    if not text:
        return now - timedelta(days=14), now, "默认最近14天"
    t = text.strip().lower()
    nmap = {"一":1,"两":2,"二":2,"三":3,"四":4,"五":5,"六":6,"七":7,"八":8,"九":9,"十":10}
    def cn_num(x: str) -> int:
        if x.isdigit():
            return int(x)
        if x in nmap:
            return nmap[x]
        if x.startswith("十") and len(x)>1:
            return 10 + nmap.get(x[1],0)
        if x.endswith("十") and len(x)>1:
            return nmap.get(x[0],1)*10
        if "十" in x:
            a,b=x.split("十",1)
            return nmap.get(a,1)*10+nmap.get(b,0)
        return 1
    m = re.search(r"最近\s*([0-9一两二三四五六七八九十]+)\s*(天|日|周|星期|个月|月)", text)
    if m:
        n = cn_num(m.group(1)); unit=m.group(2)
        days = n if unit in ("天","日") else n*7 if unit in ("周","星期") else n*30
        return now - timedelta(days=days), now, f"最近{n}{unit}"
    if "最近两周" in text or "近两周" in text:
        return now - timedelta(days=14), now, "最近两周"
    if "最近一个月" in text or "近一个月" in text:
        return now - timedelta(days=30), now, "最近一个月"
    if "今天" in text:
        return now.replace(hour=0, minute=0, second=0, microsecond=0), now, "今天"
    if "昨天" in text:
        end = now.replace(hour=0, minute=0, second=0, microsecond=0)
        return end - timedelta(days=1), end - timedelta(microseconds=1), "昨天"
    if "本周" in text or "这周" in text:
        start = (now - timedelta(days=now.weekday())).replace(hour=0, minute=0, second=0, microsecond=0)
        return start, now, "本周"
    if "上周" in text:
        this = (now - timedelta(days=now.weekday())).replace(hour=0, minute=0, second=0, microsecond=0)
        return this - timedelta(days=7), this - timedelta(microseconds=1), "上周"
    m = re.search(r"([0-9]{1,2})\s*月\s*(以来|到现在|至今)?", text)
    if m:
        month = int(m.group(1)); year = now.year
        start = datetime(year, month, 1, tzinfo=TZ)
        if start > now:
            start = datetime(year-1, month, 1, tzinfo=TZ)
        return start, now, f"{month}月以来"
    return now - timedelta(days=14), now, f"无法精确解析'{text}'，按最近14天"


def safe_float(x: Any) -> Optional[float]:
    try:
        if x is None or x == "": return None
        v = float(x)
        if math.isnan(v) or math.isinf(v): return None
        return v
    except Exception:
        return None


def redact_obj(o: Any) -> Any:
    if isinstance(o, dict):
        return {k: ("[REDACTED]" if SECRET_KEY_RE.search(str(k)) else redact_obj(v)) for k,v in o.items()}
    if isinstance(o, list):
        return [redact_obj(v) for v in o]
    return o


@dataclass
class Candidate:
    symbol: str
    price: Optional[float] = None
    structure: Optional[str] = None
    pos_range: Optional[float] = None
    dist_res_atr: Optional[float] = None
    dist_sup_atr: Optional[float] = None
    rsi7: Optional[float] = None
    macd: Optional[float] = None
    raw: Dict[str, Any] = field(default_factory=dict)


@dataclass
class Cycle:
    path: str
    cycle: int
    ts: datetime
    decisions: List[Dict[str, Any]]
    candidates: Dict[str, Candidate]
    positions: Dict[str, float]
    equity: Optional[float]
    prompt: str
    cot: str


def parse_candidates(prompt: str) -> Dict[str, Candidate]:
    out: Dict[str, Candidate] = {}
    # Candidate blocks start with ### N. SYMBOL and end before next ###.
    starts = list(re.finditer(r"(?m)^###\s+\d+\.\s+([A-Z0-9]+USDT)\s*$", prompt))
    for i, m in enumerate(starts):
        symbol = m.group(1)
        end = starts[i+1].start() if i+1 < len(starts) else len(prompt)
        block = prompt[m.end():end]
        c = Candidate(symbol=symbol)
        c.price = safe_float((re.search(r"current_price\s*=\s*([-+0-9.eE]+)", block) or [None, None])[1])
        c.structure = (re.search(r"4h_market_structure\s*=\s*([A-Za-z_\-]+)", block) or [None, None])[1]
        c.pos_range = safe_float((re.search(r"position_in_range_4h\s*=\s*([-+0-9.eE]+)", block) or [None, None])[1])
        c.dist_res_atr = safe_float((re.search(r"dist_to_resistance_atr[^=]*=\s*([-+0-9.eE]+)", block) or [None, None])[1])
        c.dist_sup_atr = safe_float((re.search(r"dist_to_support_atr[^=]*=\s*([-+0-9.eE]+)", block) or [None, None])[1])
        c.rsi7 = safe_float((re.search(r"current_rsi \(7 period\)\s*=\s*([-+0-9.eE]+)", block) or [None, None])[1])
        c.macd = safe_float((re.search(r"current_macd\s*=\s*([-+0-9.eE]+)", block) or [None, None])[1])
        c.raw = {"pos_range": c.pos_range, "dist_res_atr": c.dist_res_atr, "dist_sup_atr": c.dist_sup_atr, "structure": c.structure}
        out[symbol] = c
    return out


def parse_positions_prices(d: Dict[str, Any]) -> Dict[str, float]:
    prices: Dict[str, float] = {}
    for p in d.get("positions") or []:
        if isinstance(p, dict):
            sym = p.get("symbol")
            px = safe_float(p.get("mark_price") or p.get("current_price") or p.get("price"))
            if sym and px:
                prices[sym] = px
    return prices


def load_cycles(log_dir: Path, since: Optional[datetime], until: Optional[datetime]) -> List[Cycle]:
    cycles: List[Cycle] = []
    for path in sorted(glob.glob(str(log_dir / "decision_*.json"))):
        try:
            with open(path, "r", encoding="utf-8") as f:
                d = json.load(f)
            ts = parse_dt(d.get("timestamp"))
            if not ts:
                continue
            if since and ts < since:
                continue
            if until and ts > until:
                continue
            prompt = d.get("input_prompt") or d.get("user_prompt") or ""
            candidates = parse_candidates(prompt)
            positions = parse_positions_prices(d)
            # Include positions as candidates for hold/wait replay if absent.
            for sym, px in positions.items():
                candidates.setdefault(sym, Candidate(symbol=sym, price=px))
            cycles.append(Cycle(
                path=path,
                cycle=int(d.get("cycle_number") or d.get("cycle") or 0),
                ts=ts,
                decisions=d.get("decisions") or [],
                candidates=candidates,
                positions=positions,
                equity=safe_float((d.get("account_state") or {}).get("total_balance")),
                prompt=prompt,
                cot=d.get("cot_trace") or "",
            ))
        except Exception as e:
            print(f"WARN parse failed {path}: {e}")
    cycles.sort(key=lambda c: (c.ts, c.cycle))
    return cycles


def classify_wait(reason: str, cycle: Cycle) -> Tuple[str, List[str]]:
    text = (reason or "") + " " + cycle.cot
    tags: List[str] = []
    if re.search(r"阻力|resistance|不追多|追多|高位|区间上沿", text, re.I):
        tags.append("near_resistance_no_long")
    if re.search(r"支撑|support|不追空|追空|低位|区间下沿", text, re.I):
        tags.append("near_support_no_short")
    if re.search(r"候选.*空|无.*候选|候选列表为空|无新的候选|最多0个", text):
        tags.append("no_candidates")
    if re.search(r"夏普|连亏|胜率|回撤|风险|保守|控制", text):
        tags.append("risk_control")
    if re.search(r"持仓.*上限|仓位.*上限|保证金|margin", text, re.I):
        tags.append("position_limit")
    if not tags:
        tags.append("generic_wait")
    # Add structural tags based on candidate metrics if prompt contains them.
    if cycle.candidates:
        near_res = sum(1 for c in cycle.candidates.values() if c.dist_res_atr is not None and c.dist_res_atr <= 1.0)
        near_sup = sum(1 for c in cycle.candidates.values() if c.dist_sup_atr is not None and c.dist_sup_atr <= 1.0)
        if near_res:
            tags.append("prompt_has_near_resistance_candidates")
        if near_sup:
            tags.append("prompt_has_near_support_candidates")
    return tags[0], sorted(set(tags))


def future_window(cycles: List[Cycle], idx: int, n: int) -> List[Cycle]:
    return cycles[idx+1: idx+1+n]


def symbol_path(cycles: Iterable[Cycle], symbol: str) -> List[float]:
    vals = []
    for c in cycles:
        cand = c.candidates.get(symbol)
        if cand and cand.price:
            vals.append(cand.price)
    return vals


def pct(a: Optional[float], b: Optional[float]) -> Optional[float]:
    if a is None or b is None or a == 0:
        return None
    return (b / a - 1.0) * 100.0


def analyze_wait(cycles: List[Cycle], idx: int, dec: Dict[str, Any], windows: List[int]) -> Dict[str, Any]:
    c = cycles[idx]
    reason = dec.get("reasoning", "")
    primary, tags = classify_wait(reason, c)
    symbol = dec.get("symbol") or "ALL"
    candidate_symbols = list(c.candidates.keys())
    if symbol and symbol not in ("ALL", "") and symbol in c.candidates:
        target_symbols = [symbol]
    else:
        target_symbols = candidate_symbols[:]
    row: Dict[str, Any] = {
        "cycle": c.cycle,
        "timestamp": c.ts.isoformat(),
        "symbol": symbol or "ALL",
        "rule_primary": primary,
        "rule_tags": ";".join(tags),
        "candidate_count": len(candidate_symbols),
        "reasoning": re.sub(r"\s+", " ", reason).strip()[:260],
    }
    # Future opens/closes as behavioral proxy.
    maxw = max(windows) if windows else 0
    futmax = future_window(cycles, idx, maxw)
    for w in windows:
        fut = future_window(cycles, idx, w)
        opens = []
        closes = []
        eqs = [x.equity for x in fut if x.equity is not None]
        for fc in fut:
            for d in fc.decisions:
                action = d.get("action", "")
                if action in ("open_long", "open_short") and d.get("success", True):
                    opens.append(f"{d.get('symbol')}:{action}")
                if action in ("close", "close_long", "close_short", "partial_close") and d.get("success", True):
                    closes.append(f"{d.get('symbol')}:{action}:{d.get('decision_source','')}")
        row[f"future_open_{w}"] = "|".join(opens[:8])
        row[f"future_close_{w}"] = "|".join(closes[:8])
        row[f"equity_chg_pct_{w}"] = round(pct(c.equity, eqs[-1]), 3) if c.equity and eqs else ""
        best_up = None; best_down = None; best_sym_up = ""; best_sym_down = ""
        for sym in target_symbols:
            base = c.candidates.get(sym).price if c.candidates.get(sym) else None
            path = symbol_path(fut, sym)
            if not base or not path:
                continue
            up = pct(base, max(path)); down = pct(base, min(path))
            if up is not None and (best_up is None or up > best_up):
                best_up, best_sym_up = up, sym
            if down is not None and (best_down is None or down < best_down):
                best_down, best_sym_down = down, sym
        row[f"best_up_pct_{w}"] = round(best_up, 3) if best_up is not None else ""
        row[f"best_up_symbol_{w}"] = best_sym_up
        row[f"best_down_pct_{w}"] = round(best_down, 3) if best_down is not None else ""
        row[f"best_down_symbol_{w}"] = best_sym_down
    # Label by 24-cycle horizon when available, otherwise largest.
    hw = 24 if 24 in windows else (windows[-1] if windows else 0)
    up = row.get(f"best_up_pct_{hw}"); down = row.get(f"best_down_pct_{hw}"); opens = row.get(f"future_open_{hw}")
    label = "unknown_insufficient_price_path"
    if primary == "near_resistance_no_long":
        if isinstance(up, (int,float)) and up >= 1.2:
            label = "missed_long_breakout"
        elif isinstance(down, (int,float)) and down <= -0.6:
            label = "correct_wait_rejection"
        else:
            label = "conservative_acceptable"
    elif primary == "near_support_no_short":
        if isinstance(down, (int,float)) and down <= -1.2:
            label = "missed_short_breakdown"
        elif isinstance(up, (int,float)) and up >= 0.6:
            label = "correct_wait_rebound"
        else:
            label = "conservative_acceptable"
    elif primary in ("no_candidates", "risk_control", "position_limit", "generic_wait"):
        if opens:
            label = "possibly_over_conservative_future_open"
        elif isinstance(up, (int,float)) and up >= 1.5:
            label = "missed_tradeable_move_up"
        elif isinstance(down, (int,float)) and down <= -1.5:
            label = "missed_tradeable_move_down"
        else:
            label = "correct_or_acceptable_wait"
    row["label"] = label
    return row


def summarize(rows: List[Dict[str, Any]], windows: List[int]) -> Dict[str, Any]:
    by_rule: Dict[str, Dict[str, Any]] = {}
    by_label: Dict[str, int] = {}
    for r in rows:
        by_label[r["label"]] = by_label.get(r["label"], 0) + 1
        b = by_rule.setdefault(r["rule_primary"], {"count":0,"labels":{},"future_open_24":0,"median_best_up_24":[],"median_best_down_24":[]})
        b["count"] += 1
        b["labels"][r["label"]] = b["labels"].get(r["label"], 0) + 1
        if r.get("future_open_24"):
            b["future_open_24"] += 1
        for key, arr in (("best_up_pct_24","median_best_up_24"),("best_down_pct_24","median_best_down_24")):
            v = r.get(key)
            if isinstance(v, (int,float)):
                b[arr].append(v)
    for b in by_rule.values():
        for arr in ("median_best_up_24","median_best_down_24"):
            vals = b[arr]
            b[arr] = round(statistics.median(vals), 3) if vals else ""
        b["future_open_rate_24"] = round(b["future_open_24"] / b["count"] * 100, 1) if b["count"] else 0
    return {"by_rule": by_rule, "by_label": by_label}


def write_outputs(rows: List[Dict[str, Any]], cycles: List[Cycle], args: argparse.Namespace, since: Optional[datetime], until: Optional[datetime], range_label: str) -> Tuple[Path, Path]:
    outdir = Path(args.output_dir)
    outdir.mkdir(parents=True, exist_ok=True)
    stamp = datetime.now(TZ).strftime("%Y%m%d_%H%M%S")
    csv_path = outdir / f"decision_wait_audit_{stamp}.csv"
    md_path = outdir / f"decision_wait_audit_{stamp}.md"
    fieldnames = []
    for r in rows:
        for k in r.keys():
            if k not in fieldnames:
                fieldnames.append(k)
    with open(csv_path, "w", newline="", encoding="utf-8") as f:
        w = csv.DictWriter(f, fieldnames=fieldnames)
        w.writeheader(); w.writerows(rows)
    s = summarize(rows, args.windows)
    total = len(rows)
    label_good = sum(v for k,v in s["by_label"].items() if k.startswith("correct") or "acceptable" in k)
    label_missed = sum(v for k,v in s["by_label"].items() if k.startswith("missed"))
    lines = []
    lines.append("# NOFX Decision Replay / Wait Audit")
    lines.append("")
    lines.append(f"- Trader: `{args.trader_id}`")
    lines.append(f"- Range: {range_label} ({since.isoformat() if since else 'begin'} → {until.isoformat() if until else 'end'})")
    lines.append(f"- Decision cycles loaded: {len(cycles)}")
    if cycles:
        lines.append(f"- Cycle span: #{cycles[0].cycle} → #{cycles[-1].cycle}")
    lines.append(f"- Wait decisions audited: {total}")
    lines.append(f"- Windows: {', '.join(str(x) for x in args.windows)} cycles")
    lines.append("- Price privacy: absolute prices are not printed; report uses percentages and labels only.")
    lines.append("")
    lines.append("## Executive summary")
    if total:
        lines.append(f"- Correct/acceptable wait ratio: {label_good}/{total} = {label_good/total*100:.1f}%")
        lines.append(f"- Missed-tradeable-move ratio: {label_missed}/{total} = {label_missed/total*100:.1f}%")
        if label_missed/total > 0.25:
            lines.append("- Interpretation: wait decisions may be too conservative in this window; inspect missed cases before tightening rules.")
        elif label_good/total >= 0.65:
            lines.append("- Interpretation: wait behavior is mostly useful/acceptable in this window; no evidence that wait is purely random.")
        else:
            lines.append("- Interpretation: evidence is mixed; sample quality or price-path coverage may be insufficient.")
    else:
        lines.append("- No LLM wait decisions found in selected range.")
    lines.append("")
    lines.append("## Labels")
    for k,v in sorted(s["by_label"].items(), key=lambda kv: (-kv[1], kv[0])):
        lines.append(f"- {k}: {v}")
    lines.append("")
    lines.append("## Rule effectiveness")
    lines.append("| rule | count | labels | future open rate 24c | median best up 24c | median best down 24c |")
    lines.append("|---|---:|---|---:|---:|---:|")
    for rule,b in sorted(s["by_rule"].items(), key=lambda kv: -kv[1]["count"]):
        labels = ", ".join(f"{k}:{v}" for k,v in sorted(b["labels"].items()))
        lines.append(f"| {rule} | {b['count']} | {labels} | {b['future_open_rate_24']}% | {b['median_best_up_24']} | {b['median_best_down_24']} |")
    lines.append("")
    lines.append("## Representative missed / uncertain cases")
    shown = 0
    for r in rows:
        if r["label"].startswith("missed") or r["label"].startswith("possibly"):
            lines.append(f"- cycle #{r['cycle']} {r['timestamp']} `{r['rule_primary']}` `{r['label']}` candidates={r['candidate_count']} best_up_24={r.get('best_up_pct_24','')}% {r.get('best_up_symbol_24','')} best_down_24={r.get('best_down_pct_24','')}% {r.get('best_down_symbol_24','')} reason={r['reasoning']}")
            shown += 1
            if shown >= 12: break
    if shown == 0:
        lines.append("- None under current heuristic thresholds.")
    lines.append("")
    lines.append("## Method notes")
    lines.append("- This is a heuristic audit, not a backtest. It uses logged prompt candidate prices / position mark prices as the replay path.")
    lines.append("- `future_open_24` means the LLM opened something within the next 24 logged cycles after waiting; it is a conservatism signal, not automatically an error.")
    lines.append("- Structural rules are inferred from reasoning text and prompt metrics, so ambiguous reasoning is classified as `generic_wait` or `no_candidates`.")
    lines.append("- No trading API calls, order placement, config writes, restart, or decision_log mutation are performed.")
    lines.append("")
    lines.append(f"CSV: `{csv_path}`")
    md_path.write_text("\n".join(lines) + "\n", encoding="utf-8")
    return md_path, csv_path


def main() -> None:
    ap = argparse.ArgumentParser(description="Audit nofx wait decisions by replaying later decision-log cycles.")
    ap.add_argument("--base-dir", default="/home/admin/shijp/nofx")
    ap.add_argument("--trader-id", default="binance_ds_strategyA_tp")
    ap.add_argument("--log-dir", default=None)
    ap.add_argument("--output-dir", default=None)
    ap.add_argument("--since", default=None, help="ISO datetime, e.g. 2026-07-01 or 2026-07-01T00:00:00+08:00")
    ap.add_argument("--until", default=None)
    ap.add_argument("--range-text", default=None, help="Natural language range, e.g. 最近两周 / 最近一个月 / 上周")
    ap.add_argument("--windows", nargs="*", type=int, default=DEFAULT_WINDOWS)
    args = ap.parse_args()
    base = Path(args.base_dir)
    log_dir = Path(args.log_dir) if args.log_dir else base / "decision_logs" / args.trader_id
    args.output_dir = args.output_dir or str(base / "reports" / "decision_wait_audit")
    if args.since or args.until:
        since = parse_dt(args.since); until = parse_dt(args.until); range_label = "explicit"
    else:
        since, until, range_label = parse_range_text(args.range_text)
    cycles = load_cycles(log_dir, since, until)
    rows: List[Dict[str, Any]] = []
    for i,c in enumerate(cycles):
        for d in c.decisions:
            if d.get("action") == "wait" and (d.get("decision_source") in (None, "", "llm")):
                rows.append(analyze_wait(cycles, i, d, args.windows))
    md, csvp = write_outputs(rows, cycles, args, since, until, range_label)
    print(json.dumps({
        "ok": True,
        "trader_id": args.trader_id,
        "range_label": range_label,
        "since": since.isoformat() if since else None,
        "until": until.isoformat() if until else None,
        "cycles_loaded": len(cycles),
        "wait_decisions": len(rows),
        "markdown_report": str(md),
        "csv_report": str(csvp),
    }, ensure_ascii=False, indent=2))

if __name__ == "__main__":
    main()
