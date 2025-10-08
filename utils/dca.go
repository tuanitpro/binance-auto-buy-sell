package utils

import "fmt"

// DCAResult holds each DCA target summary
type DCAResult struct {
	TargetAvg float64
	BuyQty    float64
	NewTotal  float64
	USDTSpent float64
	DropPct   float64
}

// CalculateDCA computes how much to buy to reduce loss by different targets:
//   - 30% reduction
//   - 50% reduction
//   - 80% reduction
//   - 0% (break even)
func CalculateDCA(symbol string, currentPrice float64, currentQty, buyPrice float64) ([]DCAResult, error) {
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
