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
		log.Printf("[%s] Buy: %.8f | Current: %.8f | Qty: %.8f | Change: %.2f%%\n",
			s.Symbol, s.BuyPrice, price, s.Free, change)

		if change >= percentThreshold {
			msg := fmt.Sprintf("🚀 #%s up +%.2f%% (%.8f → %.8f). Quantity %.8f. Signal: Sell 🎯",
				s.Symbol, change, s.BuyPrice, price, s.Free)
			if err := telegram.Send(msg); err != nil {
				log.Printf("Telegram send error: %v\n", err)
			}
			if err := api.PlaceOrder(s.Symbol, "SELL", s.Free); err != nil {
				log.Printf("Sell order error %s: %v\n", s.Symbol, err)
			}
		} else if change <= -percentThreshold {
			msg := fmt.Sprintf("🔻 #%s down %.2f%% (%.8f → %.8f). Buying %.8f. Signal: Buy ✅",
				s.Symbol, change, s.BuyPrice, price, s.Free)
			if err := telegram.Send(msg); err != nil {
				log.Printf("Telegram send error: %v\n", err)
			}
			if err := api.PlaceOrder(s.Symbol, "BUY", s.Free); err != nil {
				log.Printf("Buy order error %s: %v\n", s.Symbol, err)
			}
		}
	}
}

// =================== MAIN ======================
func main() {
	if err := godotenv.Load(); err != nil {
		log.Fatal("Error loading .env file")
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
	c.AddFunc("@every 5m", cronJob)
	c.Start()

	log.Println("Bot started. Checking every 5 minutes...")
	select {} // block forever
}
