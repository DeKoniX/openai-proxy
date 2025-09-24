package models

import "time"

// Proxy describes a configured upstream forwarding rule.
type Proxy struct {
	ID                  int64     `json:"id"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	Name                string    `json:"name"`
	PathPrefix          string    `json:"path_prefix"`
	UpstreamURL         string    `json:"upstream_url"`
	UpstreamKey         string    `json:"upstream_key"`
	TokenPriceInputUSD  float64   `json:"token_price_input_usd"`
	TokenPriceOutputUSD float64   `json:"token_price_output_usd"`
	LastResetAt         time.Time `json:"last_reset_at"`
	UsageInputTokens    int64     `json:"usage_input_tokens"`
	UsageOutputTokens   int64     `json:"usage_output_tokens"`
	UsageInputCostUSD   float64   `json:"usage_input_cost_usd"`
	UsageOutputCostUSD  float64   `json:"usage_output_cost_usd"`
	UsageInputCostRUB   float64   `json:"usage_input_cost_rub"`
	UsageOutputCostRUB  float64   `json:"usage_output_cost_rub"`
}

// APILog captures a proxied request and response details stored in the database.
type APILog struct {
	ID              int64     `json:"id"`
	CreatedAt       time.Time `json:"created_at"`
	ProxyID         int64     `json:"proxy_id"`
	ProxyName       string    `json:"proxy_name"`
	ProxyPathPrefix string    `json:"proxy_path_prefix"`
	Method          string    `json:"method"`
	Path            string    `json:"path"`
	Query           string    `json:"query"`
	RequestHeaders  string    `json:"request_headers"`
	RequestBody     []byte    `json:"request_body"`
	ResponseStatus  int       `json:"response_status"`
	ResponseHeaders string    `json:"response_headers"`
	ResponseBody    []byte    `json:"response_body"`
	ErrorMessage    string    `json:"error_message"`
	RequestTokens   int       `json:"request_tokens"`
	ResponseTokens  int       `json:"response_tokens"`
	TotalTokens     int       `json:"total_tokens"`
	CostInputUSD    float64   `json:"cost_input_usd"`
	CostOutputUSD   float64   `json:"cost_output_usd"`
	CostInputRUB    float64   `json:"cost_input_rub"`
	CostOutputRUB   float64   `json:"cost_output_rub"`
	CostTotalUSD    float64   `json:"cost_total_usd"`
	CostTotalRUB    float64   `json:"cost_total_rub"`
}
