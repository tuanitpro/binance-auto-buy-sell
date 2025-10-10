package utils

import (
	"errors"
	"fmt"
	"math"
)

type PredictResult struct {
	NextPrice float64
	ChangePct float64
	Signal    string
	MACD      float64
	SignalMA  float64
	Histogram float64
	RSI       float64
	StochRSI  float64
	DayHigh   float64 // new: highest price of the current day
}

// CalculateRSI computes RSI for the given closes using Wilder’s smoothing
func CalculateRSI(closes []float64, period int) (float64, error) {
	if len(closes) < period+1 {
		return 0, errors.New("not enough closes to calculate RSI")
	}

	// initial average gain/loss using first period
	gainSum := 0.0
	lossSum := 0.0
	for i := 1; i <= period; i++ {
		delta := closes[i] - closes[i-1]
		if delta > 0 {
			gainSum += delta
		} else {
			lossSum += -delta
		}
	}
	avgGain := gainSum / float64(period)
	avgLoss := lossSum / float64(period)

	// Wilder smoothing for remaining data
	for i := period + 1; i < len(closes); i++ {
		delta := closes[i] - closes[i-1]
		gain := 0.0
		loss := 0.0
		if delta > 0 {
			gain = delta
		} else {
			loss = -delta
		}
		avgGain = (avgGain*(float64(period-1)) + gain) / float64(period)
		avgLoss = (avgLoss*(float64(period-1)) + loss) / float64(period)
	}

	if avgLoss == 0 {
		return 100, nil
	}
	rs := avgGain / avgLoss
	rsi := 100 - (100 / (1 + rs))
	if math.IsNaN(rsi) {
		return 0, nil
	}
	return rsi, nil
}

// CalculateStochRSI computes the Stochastic RSI with optional 3-period SMA smoothing.
// Returns the smoothed StochRSI (0–1 scale).
func CalculateStochRSI(closes []float64, rsiPeriod, stochPeriod int) (float64, error) {
	if len(closes) < rsiPeriod+stochPeriod {
		return 0, fmt.Errorf("not enough closes for StochRSI calculation: got %d need %d", len(closes), rsiPeriod+stochPeriod)
	}

	// --- Step 1: Build RSI series ---
	rsiSeries := make([]float64, 0, len(closes)-rsiPeriod)
	for i := rsiPeriod + 1; i <= len(closes); i++ {
		rsi, err := CalculateRSI(closes[i-(rsiPeriod+1):i], rsiPeriod)
		if err != nil || rsi == 0 {
			continue
		}
		rsiSeries = append(rsiSeries, rsi)
	}

	if len(rsiSeries) < stochPeriod {
		return 0, fmt.Errorf("not enough RSI points for StochRSI window: have %d need %d", len(rsiSeries), stochPeriod)
	}

	// --- Step 2: Compute raw StochRSI series ---
	stochRSISeries := make([]float64, 0, len(rsiSeries)-stochPeriod+1)
	for i := stochPeriod; i <= len(rsiSeries); i++ {
		window := rsiSeries[i-stochPeriod : i]
		minRSI, maxRSI := window[0], window[0]
		for _, v := range window {
			if v < minRSI {
				minRSI = v
			}
			if v > maxRSI {
				maxRSI = v
			}
		}
		if maxRSI == minRSI {
			stochRSISeries = append(stochRSISeries, 0)
			continue
		}
		value := (rsiSeries[i-1] - minRSI) / (maxRSI - minRSI)
		value = math.Max(0, math.Min(1, value))
		stochRSISeries = append(stochRSISeries, value)
	}

	// --- Step 3: Apply 3-period SMA smoothing (TradingView-style) ---
	smoothPeriod := 3
	if len(stochRSISeries) < smoothPeriod {
		return stochRSISeries[len(stochRSISeries)-1], nil
	}

	sum := 0.0
	for _, v := range stochRSISeries[len(stochRSISeries)-smoothPeriod:] {
		sum += v
	}
	smoothed := sum / float64(smoothPeriod)

	return math.Max(0, math.Min(1, smoothed)), nil
}

// SMA computes simple moving average
func SMA(data []float64) float64 {
	if len(data) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range data {
		sum += v
	}
	return sum / float64(len(data))
}

// EMA calculates the Exponential Moving Average for the last value in data.
func EMA(data []float64, period int) float64 {
	if len(data) < period {
		return SMA(data)
	}

	k := 2.0 / (float64(period) + 1.0)
	ema := SMA(data[:period]) // start with SMA as seed

	for i := period; i < len(data); i++ {
		ema = (data[i]-ema)*k + ema
	}
	return ema
}

// MACD calculates the MACD line, signal line, and histogram.
func MACD(closes []float64, shortPeriod, longPeriod, signalPeriod int) (float64, float64, float64) {
	shortEMA := make([]float64, 0, len(closes))
	longEMA := make([]float64, 0, len(closes))
	for i := range closes {
		sub := closes[:i+1]
		shortEMA = append(shortEMA, EMA(sub, shortPeriod))
		longEMA = append(longEMA, EMA(sub, longPeriod))
	}

	macdLine := make([]float64, len(closes))
	for i := range closes {
		macdLine[i] = shortEMA[i] - longEMA[i]
	}

	signalLine := EMA(macdLine, signalPeriod)
	histogram := macdLine[len(macdLine)-1] - signalLine

	return macdLine[len(macdLine)-1], signalLine, histogram
}

// PredictNextPrice analyzes close prices using EMA, MACD, and StochRSI.
func PredictNextPrice(closes []float64) (*PredictResult, error) {
	if len(closes) < 60 { // ensure enough data for MACD & StochRSI
		return nil, errors.New("not enough close prices to calculate indicators")
	}

	currentPrice := closes[len(closes)-1]
	prevPrice := closes[len(closes)-2]

	shortEMA := EMA(closes, 7)
	longEMA := EMA(closes, 20)

	macdLine, signalLine, hist := MACD(closes, 12, 26, 9)

	stochRSI, err := CalculateStochRSI(closes, 14, 14)
	if err != nil {
		return nil, fmt.Errorf("stochRSI calc failed: %w", err)
	}

	momentum := (currentPrice - prevPrice) / prevPrice * 100
	predicted := currentPrice * (1 + momentum/200)
	changePct := (predicted - currentPrice) / currentPrice * 100

	// --- Signal logic ---
	signal := "HOLD"

	// BUY: trend up + MACD bullish + StochRSI < 0.2 (oversold)
	if shortEMA > longEMA && macdLine > signalLine && stochRSI < 0.2 {
		signal = "BUY"
	}

	// SELL: trend down + MACD bearish + StochRSI > 0.8 (overbought)
	if shortEMA < longEMA && macdLine < signalLine && stochRSI > 0.8 {
		signal = "SELL"
	}

	return &PredictResult{
		NextPrice: math.Round(predicted*100) / 100,
		ChangePct: math.Round(changePct*100) / 100,
		Signal:    signal,
		MACD:      math.Round(macdLine*1000) / 1000,
		SignalMA:  math.Round(signalLine*1000) / 1000,
		Histogram: math.Round(hist*1000) / 1000,
		StochRSI:  math.Round(stochRSI*1000) / 1000,
	}, nil
}
