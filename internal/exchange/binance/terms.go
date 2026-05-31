package binance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	json "github.com/goccy/go-json"
	"github.com/govalues/decimal"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

func (p *Provider) TermsUnavailableMessage() string {
	if p.APIKey == "" || p.APISecret == "" {
		return "missing Binance API credentials"
	}
	return ""
}

func (p *Provider) FetchFeeSchedule(ctx context.Context, binding exchange.Binding, now time.Time) (exchange.FeeSchedule, error) {
	return p.fetchCommission(ctx, binding.RESTSymbol, now)
}

func (p *Provider) FetchAccount(ctx context.Context, now time.Time) (exchange.AccountSnapshot, error) {
	balances, err := p.fetchAccountBalances(ctx, now)
	if err != nil {
		return exchange.AccountSnapshot{}, err
	}
	return exchange.AccountSnapshot{
		Exchange:  p.Exchange(),
		Balances:  balances,
		UpdatedAt: now,
		Message:   "authenticated Binance wallet data",
	}, nil
}

func (p *Provider) FetchMarketConstraints(ctx context.Context, binding exchange.Binding) (exchange.MarketConstraints, error) {
	return p.fetchMarketConstraints(ctx, binding.RESTSymbol)
}

func (p *Provider) FetchTransferFees(ctx context.Context, assets []string, now time.Time) (exchange.TransferFees, error) {
	return p.fetchWithdrawalFees(ctx, assets, now)
}

func (p *Provider) fetchCommission(ctx context.Context, symbol string, now time.Time) (exchange.FeeSchedule, error) {
	var response binanceCommissionResponse
	if err := p.signedGET(ctx, "/api/v3/account/commission", url.Values{"symbol": {symbol}}, now, &response); err != nil {
		return exchange.FeeSchedule{}, fmt.Errorf("binance commission: %w", err)
	}
	maker, err := decimal.Parse(response.StandardCommission.Maker)
	if err != nil || maker.IsZero() {
		maker, err = decimal.Parse(response.DiscountedCommission.Maker)
	}
	if err != nil {
		return exchange.FeeSchedule{}, fmt.Errorf("parse maker commission: %w", err)
	}
	taker, err := decimal.Parse(response.StandardCommission.Taker)
	if err != nil || taker.IsZero() {
		taker, err = decimal.Parse(response.DiscountedCommission.Taker)
	}
	if err != nil {
		return exchange.FeeSchedule{}, fmt.Errorf("parse taker commission: %w", err)
	}
	if taker.IsZero() {
		return exchange.FeeSchedule{}, fmt.Errorf("binance taker commission is zero")
	}
	if maker.IsZero() {
		return exchange.FeeSchedule{}, fmt.Errorf("binance maker commission is zero")
	}
	return exchange.FeeSchedule{MakerRate: maker, TakerRate: taker}, nil
}

func (p *Provider) fetchAccountBalances(ctx context.Context, now time.Time) (map[string]decimal.Decimal, error) {
	var response binanceAccountResponse
	if err := p.signedGET(ctx, "/api/v3/account", nil, now, &response); err != nil {
		return nil, fmt.Errorf("binance account: %w", err)
	}
	balances := make(map[string]decimal.Decimal, len(response.Balances))
	parsedAny := false
	for _, balance := range response.Balances {
		free, err := decimal.Parse(balance.Free)
		if err != nil {
			continue
		}
		parsedAny = true
		locked, err := decimal.Parse(balance.Locked)
		if err != nil {
			locked = decimal.Zero
		}
		total, err := free.Add(locked)
		if err != nil {
			total = free
		}
		if !total.IsZero() {
			balances[balance.Asset] = total
		}
	}
	if !parsedAny {
		return nil, fmt.Errorf("binance account returned no parseable balances")
	}
	return balances, nil
}

func (p *Provider) fetchMarketConstraints(ctx context.Context, symbol string) (exchange.MarketConstraints, error) {
	endpoint, err := url.Parse(strings.TrimRight(p.restBaseURL(), "/") + "/api/v3/exchangeInfo")
	if err != nil {
		return exchange.MarketConstraints{}, err
	}
	query := endpoint.Query()
	query.Set("symbol", symbol)
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
		return exchange.MarketConstraints{}, fmt.Errorf("binance exchangeInfo status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var response binanceExchangeInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return exchange.MarketConstraints{}, err
	}
	if len(response.Symbols) == 0 {
		return exchange.MarketConstraints{}, fmt.Errorf("binance exchangeInfo returned no symbols")
	}
	return parseBinanceRules(response.Symbols[0].Filters), nil
}

func (p *Provider) fetchWithdrawalFees(ctx context.Context, assets []string, now time.Time) (exchange.TransferFees, error) {
	var response []binanceCoinInfo
	if err := p.signedGET(ctx, "/sapi/v1/capital/config/getall", nil, now, &response); err != nil {
		return nil, err
	}

	want := make(map[string]struct{}, len(assets))
	for _, asset := range assets {
		want[asset] = struct{}{}
	}
	fees := make(exchange.TransferFees, len(assets))
	for _, coin := range response {
		if _, ok := want[coin.Coin]; !ok {
			continue
		}
		fee, ok := lowestEnabledWithdrawalFee(coin.NetworkList)
		if ok {
			fees[coin.Coin] = fee
		}
	}
	return fees, nil
}

func (p *Provider) signedGET(ctx context.Context, path string, values url.Values, now time.Time, out any) error {
	if values == nil {
		values = url.Values{}
	}
	values.Set("timestamp", fmt.Sprintf("%d", now.UnixMilli()))
	values.Set("recvWindow", "5000")
	payload := values.Encode()
	signature := hmac.New(sha256.New, []byte(p.APISecret))
	_, _ = signature.Write([]byte(payload))
	values.Set("signature", hex.EncodeToString(signature.Sum(nil)))

	endpoint := strings.TrimRight(p.signedRestBaseURL(), "/") + path + "?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-MBX-APIKEY", p.APIKey)

	resp, err := p.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return err
	}
	return nil
}

func parseBinanceRules(filters []binanceFilter) exchange.MarketConstraints {
	rules := exchange.MarketConstraints{}
	for _, filter := range filters {
		switch filter.FilterType {
		case "PRICE_FILTER":
			if value, err := decimal.Parse(filter.TickSize); err == nil {
				rules.TickSize = value
			}
		case "LOT_SIZE":
			if value, err := decimal.Parse(filter.MinQty); err == nil {
				rules.MinBase = value
			}
			if value, err := decimal.Parse(filter.StepSize); err == nil {
				rules.StepSize = value
			}
		case "NOTIONAL", "MIN_NOTIONAL":
			if value, err := decimal.Parse(filter.MinNotional); err == nil {
				rules.MinNotional = value
			}
		}
	}
	return rules
}

func lowestEnabledWithdrawalFee(networks []binanceNetworkInfo) (decimal.Decimal, bool) {
	var best decimal.Decimal
	found := false
	for _, network := range networks {
		if !network.WithdrawEnable {
			continue
		}
		fee, err := decimal.Parse(network.WithdrawFee)
		if err != nil {
			continue
		}
		if !found || fee.Cmp(best) < 0 {
			best = fee
			found = true
		}
	}
	return best, found
}

type binanceCommissionResponse struct {
	StandardCommission struct {
		Maker string `json:"maker"`
		Taker string `json:"taker"`
	} `json:"standardCommission"`
	DiscountedCommission struct {
		Maker string `json:"maker"`
		Taker string `json:"taker"`
	} `json:"discountedCommission"`
}

type binanceAccountResponse struct {
	Balances []struct {
		Asset  string `json:"asset"`
		Free   string `json:"free"`
		Locked string `json:"locked"`
	} `json:"balances"`
}

type binanceExchangeInfoResponse struct {
	Symbols []struct {
		Filters []binanceFilter `json:"filters"`
	} `json:"symbols"`
}

type binanceFilter struct {
	FilterType  string `json:"filterType"`
	MinQty      string `json:"minQty"`
	StepSize    string `json:"stepSize"`
	TickSize    string `json:"tickSize"`
	MinNotional string `json:"minNotional"`
}

type binanceCoinInfo struct {
	Coin        string               `json:"coin"`
	NetworkList []binanceNetworkInfo `json:"networkList"`
}

type binanceNetworkInfo struct {
	WithdrawEnable bool   `json:"withdrawEnable"`
	WithdrawFee    string `json:"withdrawFee"`
}
