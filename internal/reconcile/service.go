package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"nofx/config"
	"nofx/memory"
)

const syncInterval = 10 * time.Minute

// Metadata explains the source, coverage and freshness of displayed statistics.
type Metadata struct {
	Source             string     `json:"source"`
	Status             string     `json:"status"`
	From               *time.Time `json:"from,omitempty"`
	Through            *time.Time `json:"through,omitempty"`
	UnmatchedCloses    int        `json:"unmatched_closes,omitempty"`
	UnallocatedFunding int        `json:"unallocated_funding,omitempty"`
	NonUSDTCommission  bool       `json:"non_usdt_commission,omitempty"`
}

type account struct {
	id   string
	path string
	ex   exchange
}

type snapshot struct {
	history []*memory.TradeRecord
	meta    Metadata
}

// Service publishes snapshots to the display APIs. Its worker is the sole
// writer of reconciliation files and stays outside the trading cycle.
type Service struct {
	accounts []account
	mu       sync.RWMutex
	views    map[string]snapshot
}

func New(dir string, cfg *config.Config) *Service {
	s := &Service{views: make(map[string]snapshot)}
	keys := make(map[string]int)
	for _, tr := range cfg.Traders {
		if tr.Exchange == "binance" && !tr.PaperTradingMode {
			keys[tr.BinanceAPIKey]++
		}
	}
	for _, tr := range cfg.Traders {
		if tr.Exchange != "binance" || tr.PaperTradingMode {
			continue
		}
		view := snapshot{meta: Metadata{Source: "local", Status: "pending"}}
		if keys[tr.BinanceAPIKey] != 1 || tr.BinanceAPIKey == "" || tr.BinanceSecretKey == "" ||
			tr.ID == "" || tr.ID == "." || tr.ID == ".." || filepath.Base(tr.ID) != tr.ID || strings.Contains(tr.ID, "\\") {
			view.meta.Status = "unavailable"
			log.Printf("reconciliation unavailable for %s: credentials or trader ID cannot be attributed safely", tr.ID)
		} else {
			s.accounts = append(s.accounts, account{tr.ID, filepath.Join(dir, tr.ID+".json"), newBinance(tr.BinanceAPIKey, tr.BinanceSecretKey, cfg.BinanceTestnet)})
		}
		s.views[tr.ID] = view
	}
	return s
}

// History returns the latest immutable snapshot. A nil history selects the
// existing local display path. A synced account with no trades has an empty slice.
func (s *Service) History(id string) ([]*memory.TradeRecord, *Metadata) {
	if s == nil {
		return nil, nil
	}
	s.mu.RLock()
	view, ok := s.views[id]
	s.mu.RUnlock()
	if !ok {
		return nil, nil
	}
	return view.history, &view.meta
}

func (s *Service) Run(ctx context.Context) {
	if len(s.accounts) == 0 {
		return
	}
	// The single worker paces all accounts together to leave headroom for
	// the trading process's own exchange requests during the initial import.
	pacer := time.NewTicker(250 * time.Millisecond)
	defer pacer.Stop()
	for _, a := range s.accounts {
		if client, ok := a.ex.(*binance); ok {
			client.pace = pacer.C
		}
		l, err := readLedger(a.path)
		if err == nil && l.Trader == a.id && l.From > 0 && l.Through >= l.From {
			s.publish(a.id, &l, "stale")
		}
	}
	for ctx.Err() == nil {
		now := time.Now()
		earliest := now.In(shanghai).AddDate(0, -3, 0).Add(day).UnixMilli()
		cutoff := now.Add(-time.Minute).UnixMilli()
		for _, a := range s.accounts {
			if ctx.Err() != nil {
				return
			}
			l, err := syncAccount(ctx, a, earliest, cutoff)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				s.markFailed(a.id)
				var requestErr *url.Error
				if errors.As(err, &requestErr) {
					err = requestErr.Err
				}
				log.Printf("reconciliation failed for %s: %v", a.id, err)
				continue
			}
			s.publish(a.id, &l, "ready")
		}
		timer := time.NewTimer(syncInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *Service) markFailed(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	view := s.views[id]
	view.meta.Status = "stale"
	if view.history == nil {
		view.meta.Status = "unavailable"
	}
	s.views[id] = view
}

func (s *Service) publish(id string, l *ledger, status string) {
	from, through := time.UnixMilli(l.From).UTC(), time.UnixMilli(l.Through).UTC()
	meta := Metadata{Source: "binance", Status: status, From: &from, Through: &through,
		UnmatchedCloses: len(l.Unmatched), UnallocatedFunding: len(l.Unallocated)}
	for asset, fee := range l.CashFlows.Fees {
		if asset != "USDT" && !fee.IsZero() {
			meta.NonUSDTCommission = true
		}
	}
	history := make([]*memory.TradeRecord, 0, len(l.Closed))
	for _, p := range l.Closed {
		if p == nil || !strings.HasSuffix(p.Symbol, "USDT") {
			continue
		}
		entry, _ := p.EntryPrice.Float64()
		size, _ := p.OpenQuantity.Float64()
		exit, _ := p.ExitPrice.Float64()
		net, _ := p.Totals.Net["USDT"].Float64()
		percent := 0.0
		if entry*size > 0 {
			percent = net / (entry * size) * 100
		}
		history = append(history, &memory.TradeRecord{
			TradeID: p.ID, TraderID: id, Symbol: p.Symbol, Side: p.Side,
			OpenTime: time.UnixMilli(p.OpenTime).UTC(), CloseTime: time.UnixMilli(p.CloseTime).UTC(),
			DurationS:  (p.CloseTime - p.OpenTime) / 1000,
			EntryPrice: entry, ExitPrice: exit, Quantity: size, PositionSizeUSD: entry * size,
			PnL: net, PnLPct: percent, ExitReason: "exchange",
		})
	}
	s.mu.Lock()
	s.views[id] = snapshot{history, meta}
	s.mu.Unlock()
}

func syncAccount(ctx context.Context, a account, earliest, cutoff int64) (ledger, error) {
	if err := os.MkdirAll(filepath.Dir(a.path), 0700); err != nil {
		return ledger{}, err
	}
	// Lock a separate inode because the ledger is atomically replaced.
	lock, err := os.OpenFile(a.path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return ledger{}, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return ledger{}, fmt.Errorf("reconciliation ledger is locked: %w", err)
	}
	l, err := readLedger(a.path)
	if errors.Is(err, os.ErrNotExist) {
		l = ledger{Trader: a.id, From: earliest, Through: earliest}
	} else if err != nil {
		return l, fmt.Errorf("load ledger: %w", err)
	} else if l.Trader != a.id || l.From <= 0 || l.From > l.Through {
		return l, fmt.Errorf("invalid ledger identity or coverage")
	}
	seedPath := filepath.Join(filepath.Dir(a.path), "archive", a.id+".json")
	if err := l.mergeArchive(seedPath, cutoff); err != nil {
		return l, err
	}
	if l.Through < earliest || l.Through > cutoff {
		return l, fmt.Errorf("checkpoint outside queryable history; check clock or provide contiguous archive")
	}
	start := max(earliest, l.From, l.Through-day.Milliseconds())
	if err := pull(ctx, a.ex, &l, start, cutoff); err != nil {
		return l, err
	}
	l.Through = cutoff
	if err := l.rebuild(); err != nil {
		return l, err
	}
	if err := ctx.Err(); err != nil {
		return l, err
	}
	return l, writeLedger(a.path, &l)
}

func (l *ledger) mergeArchive(path string, cutoff int64) error {
	archive, err := readLedger(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load archive: %w", err)
	}
	if archive.Trader != l.Trader || archive.From <= 0 || archive.From >= archive.Through || archive.Through > cutoff ||
		archive.Through < l.From || archive.From > l.Through {
		return fmt.Errorf("archive identity or coverage cannot be joined to ledger")
	}
	if archive.From >= l.From && archive.Through <= l.Through {
		return nil // Already covered by a previous import.
	}
	for _, f := range archive.Fills {
		if f == nil || f.Time < archive.From || f.Time >= archive.Through {
			return fmt.Errorf("archive fill is outside its coverage")
		}
	}
	for _, row := range archive.Income {
		if row == nil || row.Time < archive.From || row.Time >= archive.Through {
			return fmt.Errorf("archive income is outside its coverage")
		}
	}
	l.Fills = append(l.Fills, archive.Fills...)
	l.Income = append(l.Income, archive.Income...)
	l.From = min(l.From, archive.From)
	l.Through = max(l.Through, archive.Through)
	return nil
}
