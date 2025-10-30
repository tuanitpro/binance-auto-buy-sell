package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"context"
	"os/signal"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
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
	minQuantity          float64 = 5.0  // minimum quantity to trade

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
	// Fetch daily high (1D interval)
	dayKlines, err := api.GetKlines(symbol, "1d", 1)
	if err != nil {
		log.Printf("GetKlines 1d failed: %v", err)
	} else if len(dayKlines) > 0 {
		prediction.DayHigh = dayKlines[0].High
		prediction.DayLow = dayKlines[0].Low
	}

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

	fmt.Printf("[%s] Qty: %.8f | Entry Price: %.8f | Average Price: %.8f | Current: %.8f | Total: %.8f | PnL: %.8f (%.2f%%)\n",
		balance.Symbol,
		balance.Free,
		balance.CostPrice,
		balance.AveragePrice,
		price,
		balance.TotalUSDT,
		pnlUSDT,
		change)

	if change > -percentThreshold && change < percentThreshold {
		return msg // no significant change, skip
	}

	prediction, err := checkSignal(balance.Symbol, change)
	if err != nil {
		fmt.Println("❌ Error:", err)
		return msg
	}

	msg += fmt.Sprintf("🚀🚀🚀 *Auto-Trade for: #%s * \nPnL: %.2f%% (%.8f → %.8f)\n%s\nSignal: *%s* \nQuantity: %.8f  \nEntry Price: %.8f \nAverage Price: %.8f \nCurrent Price: %.8f \nHigh:  %.8f - Low: %.8f  \nNext Price: %.8f (%+.2f%%)",
		balance.Symbol,
		change,
		balance.AveragePrice,
		price,
		profitOrLoss,
		prediction.Signal,
		balance.Free,
		balance.CostPrice,
		balance.AveragePrice,
		price,
		prediction.DayHigh,
		prediction.DayLow,
		prediction.NextPrice,
		prediction.ChangePct)
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

	if (change > percentThresholdSell && balance.Free >= minQuantity) &&
		(price >= prediction.DayHigh || prediction.Signal == "SELL") {
		if err := api.PlaceOrder(balance.Symbol, "SELL", minQuantity); err != nil {
			log.Printf("Sell order error #%s: %v\n", balance.Symbol, err)
			return msg
		}

		msg += fmt.Sprintf("\n\nPartial Take-Profit: Sold %.1f units.", minQuantity)
	}

	if prediction.Signal == "BUY" && change <= -percentThresholdBuy {
		if err := api.PlaceOrder(balance.Symbol, "BUY", minQuantity); err != nil {
			log.Printf("Buy order error %s: %v\n", balance.Symbol, err)
			return msg
		}
		msg += fmt.Sprintf("\n\nDCA Buy Order: Bought %.1f units.", minQuantity)
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

func summarizeBalances() {
	balances, err := api.GetAccountBalances()
	if err != nil {
		log.Println("Error getting balances:", err)
		return
	}

	log.Println("📊 Account Balances Summary:")
	msg := "📊 *Account Balances Summary:*\n\n"
	totalUSDT := 0.0
	totalCurrentUSDT := 0.0
	totalProfitLoss := 0.0
	for _, balance := range balances {
		price, err := api.GetPrice(balance.Symbol)
		if err != nil {
			log.Println("Price error:", err)

		}

		if balance.TotalUSDT > 0 {
			currentValueUSDT := price * balance.Total
			pnlUSDT := currentValueUSDT - balance.TotalUSDT
			change := (price - balance.AveragePrice) / balance.AveragePrice * 100
			totalUSDT += balance.TotalUSDT
			totalCurrentUSDT += currentValueUSDT
			totalProfitLoss += pnlUSDT
			fmt.Printf("[%s]: Qty: %.8f | Avg Price: %.8f | Current Price: %.8f | Total: %.2f USDT. | %.2f PNL: %.2f (%.2f%%)\n",
				balance.Symbol, balance.Total, balance.AveragePrice, price, balance.TotalUSDT, currentValueUSDT, pnlUSDT, change)
			msg += fmt.Sprintf("[#%s]: %.4f - Avg: %.4f - PnL: %.2f (%.2f%%)\n",
				balance.Symbol, balance.Free, balance.AveragePrice, pnlUSDT, change)
		}
	}
	totalChange := (totalCurrentUSDT - totalUSDT) / totalUSDT * 100
	fmt.Printf("Total Portfolio Value: %.2f USDT. Current: %.2f. PnL: %.2f (%.2f%%)\n", totalUSDT, totalCurrentUSDT, totalProfitLoss, totalChange)
	msg += fmt.Sprintf("\n*Total Portfolio Value:* %.2f USDT. \n*Current:* %.2f USDT. \n*PNL:* %.2f USDT (%.2f%%)", totalUSDT, totalCurrentUSDT, totalProfitLoss, totalChange)
	if err := telegram.Send(msg); err != nil {
		log.Printf("Telegram send error: %v\n", err)
	} else {
		log.Println("Telegram summary message sent.")
	}
}

func handler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil { // ignore non-Message updates
		return
	}

	log.Printf("Received message: %s", update.Message.Text)
	if update.Message.Text == "/start" {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Hello! I am your Binance Auto Trade Bot. I will help you monitor your account and execute trades based on predefined strategies. You can use the /help command to see available commands.",
		})
		return
	}
	if update.Message.Text == "/help" {
		helpText := "Available commands:\n" +
			"/start - Start the bot\n" +
			"/help - Show this help message\n" +
			"/balance - Show account balance summary\n" +
			"/run - Run the trading job immediately\n" +
			"/schedule - Schedule the trading job every 5 minutes\n" +
			"/stop - Stop the scheduled trading job\n" +
			"\nThe bot automatically checks your account every 5 minutes and summarizes balances daily at 12:30 PM."
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   helpText,
		})
		return
	}
	if update.Message.Text == "/balance" {
		summarizeBalances()
		return
	}
	if update.Message.Text == "/run" {
		cronJob()
		return
	}
	// Echo the received message back to the user

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   update.Message.Text,
	})
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

	var minQuantityString = os.Getenv("MIN_QUANTITY")
	if minQuantityString != "" {
		if v, err := strconv.ParseFloat(minQuantityString, 64); err == nil {
			minQuantity = v
		} else {
			log.Printf("Warning: invalid MIN_QUANTITY: %v. Using default %.2f\n", err, minQuantity)
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
		summarizeBalances()
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
	// run every day at 12:30 (12:30 PM) - summary of balances
	_, err = c.AddFunc("30 12 * * *", summarizeBalances)
	if err != nil {
		fmt.Println("❌ Cannot schedule job:", err)
		os.Exit(1)
	}

	c.Start()
	log.Println("Cron jobs scheduled.")
	// --- end cron ---

	// Setup Telegram bot
	// Create a context that is cancelled on SIGINT (Ctrl+C)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	opts := []bot.Option{
		bot.WithDefaultHandler(handler),
	}

	b, err := bot.New(tgToken, opts...)
	if err != nil {
		panic(err)
	}
	log.Println("Bot started.")
	b.Start(ctx)

	// select {} // block forever
}
