package binance

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
)

// AccountBalance represents an asset in the user's Binance account
type AccountBalance struct {
	Symbol       string  // e.g., BTCUSDT, ETHUSDT
	Asset        string  // e.g., BTC, ETH
	Free         float64 // available amount
	Locked       float64 // in open orders
	Total        float64 // Free + Locked
	AveragePrice float64 // average buy price computed from trade history
	CostPrice    float64
	TotalUSDT    float64 // Total * AveragePrice
}

// GetAccountBalances fetches balances and computes AveragePrice for each symbol (e.g., BTCUSDT)
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
		averagePrice, costPrice, err := b.computeAverageAveragePrice(symbol)
		if err != nil {
			fmt.Printf("⚠️  %s: cannot compute buy price: %v\n", symbol, err)
		}

		balances = append(balances, AccountBalance{
			Symbol:       symbol,
			Asset:        bItem.Asset,
			Free:         free,
			Locked:       locked,
			Total:        total,
			AveragePrice: averagePrice,
			CostPrice:    costPrice,
			TotalUSDT:    total * averagePrice,
		})
	}

	return balances, nil
}

// computeAverageAveragePrice returns both average buy price and cost price (after sells)
func (b *HttpRequest) computeAverageAveragePrice(symbol string) (averagePrice, costPrice float64, err error) {
	trades, err := b.GetTradeHistory(symbol, 500)
	if err != nil {
		return 0, 0, err
	}
	if len(trades) == 0 {
		return 0, 0, fmt.Errorf("no trade history")
	}

	// Ensure chronological order (FIFO)
	sort.Slice(trades, func(i, j int) bool {
		return trades[i].Time.Before(trades[j].Time)
	})

	// Weighted average (for AveragePrice) and running balance (for CostPrice)
	var totalBuyQty, totalBuyValue float64
	var currentQty, currentCost float64

	for _, t := range trades {
		if t.IsBuyer {
			totalBuyQty += t.Qty
			totalBuyValue += t.Price * t.Qty

			// FIFO-based remaining position
			currentCost += t.Price * t.Qty
			currentQty += t.Qty
		} else {
			// Sell — reduce from position cost
			if currentQty > 0 {
				avgCost := currentCost / currentQty
				reduce := math.Min(t.Qty, currentQty)
				currentCost -= avgCost * reduce
				currentQty -= reduce
			}
		}
	}

	if totalBuyQty == 0 {
		return 0, 0, fmt.Errorf("no BUY trades found")
	}
	averagePrice = totalBuyValue / totalBuyQty

	if currentQty == 0 {
		return averagePrice, 0, fmt.Errorf("no holdings left — all sold")
	}
	costPrice = currentCost / currentQty

	return averagePrice, costPrice, nil
}
