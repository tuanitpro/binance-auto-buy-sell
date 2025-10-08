package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
	"github.com/robfig/cron/v3"
	"main.go/binance"
	"main.go/notifier"
	"main.go/utils"
)

var (
	apiKey               string
	secretKey            string
	tgToken              string
	tgChatID             string
	interval             string  = "4h" // default interval for klines
	percentThreshold     float64 = 10.0 // percentage change threshold for alerts
	percentThresholdBuy  float64 = 10.0 // percentage change threshold buy for alerts
	percentThresholdSell float64 = 15.0 // percentage change threshold sell for alerts

	api      *binance.HttpRequest
	telegram *notifier.TelegramNotifier
)

// =================== Worker ======================
// Updated checkPrices to use s.BuyPrice (from account trade history).
func cronJob() {
	balances, err := api.GetAccountBalances()
	if err != nil {
		log.Println("Error getting balances:", err)
		return
	}

	fmt.Println("📊 Account Balances:")

	for _, s := range balances {
		// if we couldn't compute buy price from trade history, skip
		if s.BuyPrice <= 0 {
			log.Printf("[%s] No buyPrice from account history (Qty: %.8f). Skipping.\n", s.Asset, s.Total)
			continue
		}

		// 1. Get 4h klines (we need at least 15 closes for RSI14)
		klines, err := api.GetKlines(s.Symbol, interval, 200)
		if err != nil {
			log.Printf("GetKlines failed: %w", err)
			continue
		}
		if len(klines) < 29 {
			log.Printf("not enough klines for RSI: have=%d", len(klines))
			continue
		}

		// collect closes in chronological order
		closes := make([]float64, len(klines))
		for i := range klines {
			closes[i] = klines[i].Close
		}

		prediction, err := utils.PredictNextPrice(closes)
		if err != nil {
			fmt.Println("❌ Error:", err)
			continue
		}
		// fmt.Printf("[%s] | Predicted: %.2f (%+.2f%%) | Signal: %s | StochRSI: %.2f | MACD: %.3f | SignalMA: %.3f | Hist: %.3f\n",
		// 	s.Symbol,
		// 	prediction.NextPrice, prediction.ChangePct,
		// 	prediction.Signal, prediction.StochRSI,
		// 	prediction.MACD, prediction.SignalMA, prediction.Histogram)

		price, err := api.GetPrice(s.Symbol)
		if err != nil {
			log.Println("Price error:", err)
			continue
		}

		change := (price - s.BuyPrice) / s.BuyPrice * 100
		investedUSDT := s.BuyPrice * s.Free
		currentValueUSDT := price * s.Free
		pnlUSDT := currentValueUSDT - investedUSDT
		log.Printf("[%s] Qty: %.8f | Buy Price: %.8f | Current: %.8f | Change: %.2f%% | PnL: %.8f | StochRSI: %.2f | MACD: %.3f  | Next Price: %.2f (%+.2f%%) | Signal: %s\n",
			s.Symbol, s.Free, s.BuyPrice, price, change, pnlUSDT, prediction.StochRSI, prediction.MACD, prediction.NextPrice, prediction.ChangePct, prediction.Signal)

		if change >= percentThreshold {
			msg := fmt.Sprintf("🚀🚀🚀 *Auto-Trade for: #%s * \nPnL: +%.2f%% (%.8f → %.8f)\nProfit: +%.8f (USDT)\nSignal: *%s* \nStochRSI: %.2f \nMACD: %.3f \nQuantity: %.8f \nBuy Price: %.8f \nCurrent Price: %.8f \nNext Price: %.8f (%+.2f%%)",
				s.Symbol, change, s.BuyPrice, price, pnlUSDT, prediction.Signal, prediction.StochRSI, prediction.MACD, s.Free, s.BuyPrice, price, prediction.NextPrice, prediction.ChangePct)

			if change >= (percentThresholdSell) && s.Free >= 10 && prediction.Signal == "SELL" {
				if err := api.PlaceOrder(s.Symbol, "SELL", 10); err != nil {
					log.Printf("Sell order error #%s: %v\n", s.Symbol, err)
					continue
				}
				msg += "\n\nPartial Take-Profit: Sold 10 units."
			}

			if err := telegram.Send(msg); err != nil {
				log.Printf("Telegram send error: %v\n", err)
			}
		} else if change <= -(percentThreshold) {
			msg := fmt.Sprintf("🔻🔻🔻 *Auto-Trade for : #%s * \nPnL: %.2f%% (%.8f → %.8f)\nLoss: %.8f (USDT)\nSignal: *%s* \nStochRSI: %.2f \nMACD: %.3f \nQuantity: %.8f \nBuy Price: %.8f \nCurrent Price: %.8f \nNext Price: %.8f (%+.2f%%)",
				s.Symbol, change, s.BuyPrice, price, pnlUSDT, prediction.Signal, prediction.StochRSI, prediction.MACD, s.Free, s.BuyPrice, price, prediction.NextPrice, prediction.ChangePct)

			results, _ := utils.CalculateDCA(s.Symbol, price, s.Free, s.BuyPrice)
			fmt.Printf("📊 DCA Strategy for %s\n", s.Symbol)

			msg += fmt.Sprintf("\n\n📊 DCA Strategy for #%s\n", s.Symbol)
			for _, r := range results {
				fmt.Printf("🎯 Target Avg: %.2f USDT |  Drop: %.2f%% | Buy: %.1f | Total: %.1f | Cost: %.2f USDT\n",
					r.TargetAvg, r.DropPct, r.BuyQty, r.NewTotal, r.USDTSpent)

				msg += fmt.Sprintf("🎯 Target Avg: %.2f USDT | Buy: %.1f | Total: %.1f | Cost: %.2f USDT\n", r.TargetAvg, r.BuyQty, r.NewTotal, r.USDTSpent)
			}

			if change <= -(percentThresholdBuy) && prediction.Signal == "BUY" {
				// if err := api.PlaceOrder(s.Symbol, "BUY", 10); err != nil {
				// 	log.Printf("Buy order error %s: %v\n", s.Symbol, err)
				// 	continue
				// }
				msg += "\n\nDCA Buy Order: Bought 10 units."
			}
			if err := telegram.Send(msg); err != nil {
				log.Printf("Telegram send error: %v\n", err)
			}
			log.Printf("Telegram message sent for %s\n", s.Symbol)
		}
	}
}

// =================== MAIN ======================
func main() {
	err := godotenv.Load()
	if err != nil {
		fmt.Println("⚠️ Warning: .env file not found, using system environment variables")
	}

	apiKey = os.Getenv("BINANCE_API_KEY")
	secretKey = os.Getenv("BINANCE_SECRET_KEY")
	tgToken = os.Getenv("TELEGRAM_TOKEN")
	tgChatID = os.Getenv("TELEGRAM_CHAT_ID")
	interval = os.Getenv("INTERVAL")

	if apiKey == "" || secretKey == "" || tgToken == "" || tgChatID == "" {
		log.Fatal("Missing API keys or Telegram config in .env")
	}

	var percentThresholdString = os.Getenv("PERCENT_THRESHOLD")

	if percentThresholdString != "" {
		if v, err := strconv.ParseFloat(percentThresholdString, 64); err == nil {
			percentThreshold = v
		} else {
			log.Printf("Warning: invalid PERCENT_THRESHOLD: %v. Using default %.2f\n", err, percentThreshold)
		}
	}

	var percentThresholdBuyString = os.Getenv("PERCENT_THRESHOLD_BUY")
	if percentThresholdBuyString != "" {
		if v, err := strconv.ParseFloat(percentThresholdBuyString, 64); err == nil {
			percentThresholdBuy = v
		} else {
			log.Printf("Warning: invalid PERCENT_THRESHOLD_BUY: %v. Using default %.2f\n", err, percentThresholdBuy)
		}
	}

	var percentThresholdSellString = os.Getenv("PERCENT_THRESHOLD_SELL")
	if percentThresholdSellString != "" {
		if v, err := strconv.ParseFloat(percentThresholdSellString, 64); err == nil {
			percentThresholdSell = v
		} else {
			log.Printf("Warning: invalid PERCENT_THRESHOLD_SELL: %v. Using default %.2f\n", err, percentThresholdSell)
		}
	}

	api = binance.NewHttpRequest(apiKey, secretKey)
	telegram = notifier.NewTelegramNotifier(tgToken, tgChatID)

	// --- Add flag ---
	runNow := flag.Bool("now", false, "Run the job immediately without waiting for schedule")
	flag.Parse()

	if *runNow {
		fmt.Println("🚀 Running job immediately (--now)")
		cronJob() // run once immediately
		return
	}

	// --- default: cron schedule ---
	c := cron.New()
	// run every 5 minutes
	_, err = c.AddFunc("@every 5m", cronJob)
	if err != nil {
		fmt.Println("❌ Cannot schedule job:", err)
		os.Exit(1)
	}
	c.Start()

	log.Println("Bot started. Checking every 5 minutes...")
	select {} // block forever
}
