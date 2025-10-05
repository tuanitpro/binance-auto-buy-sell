package binance

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// HttpRequest is a helper for signed Binance API requests
type HttpRequest struct {
	APIKey    string
	SecretKey string
	BaseURL   string
	Client    *http.Client
}

// NewHttpRequest creates a new Binance HttpRequest helper
func NewHttpRequest(apiKey, secretKey string) *HttpRequest {
	return &HttpRequest{
		APIKey:    apiKey,
		SecretKey: secretKey,
		BaseURL:   "https://api.binance.com",
		Client:    &http.Client{Timeout: 10 * time.Second},
	}
}

// signPayload generates the HMAC SHA256 signature
func (b *HttpRequest) signPayload(query string) string {
	mac := hmac.New(sha256.New, []byte(b.SecretKey))
	mac.Write([]byte(query))
	return hex.EncodeToString(mac.Sum(nil))
}

// SignedRequest sends a signed request to Binance API
func (b *HttpRequest) SignedRequest(method, endpoint string, params map[string]string) ([]byte, error) {
	values := url.Values{}
	for k, v := range params {
		values.Add(k, v)
	}

	// add timestamp
	values.Add("timestamp", fmt.Sprintf("%d", time.Now().UnixMilli()))

	// sign
	query := values.Encode()
	signature := b.signPayload(query)
	values.Add("signature", signature)

	reqURL := fmt.Sprintf("%s%s?%s", b.BaseURL, endpoint, values.Encode())

	req, err := http.NewRequest(method, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("X-MBX-APIKEY", b.APIKey)

	resp, err := b.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call Binance API: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Binance API error (%d): %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// PublicRequest sends a public (non-signed) request to Binance
func (b *HttpRequest) PublicRequest(endpoint string, params map[string]string) ([]byte, error) {
	values := url.Values{}
	for k, v := range params {
		values.Add(k, v)
	}

	reqURL := fmt.Sprintf("%s%s?%s", b.BaseURL, endpoint, values.Encode())

	resp, err := b.Client.Get(reqURL)
	if err != nil {
		return nil, fmt.Errorf("failed to call Binance public API: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Binance public API error (%d): %s", resp.StatusCode, string(body))
	}

	return body, nil
}
