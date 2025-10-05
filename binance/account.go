package binance

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// AccountBalance represents an asset in the user's Binance account
type AccountBalance struct {
	Symbol   string  // e.g., BTCUSDT, ETHUSDT
	Asset    string  // e.g., BTC, ETH
	Free     float64 // available amount
	Locked   float64 // in open orders
	Total    float64 // Free + Locked
	BuyPrice float64 // average buy price computed from trade history
}

// GetAccountBalances fetches balances and computes BuyPrice for each symbol (e.g., BTCUSDT)
func (b *HttpRequest) GetAccountBalances() ([]AccountBalance, error) {
	body, err := b.SignedRequest("GET", "/api/v3/account", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch account balances: %w", err)
	}

	var result struct {
		Balances []struct {
			Asset  string `json:"asset"`
			Free   string `json:"free"`
			Locked string `json:"locked"`
		} `json:"balances"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse account balances: %w", err)
	}

	var balances []AccountBalance
	for _, bItem := range result.Balances {
		free, _ := strconv.ParseFloat(bItem.Free, 64)
		locked, _ := strconv.ParseFloat(bItem.Locked, 64)
		total := free + locked

		// Skip empty / stablecoin-only entries
		if total <= 0.01 || bItem.Asset == "USDT" {
			continue
		}

		symbol := bItem.Asset + "USDT"

		// Compute average buy price from trade history (FIFO)
		buyPrice, err := b.computeAverageBuyPrice(symbol)
		if err != nil {
			fmt.Printf("⚠️  %s: cannot compute buy price: %v\n", symbol, err)
		}

		balances = append(balances, AccountBalance{
			Symbol:   symbol,
			Asset:    bItem.Asset,
			Free:     free,
			Locked:   locked,
			Total:    total,
			BuyPrice: buyPrice,
		})
	}

	return balances, nil
}

// computeAverageBuyPrice returns weighted average buy price based on trade history
func (b *HttpRequest) computeAverageBuyPrice(symbol string) (float64, error) {
	trades, err := b.GetTradeHistory(symbol, 500)
	if err != nil {
		return 0, err
	}
	if len(trades) == 0 {
		return 0, fmt.Errorf("no trade history")
	}

	// Filter only BUY trades
	buyTrades := make([]Trade, 0)
	for _, t := range trades {
		if t.IsBuyer {
			buyTrades = append(buyTrades, t)
		}
	}
	if len(buyTrades) == 0 {
		return 0, fmt.Errorf("no BUY trades found")
	}

	// Ensure chronological order (FIFO)
	sort.Slice(buyTrades, func(i, j int) bool {
		return buyTrades[i].Time.Before(buyTrades[j].Time)
	})

	// Weighted average: sum(price * qty) / sum(qty)
	var totalQty, totalValue float64
	for _, t := range buyTrades {
		totalQty += t.Qty
		totalValue += t.Price * t.Qty
	}
	if totalQty == 0 {
		return 0, fmt.Errorf("totalQty=0")
	}

	return totalValue / totalQty, nil
}
