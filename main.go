package main

import (
	"errors"
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
func checkSignal(symbol string, change float64) (*utils.PredictResult, error) {
	klines, err := api.GetKlines(symbol, interval, 200)
	if err != nil {
		log.Printf("GetKlines failed: %w", err)
		return nil, err
	}
	if len(klines) < 29 {
		log.Printf("not enough klines for RSI: have=%d", len(klines))
		return nil, errors.New("not enough klines for RSI")
	}

	// collect closes in chronological order
	closes := make([]float64, len(klines))
	for i := range klines {
		closes[i] = klines[i].Close
	}

	prediction, err := utils.PredictNextPrice(closes)
	if err != nil {
		fmt.Println("❌ Error:", err)
		return nil, err
	}

	// if prediction.Signal == "BUY" && change <= -percentThresholdBuy {
	// 	prediction.Signal = "BUY"
	// } else if prediction.Signal == "SELL" && change >= percentThresholdSell {
	// 	prediction.Signal = "SELL"
	// }

	return prediction, nil
}

func autoTrade(balance binance.AccountBalance) string {
	msg := ""
	price, err := api.GetPrice(balance.Symbol)
	if err != nil {
		log.Println("Price error:", err)
		return msg
	}

	currentValueUSDT := price * balance.Free
	pnlUSDT := currentValueUSDT - balance.TotalUSDT
	profitOrLoss := fmt.Sprintf("Loss: %.2f USDT", pnlUSDT)
	if pnlUSDT > 0 {
		profitOrLoss = fmt.Sprintf("Profit: %.2f USDT", pnlUSDT)
	}
	change := (price - balance.AveragePrice) / balance.AveragePrice * 100

	fmt.Printf("[%s] Qty: %.8f | Average: %.8f | Cost: %.8f | Current: %.8f | Total: %.8f | PnL: %.8f (%.2f%%)\n",
		balance.Symbol, balance.Free, balance.AveragePrice, balance.CostPrice, price, balance.TotalUSDT, pnlUSDT, change)

	if change > -percentThreshold && change < percentThreshold {
		return msg // no significant change, skip
	}

	prediction, err := checkSignal(balance.Symbol, change)
	if err != nil {
		fmt.Println("❌ Error:", err)
		return msg
	}

	msg += fmt.Sprintf("🚀🚀🚀 *Auto-Trade for: #%s * \nPnL: %.2f%% (%.8f → %.8f)\n%s\nSignal: *%s* \nQuantity: %.8f \nAverage Price: %.8f \nCurrent Price: %.8f \nNext Price: %.8f (%+.2f%%)",
		balance.Symbol, change, balance.AveragePrice, price, profitOrLoss, prediction.Signal, balance.Free, balance.AveragePrice, price, prediction.NextPrice, prediction.ChangePct)
	if change <= -percentThreshold {
		results, _ := utils.CalculateDCA(balance.Symbol, price, balance.Free, balance.AveragePrice)
		fmt.Printf("📊 DCA Strategy for %s\n", balance.Symbol)

		msg += fmt.Sprintf("\n\n📊 DCA Strategy for #%s\n", balance.Symbol)
		for _, r := range results {
			fmt.Printf("🎯 Target Avg: %.2f USDT |  Drop: %.2f%% | Buy: %.1f | Total: %.1f | Cost: %.2f USDT\n",
				r.TargetAvg, r.DropPct, r.BuyQty, r.NewTotal, r.USDTSpent)

			msg += fmt.Sprintf("🎯 Target Avg: %.2f USDT | Buy: %.1f | Total: %.1f | Cost: %.2f USDT\n", r.TargetAvg, r.BuyQty, r.NewTotal, r.USDTSpent)
		}
	}

	if prediction.Signal == "SELL" && balance.Free >= 10 && change > percentThresholdSell {
		if err := api.PlaceOrder(balance.Symbol, "SELL", 10); err != nil {
			log.Printf("Sell order error #%s: %v\n", balance.Symbol, err)
			return msg
		}
		msg += "\n\nPartial Take-Profit: Sold 10 units."
	}

	if prediction.Signal == "BUY" && change <= -percentThresholdBuy {
		if err := api.PlaceOrder(balance.Symbol, "BUY", 10); err != nil {
			log.Printf("Buy order error %s: %v\n", balance.Symbol, err)
			return msg
		}
		msg += "\n\nDCA Buy Order: Bought 10 units."
	}

	return msg
}

func cronJob() {
	balances, err := api.GetAccountBalances()
	if err != nil {
		log.Println("Error getting balances:", err)
		return
	}

	log.Println("📊 Checking Account Balances:")

	for _, balance := range balances {
		// if we couldn't compute buy price from trade history, skip
		if balance.AveragePrice <= 0 {
			log.Printf("[%s] No AveragePrice from account history (Qty: %.8f). Skipping.\n", balance.Asset, balance.Total)
			continue
		}
		msg := autoTrade(balance)
		if msg != "" {
			if err := telegram.Send(msg); err != nil {
				log.Printf("Telegram send error: %v\n", err)
			}
			log.Printf("Telegram message sent for %s\n", balance.Symbol)
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
