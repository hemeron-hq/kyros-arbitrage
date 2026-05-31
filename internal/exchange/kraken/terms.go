package kraken

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	stdjson "encoding/json"

	json "github.com/goccy/go-json"
	"github.com/govalues/decimal"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

func (p *Provider) TermsUnavailableMessage() string {
	if p.APIKey == "" || p.APISecret == "" {
		return "missing Kraken API credentials"
	}
	return ""
}

func (p *Provider) FetchFeeSchedule(ctx context.Context, binding exchange.Binding, now time.Time) (exchange.FeeSchedule, error) {
	return p.fetchTradeVolume(ctx, binding.RESTSymbol, now)
}

func (p *Provider) FetchAccount(ctx context.Context, now time.Time) (exchange.AccountSnapshot, error) {
	balances, err := p.fetchBalances(ctx, now)
	if err != nil {
		return exchange.AccountSnapshot{}, err
	}
	return exchange.AccountSnapshot{
		Exchange:  p.Exchange(),
		Balances:  normalizeKrakenBalances(balances),
		UpdatedAt: now,
		Message:   "authenticated Kraken wallet data",
	}, nil
}

func (p *Provider) FetchMarketConstraints(ctx context.Context, binding exchange.Binding) (exchange.MarketConstraints, error) {
	return p.fetchAssetPairRules(ctx, binding.RESTSymbol)
}

func (p *Provider) FetchTransferFees(_ context.Context, _ []string, _ time.Time) (exchange.TransferFees, error) {
	return exchange.TransferFees{}, nil
}

func (p *Provider) fetchTradeVolume(ctx context.Context, pair string, now time.Time) (exchange.FeeSchedule, error) {
	var response krakenTradeVolumeResponse
	values := url.Values{"pair": {pair}}
	if err := p.privatePOST(ctx, "/0/private/TradeVolume", values, now, &response); err != nil {
		return exchange.FeeSchedule{}, fmt.Errorf("kraken trade volume: %w", err)
	}
	if len(response.Errors) > 0 {
		return exchange.FeeSchedule{}, fmt.Errorf("kraken trade volume error: %s", strings.Join(response.Errors, "; "))
	}

	takerPercent, ok := firstKrakenFee(response.Result.Fees)
	if !ok {
		takerPercent = decimal.MustNew(40, 2)
	}
	makerPercent, ok := firstKrakenFee(response.Result.FeesMaker)
	if !ok {
		makerPercent = decimal.MustNew(25, 2)
	}
	takerRate, err := takerPercent.Quo(decimal.MustNew(100, 0))
	if err != nil {
		return exchange.FeeSchedule{}, err
	}
	makerRate, err := makerPercent.Quo(decimal.MustNew(100, 0))
	if err != nil {
		return exchange.FeeSchedule{}, err
	}
	return exchange.FeeSchedule{MakerRate: makerRate, TakerRate: takerRate}, nil
}

func (p *Provider) fetchBalances(ctx context.Context, now time.Time) (map[string]decimal.Decimal, error) {
	var response krakenBalanceResponse
	if err := p.privatePOST(ctx, "/0/private/BalanceEx", nil, now, &response); err != nil {
		return nil, fmt.Errorf("kraken balance: %w", err)
	}
	if len(response.Errors) > 0 {
		return nil, fmt.Errorf("kraken balance error: %s", strings.Join(response.Errors, "; "))
	}
	balances := make(map[string]decimal.Decimal, len(response.Result))
	for asset, raw := range response.Result {
		value, err := raw.BalanceDecimal()
		if err != nil || value.IsZero() {
			continue
		}
		balances[asset] = value
	}
	if len(balances) == 0 {
		return map[string]decimal.Decimal{}, nil
	}
	return balances, nil
}

func (p *Provider) fetchAssetPairRules(ctx context.Context, pair string) (exchange.MarketConstraints, error) {
	endpoint, err := url.Parse(strings.TrimRight(p.restBaseURL(), "/") + "/0/public/AssetPairs")
	if err != nil {
		return exchange.MarketConstraints{}, err
	}
	query := endpoint.Query()
	query.Set("pair", pair)
	query.Set("info", "info")
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return exchange.MarketConstraints{}, err
	}
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return exchange.MarketConstraints{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return exchange.MarketConstraints{}, fmt.Errorf("kraken AssetPairs status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var response krakenAssetPairsResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return exchange.MarketConstraints{}, err
	}
	if len(response.Errors) > 0 {
		return exchange.MarketConstraints{}, fmt.Errorf("kraken AssetPairs error: %s", strings.Join(response.Errors, "; "))
	}
	for _, assetPair := range response.Result {
		rules := exchange.MarketConstraints{}
		if assetPair.OrderMin != "" {
			if value, err := decimal.Parse(assetPair.OrderMin); err == nil {
				rules.MinBase = value
			}
		}
		if assetPair.CostMin != "" {
			if value, err := decimal.Parse(assetPair.CostMin); err == nil {
				rules.MinNotional = value
			}
		}
		return rules, nil
	}
	return exchange.MarketConstraints{}, fmt.Errorf("kraken AssetPairs returned no pair")
}

func (p *Provider) privatePOST(ctx context.Context, path string, values url.Values, _ time.Time, out any) error {
	if values == nil {
		values = url.Values{}
	}
	nonce := strconv.FormatInt(time.Now().UnixNano(), 10)
	values.Set("nonce", nonce)
	body := values.Encode()

	signature, err := p.krakenSignature(path, nonce, body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.restBaseURL(), "/")+path, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("API-Key", p.APIKey)
	req.Header.Set("API-Sign", signature)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return err
	}
	return nil
}

func (p *Provider) krakenSignature(path string, nonce string, body string) (string, error) {
	secret, err := base64.StdEncoding.DecodeString(p.APISecret)
	if err != nil {
		return "", fmt.Errorf("decode Kraken API secret: %w", err)
	}
	sha := sha256.Sum256([]byte(nonce + body))
	message := append([]byte(path), sha[:]...)
	mac := hmac.New(sha512.New, secret)
	_, _ = mac.Write(message)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

func firstKrakenFee(fees map[string]krakenFeeInfo) (decimal.Decimal, bool) {
	for _, fee := range fees {
		value, err := decimal.Parse(fee.Fee)
		if err == nil {
			return value, true
		}
	}
	return decimal.Zero, false
}

func normalizeKrakenBalances(raw map[string]decimal.Decimal) map[string]decimal.Decimal {
	balances := make(map[string]decimal.Decimal, len(raw))
	for asset, value := range raw {
		switch asset {
		case "XXBT", "XBT":
			balances["BTC"] = value
		case "ZUSD", "USD":
			balances["USD"] = value
		case "USDT", "ZUSDT":
			balances["USDT"] = value
		default:
			balances[asset] = value
		}
	}
	return balances
}

type krakenTradeVolumeResponse struct {
	Errors []string `json:"error"`
	Result struct {
		Fees      map[string]krakenFeeInfo `json:"fees"`
		FeesMaker map[string]krakenFeeInfo `json:"fees_maker"`
	} `json:"result"`
}

type krakenFeeInfo struct {
	Fee string `json:"fee"`
}

type krakenBalanceResponse struct {
	Errors []string                        `json:"error"`
	Result map[string]krakenBalancePayload `json:"result"`
}

type krakenBalancePayload struct {
	Raw stdjson.RawMessage
}

func (p *krakenBalancePayload) UnmarshalJSON(payload []byte) error {
	p.Raw = append(p.Raw[:0], payload...)
	return nil
}

func (p krakenBalancePayload) BalanceDecimal() (decimal.Decimal, error) {
	var text string
	if err := stdjson.Unmarshal(p.Raw, &text); err == nil {
		return decimal.Parse(text)
	}
	var object struct {
		Balance string `json:"balance"`
	}
	if err := stdjson.Unmarshal(p.Raw, &object); err != nil {
		return decimal.Zero, err
	}
	return decimal.Parse(object.Balance)
}

type krakenAssetPairsResponse struct {
	Errors []string                       `json:"error"`
	Result map[string]krakenAssetPairInfo `json:"result"`
}

type krakenAssetPairInfo struct {
	OrderMin string `json:"ordermin"`
	CostMin  string `json:"costmin"`
}
