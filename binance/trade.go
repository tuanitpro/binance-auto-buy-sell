package binance

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"
)

// Trade represents a single user trade record on Binance
type Trade struct {
	Symbol  string
	Price   float64
	Qty     float64
	IsBuyer bool
	Time    time.Time
}

// DCAResult holds each DCA target summary
type DCAResult struct {
	TargetAvg float64
	BuyQty    float64
	NewTotal  float64
	USDTSpent float64
	DropPct   float64
}

// GetPrice retrieves the current price for a symbol (e.g., BTCUSDT)
func (b *HttpRequest) GetPrice(symbol string) (float64, error) {
	body, err := b.PublicRequest("/api/v3/ticker/price", map[string]string{"symbol": symbol})
	if err != nil {
		return 0, err
	}

	var result struct {
		Symbol string `json:"symbol"`
		Price  string `json:"price"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("failed to parse price response: %w", err)
	}

	price, err := strconv.ParseFloat(result.Price, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid price format for %s: %w", symbol, err)
	}

	return price, nil
}

// PlaceOrder places a market buy/sell order
func (b *HttpRequest) PlaceOrder(symbol, side string, quantity float64) error {
	params := map[string]string{
		"symbol":   symbol,
		"side":     side,     // BUY or SELL
		"type":     "MARKET", //LIMIT or MARKET
		"quantity": fmt.Sprintf("%.6f", quantity),
	}

	body, err := b.SignedRequest("POST", "/api/v3/order", params)
	if err != nil {
		return fmt.Errorf("failed to place order: %w", err)
	}

	var result struct {
		OrderId int64  `json:"orderId"`
		Status  string `json:"status"`
	}
	_ = json.Unmarshal(body, &result)
	fmt.Printf("✅ Order placed: %s %s (ID: %d, Status: %s)\n", side, symbol, result.OrderId, result.Status)
	return nil
}

// GetTradeHistory retrieves the user's trade history for a symbol
func (b *HttpRequest) GetTradeHistory(symbol string, limit int) ([]Trade, error) {
	params := map[string]string{
		"symbol": symbol,
	}
	if limit > 0 {
		params["limit"] = fmt.Sprintf("%d", limit)
	}

	body, err := b.SignedRequest("GET", "/api/v3/myTrades", params)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch trade history: %w", err)
	}

	// Struct matching Binance API JSON response
	var rawTrades []struct {
		Price   string `json:"price"`
		Qty     string `json:"qty"`
		IsBuyer bool   `json:"isBuyer"`
		Time    int64  `json:"time"`
	}
	if err := json.Unmarshal(body, &rawTrades); err != nil {
		return nil, fmt.Errorf("failed to parse trade history: %w", err)
	}

	var trades []Trade
	for _, t := range rawTrades {
		price, _ := strconv.ParseFloat(t.Price, 64)
		qty, _ := strconv.ParseFloat(t.Qty, 64)
		trades = append(trades, Trade{
			Symbol:  symbol,
			Price:   price,
			Qty:     qty,
			IsBuyer: t.IsBuyer,
			Time:    time.UnixMilli(t.Time),
		})
	}
	// ✅ Ensure chronological order (FIFO)
	sort.Slice(trades, func(i, j int) bool {
		return trades[i].Time.Before(trades[j].Time)
	})
	return trades, nil

}

// CalculateDCA computes how much to buy to reduce loss by different targets:
//   - 30% reduction
//   - 50% reduction
//   - 80% reduction
//   - 0% (break even)
func (b *HttpRequest) CalculateDCA(symbol string, currentPrice float64, currentQty, buyPrice float64) ([]DCAResult, error) {
	if currentPrice <= 0 {
		return nil, fmt.Errorf("invalid current price for %s", symbol)
	}
	if currentQty <= 0 || buyPrice <= 0 {
		return nil, fmt.Errorf("invalid input: qty=%.2f buyPrice=%.2f", currentQty, buyPrice)
	}

	results := []DCAResult{}

	// current loss percentage
	lossPct := (1 - currentPrice/buyPrice) * 100
	if lossPct <= 0 {
		return results, nil // no loss, no DCA needed
	}

	// different DCA goals
	targetLossReductions := []float64{30, 50, 80, 0} // 30%, 50%, 80%, 0% (break-even)

	for _, reduction := range targetLossReductions {
		targetLoss := lossPct * (1 - reduction/100) // e.g. 8.4% * (1-0.5)=4.2%
		targetAvg := currentPrice / (1 - targetLoss/100)

		// DCA formula: q2 = ((p_target - p1)*q1) / (p2 - p_target)
		q2 := ((targetAvg - buyPrice) * currentQty) / (currentPrice - targetAvg)
		if q2 <= 0 {
			continue
		}

		totalQty := currentQty + q2
		newAvg := (buyPrice*currentQty + currentPrice*q2) / totalQty
		usdtSpent := q2 * currentPrice
		newLossPct := (1 - currentPrice/newAvg) * 100

		results = append(results, DCAResult{
			TargetAvg: newAvg,
			BuyQty:    q2,
			NewTotal:  totalQty,
			USDTSpent: usdtSpent,
			DropPct:   newLossPct,
		})
	}

	return results, nil
}
