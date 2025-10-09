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

// Kline represents a simplified kline/candle
type Kline struct {
	OpenTime  time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	CloseTime time.Time
}

// GetKlines fetches klines (candles) for symbol/interval. interval like "4h". limit optional <=1000
func (b *HttpRequest) GetKlines(symbol, interval string, limit int) ([]Kline, error) {
	// use PublicRequest to call endpoint but PublicRequest composes endpoint+params, so:
	body, err := b.PublicRequest("/api/v3/klines", map[string]string{"symbol": symbol, "interval": interval, "limit": strconv.Itoa(limit)})
	if err != nil {
		return nil, fmt.Errorf("GetKlines error: %w", err)
	}

	// kline response: array of arrays
	var raw [][]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse klines: %w", err)
	}

	out := make([]Kline, 0, len(raw))
	for _, r := range raw {
		// index mapping: 0 openTime, 1 open, 2 high, 3 low, 4 close, 5 volume, 6 closeTime...
		openTimeMs := int64(r[0].(float64))
		openStr := r[1].(string)
		highStr := r[2].(string)
		lowStr := r[3].(string)
		closeStr := r[4].(string)
		volStr := r[5].(string)
		closeTimeMs := int64(r[6].(float64))

		open, _ := strconv.ParseFloat(openStr, 64)
		high, _ := strconv.ParseFloat(highStr, 64)
		low, _ := strconv.ParseFloat(lowStr, 64)
		closeP, _ := strconv.ParseFloat(closeStr, 64)
		vol, _ := strconv.ParseFloat(volStr, 64)

		out = append(out, Kline{
			OpenTime:  time.UnixMilli(openTimeMs),
			Open:      open,
			High:      high,
			Low:       low,
			Close:     closeP,
			Volume:    vol,
			CloseTime: time.UnixMilli(closeTimeMs),
		})
	}

	return out, nil
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
