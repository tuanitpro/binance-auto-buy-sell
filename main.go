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
)

var (
	apiKey           string
	secretKey        string
	tgToken          string
	tgChatID         string
	percentThreshold float64 = 5.0 // percentage change threshold for buy/sell alerts

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

		price, err := api.GetPrice(s.Symbol)
		if err != nil {
			log.Println("Price error:", err)
			continue
		}

		change := (price - s.BuyPrice) / s.BuyPrice * 100
		investedUSDT := s.BuyPrice * s.Free
		currentValueUSDT := price * s.Free
		pnlUSDT := currentValueUSDT - investedUSDT
		log.Printf("[%s] Buy: %.8f | Current: %.8f | Qty: %.8f | Change: %.2f%% PnL: %.8f\n",
			s.Symbol, s.BuyPrice, price, s.Free, change, pnlUSDT)
		if change >= percentThreshold {
			msg := fmt.Sprintf("🚀🚀🚀 *Auto-Trade for: #%s * \nPnL: +%.2f%% (%.8f → %.8f)\nProfit: +%.8f (USDT)\nSignal: *SELL 🎯* \nQuantity: %.8f \nBuy Price: %.8f \nCurrent Price: %.8f",
				s.Symbol, change, s.BuyPrice, price, pnlUSDT, s.Free, s.BuyPrice, price)

			if change >= (percentThreshold*2) && s.Free >= 10 {
				if err := api.PlaceOrder(s.Symbol, "SELL", 10); err != nil {
					log.Printf("Sell order error #%s: %v\n", s.Symbol, err)
					continue
				}
				msg += "\n\nPartial Take-Profit: Sold 10 units."
			}

			if err := telegram.Send(msg); err != nil {
				log.Printf("Telegram send error: %v\n", err)
			}
		} else if change <= -(percentThreshold / 2) {
			msg := fmt.Sprintf("🔻🔻🔻 *Auto-Trade for : #%s * \nPnL: %.2f%% (%.8f → %.8f)\nLoss: %.8f (USDT)\nSignal: *BUY ✅* \nQuantity: %.8f \nBuy Price: %.8f \nCurrent Price: %.8f",
				s.Symbol, change, s.BuyPrice, price, pnlUSDT, s.Free, s.BuyPrice, price)

			if change <= -percentThreshold {
				if err := api.PlaceOrder(s.Symbol, "BUY", 10); err != nil {
					log.Printf("Buy order error %s: %v\n", s.Symbol, err)
					continue
				}
				msg += "\n\nDCA Buy Order: Bought 10 units."
			}
			if err := telegram.Send(msg); err != nil {
				log.Printf("Telegram send error: %v\n", err)
			}
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

	if apiKey == "" || secretKey == "" || tgToken == "" || tgChatID == "" {
		log.Fatal("Missing API keys or Telegram config in .env")
	}

	var percentThresholdString = os.Getenv("PRICE_CHANGE_THRESHOLD")

	if percentThresholdString != "" {
		if v, err := strconv.ParseFloat(percentThresholdString, 64); err == nil {
			percentThreshold = v
		} else {
			log.Printf("Warning: invalid PRICE_CHANGE_THRESHOLD: %v. Using default %.2f\n", err, percentThreshold)
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
