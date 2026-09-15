// Package dashboard is a peer client for TokenWatch's own dashboard API
// (src/tokenwatch/dashboard_app.py), the same read-only JSON surface the web
// console in src/tokenwatch/dashboard/index.html consumes.
//
// Nothing here reimplements accounting, routing, caching or budgeting: every
// number is produced by the Python service and merely displayed. A Go user and
// a browser user therefore see identical figures.
//
// Numeric note: the aggregate endpoints (recent, timeseries, cost/*, routing,
// the A/B report) are served straight from Oracle rows, so a NUMBER column
// arrives as a JSON float even when it is conceptually a count -- SUM() of an
// INTEGER column is a NUMBER, and FastAPI encodes Decimal as float. Every such
// field is therefore decoded as float64 and formatted for display, so a payload
// written as 120 and one written as 120.0 both decode. Fields that come from a
// Pydantic model (ids, booleans, split percentages) keep their real types.
package dashboard

// UsageStats is GET /api/stats, i.e. UsageStats.model_dump() from models.py.
//
// Models is keyed by model_used and the inner object is a plain dict built in
// db.get_stats, not a model, so its members are raw Oracle numbers.
type UsageStats struct {
	TotalRequests            float64                `json:"total_requests"`
	TotalInputTokens         float64                `json:"total_input_tokens"`
	TotalOutputTokens        float64                `json:"total_output_tokens"`
	TotalCacheCreationTokens float64                `json:"total_cache_creation_tokens"`
	TotalCacheReadTokens     float64                `json:"total_cache_read_tokens"`
	TotalEstimatedCost       float64                `json:"total_estimated_cost"`
	TotalCacheHits           float64                `json:"total_cache_hits"`
	TotalCacheSavings        float64                `json:"total_cache_savings"`
	Models                   map[string]ModelTotals `json:"models"`
}

// ModelTotals is one entry of UsageStats.Models.
type ModelTotals struct {
	Requests     float64 `json:"requests"`
	InputTokens  float64 `json:"input_tokens"`
	OutputTokens float64 `json:"output_tokens"`
	Cost         float64 `json:"cost"`
}

// RequestRow is one row of GET /api/recent (db.get_recent, raw Oracle columns).
//
// CacheHit, ABTestID and RoutingRuleID are numbers rather than bools and
// pointers-to-int because they come from SQL columns, not a Pydantic model: a
// NUMBER(1) flag is encoded as 1.0 and a NULL join key as null.
type RequestRow struct {
	ID                  float64 `json:"id"`
	RequestID           string  `json:"request_id"`
	APIType             string  `json:"api_type"`
	ModelRequested      string  `json:"model_requested"`
	ModelUsed           string  `json:"model_used"`
	InputTokens         float64 `json:"input_tokens"`
	OutputTokens        float64 `json:"output_tokens"`
	CacheCreationTokens float64 `json:"cache_creation_tokens"`
	CacheReadTokens     float64 `json:"cache_read_tokens"`
	LatencyMS           float64 `json:"latency_ms"`
	StatusCode          float64 `json:"status_code"`
	SourceApp           string  `json:"source_app"`
	SessionID           string  `json:"session_id"`
	FeatureTag          string  `json:"feature_tag"`
	EstimatedCost       float64 `json:"estimated_cost"`
	CacheHit            float64 `json:"cache_hit"`
	ABTestID            float64 `json:"ab_test_id"`
	RoutingRuleID       float64 `json:"routing_rule_id"`
	CreatedAt           string  `json:"created_at"`
}

// WasCached reports whether this request was served from the semantic cache.
//
// The column is a NUMBER(1) flag, so it arrives as a number and cannot be a
// bool field without an unmarshal failure on 1.0.
func (r RequestRow) WasCached() bool { return r.CacheHit > 0 }

// TimeseriesBucket is one row of GET /api/timeseries.
type TimeseriesBucket struct {
	Bucket       string  `json:"bucket"`
	InputTokens  float64 `json:"input_tokens"`
	OutputTokens float64 `json:"output_tokens"`
	Requests     float64 `json:"requests"`
	Cost         float64 `json:"cost"`
	CacheHits    float64 `json:"cache_hits"`
}

// CostByTag is one row of GET /api/cost/by-tag.
type CostByTag struct {
	Tag          string  `json:"tag"`
	Requests     float64 `json:"requests"`
	TotalCost    float64 `json:"total_cost"`
	AvgCost      float64 `json:"avg_cost"`
	InputTokens  float64 `json:"input_tokens"`
	OutputTokens float64 `json:"output_tokens"`
}

// CostByApp is one row of GET /api/cost/by-app.
type CostByApp struct {
	App       string  `json:"app"`
	Requests  float64 `json:"requests"`
	TotalCost float64 `json:"total_cost"`
	AvgCost   float64 `json:"avg_cost"`
}

// CostBySession is one row of GET /api/cost/by-session.
type CostBySession struct {
	SessionID        string  `json:"session_id"`
	Turns            float64 `json:"turns"`
	ConversationCost float64 `json:"conversation_cost"`
	Started          string  `json:"started"`
	Ended            string  `json:"ended"`
}

// CostForecast is GET /api/cost/forecast (computed in db.cost_forecast).
type CostForecast struct {
	Last7DaysTotal    float64 `json:"last_7_days_total"`
	DailyAvg          float64 `json:"daily_avg"`
	MonthlyProjection float64 `json:"monthly_projection"`
	ActiveDays        float64 `json:"active_days"`
}

// CacheStats is GET /api/cache/stats (db.cache_stats).
type CacheStats struct {
	Entries       float64 `json:"entries"`
	TotalHits     float64 `json:"total_hits"`
	ActiveEntries float64 `json:"active_entries"`
}

// BudgetStatus is one row of GET /api/budget/status. It is a BudgetRecord
// model_dump() plus two computed fields added in db.get_budget_status.
type BudgetStatus struct {
	ID             *int64  `json:"id"`
	Scope          string  `json:"scope"`
	ScopeValue     string  `json:"scope_value"`
	LimitAmount    float64 `json:"limit_amount"`
	Period         string  `json:"period"`
	ActionOnLimit  string  `json:"action_on_limit"`
	WebhookURL     string  `json:"webhook_url"`
	IsActive       bool    `json:"is_active"`
	CurrentSpend   float64 `json:"current_spend"`
	UtilizationPct float64 `json:"utilization_pct"`
}

// RoutingStat is one row of GET /api/routing/stats.
type RoutingStat struct {
	ID            float64 `json:"id"`
	RuleName      string  `json:"rule_name"`
	TargetModel   string  `json:"target_model"`
	TotalRequests float64 `json:"total_requests"`
	TotalCost     float64 `json:"total_cost"`
	AvgLatencyMS  float64 `json:"avg_latency_ms"`
}

// ABTest is one entry of GET /api/ab/list (ABTest.model_dump()).
type ABTest struct {
	ID       *int64  `json:"id"`
	TestName string  `json:"test_name"`
	ModelA   string  `json:"model_a"`
	ModelB   string  `json:"model_b"`
	SplitPct float64 `json:"split_pct"`
	Status   string  `json:"status"`
}

// ABVariant is one row of an A/B report.
type ABVariant struct {
	Variant         string  `json:"variant"`
	TotalRequests   float64 `json:"total_requests"`
	AvgLatencyMS    float64 `json:"avg_latency_ms"`
	AvgOutputTokens float64 `json:"avg_output_tokens"`
	TotalCost       float64 `json:"total_cost"`
	ErrorRate       float64 `json:"error_rate"`
}

// ABReport is GET /api/ab/report/{test_name}.
type ABReport struct {
	TestName string      `json:"test_name"`
	Variants []ABVariant `json:"variants"`
}

// Upstream is one entry of GET /api/upstreams (Upstream.model_dump()).
type Upstream struct {
	ID        *int64  `json:"id"`
	APIType   string  `json:"api_type"`
	BaseURL   string  `json:"base_url"`
	Priority  float64 `json:"priority"`
	IsHealthy bool    `json:"is_healthy"`
	FailCount float64 `json:"fail_count"`
}
