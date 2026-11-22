package reconcile

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/adshao/go-binance/v2/futures"
	"github.com/shopspring/decimal"
)

const (
	day       = 24 * time.Hour
	pageLimit = 1000
)

type exchange interface {
	income(context.Context, int64, int64) ([]*futures.IncomeHistory, error)
	fills(context.Context, string, int64, int64) ([]*futures.AccountTrade, error)
	positions(context.Context) ([]observedPosition, error)
}

type binance struct {
	client *futures.Client
	pace   <-chan time.Time
}

func newBinance(key, secret string, testnet bool) *binance {
	client := futures.NewClient(key, secret)
	if testnet {
		client.BaseURL = futures.BaseApiTestnetUrl
	}
	client.HTTPClient = &http.Client{Timeout: 20 * time.Second}
	return &binance{client: client}
}

func (b *binance) wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.pace:
		return nil
	}
}

func (b *binance) income(ctx context.Context, start, end int64) ([]*futures.IncomeHistory, error) {
	if err := b.wait(ctx); err != nil {
		return nil, err
	}
	return b.client.NewGetIncomeHistoryService().StartTime(start).EndTime(end).Limit(pageLimit).Do(ctx)
}

func (b *binance) fills(ctx context.Context, symbol string, start, end int64) ([]*futures.AccountTrade, error) {
	if err := b.wait(ctx); err != nil {
		return nil, err
	}
	return b.client.NewListAccountTradeService().Symbol(symbol).StartTime(start).EndTime(end).Limit(pageLimit).Do(ctx)
}

type observedPosition struct {
	Symbol   string          `json:"symbol"`
	Side     string          `json:"position_side"`
	Quantity decimal.Decimal `json:"quantity"`
}

func (b *binance) positions(ctx context.Context) ([]observedPosition, error) {
	if err := b.wait(ctx); err != nil {
		return nil, err
	}
	positions, err := b.client.NewGetPositionRiskService().Do(ctx)
	if err != nil {
		return nil, err
	}
	symbols := []observedPosition{}
	for _, pos := range positions {
		qty, err := decimal.NewFromString(pos.PositionAmt)
		if err != nil {
			return nil, err
		}
		if !qty.IsZero() {
			symbols = append(symbols, observedPosition{pos.Symbol, string(pos.PositionSide), qty})
		}
	}
	return symbols, nil
}

// fetchRange uses half-open time ranges internally. Split a full page rather
// than assuming the exchange returned either the oldest or newest records.
func fetchRange[T any](start, end int64, query func(int64, int64) ([]T, error)) ([]T, error) {
	rows, err := query(start, end-1)
	if err != nil {
		return nil, err
	}
	if len(rows) < pageLimit {
		return rows, nil
	}
	if end-start == 1 {
		return nil, fmt.Errorf("full Binance page at timestamp %d; cannot prove completeness", start)
	}
	mid := start + (end-start)/2
	left, err := fetchRange(start, mid, query)
	if err != nil {
		return nil, err
	}
	right, err := fetchRange(mid, end, query)
	if err != nil {
		return nil, err
	}
	return append(left, right...), nil
}

func pull(ctx context.Context, ex exchange, l *ledger, start, end int64) error {
	symbols := map[string]bool{}
	for _, t := range l.Fills {
		if t.Time >= start {
			symbols[t.Symbol] = true
		}
	}
	for _, p := range l.Open {
		symbols[p.Symbol] = true
	}
	current, err := ex.positions(ctx)
	if err != nil {
		return fmt.Errorf("positions: %w", err)
	}
	l.ObservedAt = time.Now().UnixMilli()
	l.ObservedPositions = current
	for _, pos := range current {
		symbols[pos.Symbol] = true
	}

	// Discover traded symbols from account-wide income before requesting the
	// per-symbol fills. This works even for positions already closed.
	for a := start; a < end; {
		z := min(end, a+(7*day).Milliseconds())
		rows, err := fetchRange(a, z, func(from, to int64) ([]*futures.IncomeHistory, error) {
			return ex.income(ctx, from, to)
		})
		if err != nil {
			return fmt.Errorf("income [%d,%d): %w", a, z, err)
		}
		l.Income = append(l.Income, rows...)
		for _, row := range rows {
			if row.Symbol != "" {
				symbols[row.Symbol] = true
			}
		}
		a = z
	}
	keys := make([]string, 0, len(symbols))
	for symbol := range symbols {
		keys = append(keys, symbol)
	}
	sort.Strings(keys)
	for _, symbol := range keys {
		for a := start; a < end; {
			z := min(end, a+(7*day).Milliseconds())
			rows, err := fetchRange(a, z, func(from, to int64) ([]*futures.AccountTrade, error) {
				return ex.fills(ctx, symbol, from, to)
			})
			if err != nil {
				return fmt.Errorf("fills %s [%d,%d): %w", symbol, a, z, err)
			}
			l.Fills = append(l.Fills, rows...)
			a = z
		}
	}
	return nil
}
