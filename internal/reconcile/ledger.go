package reconcile

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/adshao/go-binance/v2/futures"
	"github.com/shopspring/decimal"
)

type amounts map[string]decimal.Decimal

func (a amounts) add(asset string, value decimal.Decimal) {
	if asset != "" && !value.IsZero() {
		a[asset] = a[asset].Add(value)
	}
}

type totals struct {
	Realized amounts `json:"realized_pnl"`
	Fees     amounts `json:"commissions"`
	Funding  amounts `json:"funding"`
	Net      amounts `json:"net_by_asset"`
}

func newTotals() totals { return totals{amounts{}, amounts{}, amounts{}, amounts{}} }

func (t *totals) addFill(asset, feeAsset string, pnl, fee decimal.Decimal) {
	t.Realized.add(asset, pnl)
	t.Fees.add(feeAsset, fee)
	t.Net.add(asset, pnl)
	t.Net.add(feeAsset, fee.Neg())
}

func (t *totals) addFunding(asset string, amount decimal.Decimal) {
	t.Funding.add(asset, amount)
	t.Net.add(asset, amount)
}

type position struct {
	ID                    string          `json:"trade_id"`
	Symbol                string          `json:"symbol"`
	Side                  string          `json:"side"`
	OpenTime              int64           `json:"open_time_ms"`
	CloseTime             int64           `json:"close_time_ms,omitempty"`
	OpenQuantity          decimal.Decimal `json:"opened_qty"`
	CloseQuantity         decimal.Decimal `json:"closed_qty"`
	Remaining             decimal.Decimal `json:"remaining_qty"`
	EntryPrice            decimal.Decimal `json:"avg_entry_price"`
	ExitPrice             decimal.Decimal `json:"avg_exit_price"`
	Totals                totals          `json:"amounts"`
	openValue, closeValue decimal.Decimal
}

type ledger struct {
	Trader            string                   `json:"trader_id"`
	From              int64                    `json:"from_ms"`
	Through           int64                    `json:"through_ms_exclusive"`
	Fills             []*futures.AccountTrade  `json:"fills"`
	Income            []*futures.IncomeHistory `json:"income"`
	Closed            []*position              `json:"closed_trades"`
	Open              []*position              `json:"open_positions"`
	Daily             map[string]*totals       `json:"cash_flows_by_date_asia_shanghai"`
	CashFlows         totals                   `json:"cash_flows"`
	ObservedAt        int64                    `json:"exchange_positions_observed_at_ms"`
	ObservedPositions []observedPosition       `json:"exchange_positions_at_observation"`
	Unmatched         []string                 `json:"unmatched_close_fills,omitempty"`
	Unallocated       []int64                  `json:"unallocated_funding_ids,omitempty"`
}

var shanghai = time.FixedZone("Asia/Shanghai", 8*60*60)

func fillKey(f *futures.AccountTrade) string {
	return f.Symbol + ":" + strconv.FormatInt(f.ID, 10)
}

func (l *ledger) dayAt(ms int64) *totals {
	date := time.UnixMilli(ms).In(shanghai).Format(time.DateOnly)
	if l.Daily[date] == nil {
		day := newTotals()
		l.Daily[date] = &day
	}
	return l.Daily[date]
}

func (l *ledger) rebuild() error {
	fills := make(map[string]*futures.AccountTrade)
	for _, f := range l.Fills {
		if f == nil || f.ID <= 0 || f.Symbol == "" {
			return fmt.Errorf("invalid fill identity")
		}
		fills[fillKey(f)] = f
	}
	l.Fills = make([]*futures.AccountTrade, 0, len(fills))
	for _, f := range fills {
		l.Fills = append(l.Fills, f)
	}
	slices.SortFunc(l.Fills, func(a, b *futures.AccountTrade) int {
		return cmp.Or(cmp.Compare(a.Time, b.Time), cmp.Compare(a.Symbol, b.Symbol), cmp.Compare(a.ID, b.ID))
	})

	type incomeKey struct {
		kind string
		id   int64
	}
	income := make(map[incomeKey]*futures.IncomeHistory)
	for _, row := range l.Income {
		if row == nil || row.IncomeType == "" || row.TranID <= 0 {
			return fmt.Errorf("invalid income identity")
		}
		income[incomeKey{row.IncomeType, row.TranID}] = row
	}
	l.Income = make([]*futures.IncomeHistory, 0, len(income))
	for _, row := range income {
		l.Income = append(l.Income, row)
	}
	slices.SortFunc(l.Income, func(a, b *futures.IncomeHistory) int {
		return cmp.Or(cmp.Compare(a.Time, b.Time), cmp.Compare(a.IncomeType, b.IncomeType), cmp.Compare(a.TranID, b.TranID))
	})

	l.Daily = map[string]*totals{}
	l.CashFlows = newTotals()
	l.Open, l.Closed = []*position{}, []*position{}
	l.Unmatched, l.Unallocated = nil, nil
	open := map[string]*position{}
	for _, f := range l.Fills {
		if err := l.applyFill(f, open); err != nil {
			return fmt.Errorf("fill %s: %w", fillKey(f), err)
		}
	}
	for _, p := range open {
		l.Open = append(l.Open, p)
	}
	slices.SortFunc(l.Open, func(a, b *position) int { return cmp.Compare(a.ID, b.ID) })
	for _, row := range l.Income {
		if row.IncomeType != "FUNDING_FEE" {
			continue
		}
		amount, err := decimal.NewFromString(row.Income)
		if err != nil || row.Asset == "" {
			return fmt.Errorf("invalid funding amount for transaction %d", row.TranID)
		}
		l.dayAt(row.Time).addFunding(row.Asset, amount)
		l.CashFlows.addFunding(row.Asset, amount)
		if p := positionAt(row, l.Closed, l.Open); p != nil {
			p.Totals.addFunding(row.Asset, amount)
		} else {
			l.Unallocated = append(l.Unallocated, row.TranID)
		}
	}
	return nil
}

func positionAt(row *futures.IncomeHistory, lists ...[]*position) *position {
	var matched *position
	for _, list := range lists {
		for _, p := range list {
			if p.Symbol != row.Symbol || row.Time < p.OpenTime || (p.CloseTime != 0 && row.Time > p.CloseTime) {
				continue
			}
			// The exchange timestamps alone cannot order a funding debit and a
			// trade occurring at exactly the same millisecond.
			if matched != nil || row.Time == p.CloseTime || row.Time == p.OpenTime {
				return nil
			}
			matched = p
		}
	}
	return matched
}

func (l *ledger) applyFill(f *futures.AccountTrade, open map[string]*position) error {
	qty, qtyErr := decimal.NewFromString(f.Quantity)
	price, priceErr := decimal.NewFromString(f.Price)
	pnl, pnlErr := decimal.NewFromString(f.RealizedPnl)
	fee, feeErr := decimal.NewFromString(f.Commission)
	if qtyErr != nil || priceErr != nil || pnlErr != nil || feeErr != nil || !qty.IsPositive() || !price.IsPositive() ||
		(!fee.IsZero() && f.CommissionAsset == "") {
		return fmt.Errorf("invalid quantity, price, PnL or commission")
	}
	pnlAsset := "USDT"
	switch {
	case strings.HasSuffix(f.Symbol, "USDT"):
	case strings.HasSuffix(f.Symbol, "USDC"):
		pnlAsset = "USDC"
	default:
		return fmt.Errorf("unsupported settlement asset")
	}
	l.dayAt(f.Time).addFill(pnlAsset, f.CommissionAsset, pnl, fee)
	l.CashFlows.addFill(pnlAsset, f.CommissionAsset, pnl, fee)
	if f.Side != futures.SideTypeBuy && f.Side != futures.SideTypeSell {
		return fmt.Errorf("unknown side %s", f.Side)
	}
	oneWay := f.PositionSide == futures.PositionSideTypeBoth
	key := f.Symbol + ":" + string(f.PositionSide)
	side := "long"
	if f.Side == futures.SideTypeSell {
		side = "short"
	}
	p := open[key]
	switch f.PositionSide {
	case futures.PositionSideTypeLong, futures.PositionSideTypeShort:
		if side == strings.ToLower(string(f.PositionSide)) {
			break
		}
		if p == nil || p.Remaining.LessThan(qty) {
			l.Unmatched = append(l.Unmatched, fillKey(f))
			delete(open, key)
			return nil
		}
	case futures.PositionSideTypeBoth:
		if p == nil && !pnl.IsZero() {
			l.Unmatched = append(l.Unmatched, fillKey(f))
			return nil
		}
	default:
		return fmt.Errorf("unknown position side %s", f.PositionSide)
	}
	if p != nil && p.Side != side {
		closed := decimal.Min(p.Remaining, qty)
		closeFee := fee
		if closed.LessThan(qty) && oneWay {
			closeFee = fee.Mul(closed).Div(qty)
		}
		p.CloseQuantity = p.CloseQuantity.Add(closed)
		p.Remaining = p.Remaining.Sub(closed)
		p.closeValue = p.closeValue.Add(closed.Mul(price))
		p.ExitPrice = p.closeValue.Div(p.CloseQuantity)
		p.Totals.addFill(pnlAsset, f.CommissionAsset, pnl, closeFee)
		if p.Remaining.IsZero() {
			p.CloseTime = f.Time
			l.Closed = append(l.Closed, p)
			delete(open, key)
		}
		qty = qty.Sub(closed)
		if qty.IsZero() {
			return nil
		}
		fee, pnl = fee.Sub(closeFee), decimal.Zero
		p = nil
	}
	if p == nil {
		p = &position{ID: fillKey(f), Symbol: f.Symbol, Side: side, OpenTime: f.Time, Totals: newTotals()}
		open[key] = p
	}
	p.OpenQuantity = p.OpenQuantity.Add(qty)
	p.Remaining = p.Remaining.Add(qty)
	p.openValue = p.openValue.Add(qty.Mul(price))
	p.EntryPrice = p.openValue.Div(p.OpenQuantity)
	p.Totals.addFill(pnlAsset, f.CommissionAsset, pnl, fee)
	return nil
}

func readLedger(path string) (ledger, error) {
	var l ledger
	file, err := os.Open(path)
	if err != nil {
		return l, err
	}
	defer file.Close()
	err = json.NewDecoder(file).Decode(&l)
	return l, err
}

func writeLedger(path string, l *ledger) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".reconcile-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(l); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
