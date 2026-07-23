package quota

import (
	"regexp"
	"sort"
	"strings"
)

// reasoningrouting.go — Reasoning routing decision engine.
// Port of src/lib/reasoningRouting/policy.ts + input.ts
//
// The engine is STATELESS: all state is passed as arguments. No shared mutable
// state, no locks. Injectable seams (ComboResolver, CapabilityResolver) are the
// caller's responsibility for concurrency safety.

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type ReasoningEffort string

const (
	EffortNone   ReasoningEffort = "none"
	EffortLow    ReasoningEffort = "low"
	EffortMedium ReasoningEffort = "medium"
	EffortHigh   ReasoningEffort = "high"
	EffortXHigh  ReasoningEffort = "xhigh"
	EffortMax    ReasoningEffort = "max"
	EffortUltra  ReasoningEffort = "ultra"
)

var validEfforts = map[ReasoningEffort]bool{
	EffortNone: true, EffortLow: true, EffortMedium: true, EffortHigh: true,
	EffortXHigh: true, EffortMax: true, EffortUltra: true,
}

// ReasoningSourceEffort is "any" | "missing" | ReasoningEffort.
type ReasoningSourceEffort string

type RuleScope string

const (
	ScopeGlobal     RuleScope = "global"
	ScopeAPIKey     RuleScope = "apiKey"
	ScopeCombo      RuleScope = "combo"
	ScopeModel      RuleScope = "model"
	ScopeConnection RuleScope = "connection"
)

var scopeRank = map[RuleScope]int{
	ScopeAPIKey: 4, ScopeCombo: 3, ScopeModel: 2, ScopeGlobal: 1, ScopeConnection: 0,
}

type TargetKind string

const (
	TargetKeep  TargetKind = "keep"
	TargetModel TargetKind = "model"
	TargetCombo TargetKind = "combo"
)

type EffortMode string

const (
	EffortInherit EffortMode = "inherit"
	EffortDefault EffortMode = "default"
	EffortForce   EffortMode = "force"
)

type BudgetAction string

const (
	BudgetPreserve BudgetAction = "preserve"
	BudgetRemove   BudgetAction = "remove"
	BudgetSet      BudgetAction = "set"
)

type TagMatchMode string

const (
	TagAny TagMatchMode = "any"
	TagAll TagMatchMode = "all"
)

// ReasoningRoutingRule mirrors the DB rule shape.
type ReasoningRoutingRule struct {
	ID           string
	Name         string
	Description  string
	Scope        RuleScope
	APIKeyID     *string
	ComboID      *string
	ConnectionID *string
	ModelPattern *string
	SourceEffort ReasoningSourceEffort // "any" | "missing" | effort
	RequestTags  []string
	TagMatchMode TagMatchMode
	EffortMode   EffortMode
	TargetEffort *ReasoningEffort
	TargetKind   TargetKind
	TargetModel  *string
	TargetComboID *string
	BudgetAction BudgetAction
	BudgetTokens *int
	Priority     int
	Enabled      bool
	CreatedAt    string // ISO timestamp for tie-breaking
}

// ExtractedReasoningIntent is the parsed reasoning intent from a request.
type ExtractedReasoningIntent struct {
	Model             string
	Effort            *ReasoningEffort
	SourceEffort      string // effort value | "signal" | "missing"
	HasReasoningSignal bool
	HasThinkingBudget bool
}

// Capability is the reasoning capability verdict for a model.
type Capability string

const (
	CapSupported   Capability = "supported"
	CapUnsupported Capability = "unsupported"
	CapUnknown     Capability = "unknown"
)

// ReasoningRuleDecision is the outcome of rule evaluation.
type ReasoningRuleDecision struct {
	Rule              ReasoningRoutingRule
	SourceModel       string
	SourceEffort      string
	TargetModel       string
	TargetCombo       map[string]any
	TargetEffort      *ReasoningEffort
	Capability        Capability
	RequiresReasoning bool
	Warnings          []string
}

// RoutingInput is the input to rule evaluation.
type RoutingInput struct {
	SourceModel       string
	SourceModelAliases []string
	SourceEffort      string // effort | "signal" | "missing"
	HasReasoningSignal bool
	HasThinkingBudget bool
	APIKeyID          *string
	ComboID           *string
	ConnectionID      *string
	RequestTags       []string
	ConnectionOnly    bool
	CapabilityModel   *string
}

// CapabilityInfo is the resolved capability for a model.
type CapabilityInfo struct {
	SupportsThinking *bool // nil = unknown
}

// ComboResolver resolves a combo by ID (injectable DB seam).
type ComboResolver func(id string) (map[string]any, error)

// CapabilityResolver resolves model capabilities (injectable seam).
type CapabilityResolver func(model string) CapabilityInfo

// ---------------------------------------------------------------------------
// Effort parsing
// ---------------------------------------------------------------------------

func parseEffort(v any) *ReasoningEffort {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	e := ReasoningEffort(strings.ToLower(strings.TrimSpace(s)))
	if validEfforts[e] {
		return &e
	}
	return nil
}

func thinkingLevelEffort(v any) *ReasoningEffort {
	if b, ok := v.(bool); ok && !b {
		e := EffortNone
		return &e
	}
	s, ok := v.(string)
	if !ok {
		return nil
	}
	n := strings.ToLower(strings.TrimSpace(s))
	if n == "disabled" || n == "off" || n == "false" {
		e := EffortNone
		return &e
	}
	return parseEffort(n)
}

// ---------------------------------------------------------------------------
// Model suffix splitting
// ---------------------------------------------------------------------------

var claudeEffortSuffixes = []ReasoningEffort{
	EffortXHigh, EffortHigh, EffortMedium, EffortLow, EffortMax,
}

func splitClaudeEffortSuffix(model string) (base string, effort *ReasoningEffort) {
	lower := strings.ToLower(model)
	for _, level := range claudeEffortSuffixes {
		suffix := "-" + string(level)
		if strings.HasSuffix(lower, suffix) {
			b := model[:len(model)-len(suffix)]
			e := level
			return b, &e
		}
	}
	return model, nil
}

var codexSuffixCandidates = []ReasoningEffort{
	EffortUltra, EffortXHigh, EffortMedium, EffortHigh, EffortNone, EffortLow, EffortMax,
}

var (
	reGPTBase      = regexp.MustCompile(`^gpt-[\w.-]+$`)
	reCodexPrefix  = regexp.MustCompile(`^(?:codex|cx)/`)
	reGPT56SolTerraLuna = regexp.MustCompile(`^gpt-5\.6-(?:sol|terra|luna)$`)
	reGPT56SolTerra     = regexp.MustCompile(`^gpt-5\.6-(?:sol|terra)$`)
)

func supportsCodexSuffix(candidate ReasoningEffort, normalizedBase string) bool {
	switch candidate {
	case EffortMax:
		return reGPT56SolTerraLuna.MatchString(normalizedBase)
	case EffortUltra:
		return reGPT56SolTerra.MatchString(normalizedBase)
	default:
		return true
	}
}

func splitCodexEffortSuffix(model string) (base string, effort *ReasoningEffort, ok bool) {
	lower := strings.ToLower(model)
	for _, candidate := range codexSuffixCandidates {
		suffix := "-" + string(candidate)
		if !strings.HasSuffix(lower, suffix) {
			continue
		}
		baseModel := model[:len(model)-len(suffix)]
		normalizedBase := reCodexPrefix.ReplaceAllString(strings.ToLower(baseModel), "")
		if reGPTBase.MatchString(normalizedBase) && supportsCodexSuffix(candidate, normalizedBase) {
			e := candidate
			return baseModel, &e, true
		}
	}
	return model, nil, false
}

func splitGenericEffortSuffix(model string) (base string, effort *ReasoningEffort) {
	if strings.Contains(strings.ToLower(model), "claude") {
		b, e := splitClaudeEffortSuffix(model)
		if e != nil {
			return b, e
		}
	}
	if b, e, ok := splitCodexEffortSuffix(model); ok {
		return b, e
	}
	return model, nil
}

// ---------------------------------------------------------------------------
// Intent extraction
// ---------------------------------------------------------------------------

func asRecord(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func hasNumericField(m map[string]any, key string) bool {
	_, isFloat := m[key].(float64)
	_, isInt := m[key].(int)
	return isFloat || isInt
}

func firstDefinedEffort(body, reasoning, thinking, thinkingConfig map[string]any, suffixEffort *ReasoningEffort) *ReasoningEffort {
	output := asRecord(body["output_config"])
	// thinkingConfig.thinkingLevel ?? thinkingConfig.thinking_level
	var tcLevel any
	if v, ok := thinkingConfig["thinkingLevel"]; ok {
		tcLevel = v
	} else if v, ok := thinkingConfig["thinking_level"]; ok {
		tcLevel = v
	}

	candidates := []*ReasoningEffort{
		parseEffort(reasoning["effort"]),
		parseEffort(body["reasoning_effort"]),
		parseEffort(body["reasoningEffort"]),
		parseEffort(body["effort"]),
		parseEffort(output["effort"]),
		thinkingLevelEffort(body["thinkingLevel"]),
		thinkingLevelEffort(body["thinking_level"]),
		thinkingLevelEffort(tcLevel),
	}
	// body.thinking === false || thinking.type === "disabled" ? "none" : null
	if b, ok := body["thinking"].(bool); ok && !b {
		e := EffortNone
		candidates = append(candidates, &e)
	} else if t, ok := thinking["type"].(string); ok && t == "disabled" {
		e := EffortNone
		candidates = append(candidates, &e)
	} else {
		candidates = append(candidates, nil)
	}
	candidates = append(candidates, suffixEffort)

	for _, c := range candidates {
		if c != nil {
			return c
		}
	}
	return nil
}

func detectThinkingBudget(body, reasoning, thinking, thinkingConfig map[string]any) bool {
	checks := []struct {
		m   map[string]any
		key string
	}{
		{thinking, "budget_tokens"}, {thinking, "budgetTokens"},
		{reasoning, "budget_tokens"}, {reasoning, "budgetTokens"}, {reasoning, "max_tokens"},
		{body, "thinking_budget"}, {body, "thinkingBudget"},
		{thinkingConfig, "thinkingBudget"}, {thinkingConfig, "thinking_budget"},
	}
	for _, c := range checks {
		if hasNumericField(c.m, c.key) {
			return true
		}
	}
	return false
}

func hasKey(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

func hasReasoningSignal(body, reasoning, thinkingConfig map[string]any, explicitEffort *ReasoningEffort, hasBudget bool) bool {
	if explicitEffort != nil {
		return true
	}
	output := asRecord(body["output_config"])
	signalKeys := []struct {
		m   map[string]any
		key string
	}{
		{body, "thinking"}, {body, "thinkingLevel"}, {body, "thinking_level"},
		{reasoning, "effort"},
		{body, "reasoning_effort"}, {body, "reasoningEffort"}, {body, "effort"},
		{output, "effort"},
		{thinkingConfig, "thinkingLevel"}, {thinkingConfig, "thinking_level"},
	}
	for _, k := range signalKeys {
		if hasKey(k.m, k.key) {
			return true
		}
	}
	return hasBudget
}

// ExtractReasoningIntent parses the reasoning intent from a request model + body.
func ExtractReasoningIntent(modelInput any, bodyInput any) ExtractedReasoningIntent {
	body := asRecord(bodyInput)
	rawModel := ""
	if s, ok := modelInput.(string); ok {
		rawModel = strings.TrimSpace(s)
	}
	baseModel, suffixEffort := splitGenericEffortSuffix(rawModel)
	reasoning := asRecord(body["reasoning"])
	thinking := asRecord(body["thinking"])
	generationConfig := asRecord(body["generationConfig"])
	var thinkingConfig map[string]any
	if v, ok := generationConfig["thinkingConfig"]; ok {
		thinkingConfig = asRecord(v)
	} else if v, ok := generationConfig["thinking_config"]; ok {
		thinkingConfig = asRecord(v)
	} else {
		thinkingConfig = map[string]any{}
	}

	explicitEffort := firstDefinedEffort(body, reasoning, thinking, thinkingConfig, suffixEffort)
	hasBudget := detectThinkingBudget(body, reasoning, thinking, thinkingConfig)
	hasSignal := hasReasoningSignal(body, reasoning, thinkingConfig, explicitEffort, hasBudget)

	sourceEffort := "missing"
	if explicitEffort != nil {
		sourceEffort = string(*explicitEffort)
	} else if hasSignal {
		sourceEffort = "signal"
	}

	return ExtractedReasoningIntent{
		Model:              baseModel,
		Effort:             explicitEffort,
		SourceEffort:       sourceEffort,
		HasReasoningSignal: hasSignal,
		HasThinkingBudget:  hasBudget,
	}
}

// ---------------------------------------------------------------------------
// Glob matching + tag normalization
// ---------------------------------------------------------------------------

func globMatches(pattern, value string) bool {
	var sb strings.Builder
	for _, r := range pattern {
		switch r {
		case '*':
			sb.WriteString(".*")
		case '?':
			sb.WriteString(".")
		case '.', '+', '^', '$', '{', '}', '(', ')', '|', '[', ']', '\\':
			sb.WriteRune('\\')
			sb.WriteRune(r)
		default:
			sb.WriteRune(r)
		}
	}
	re, err := regexp.Compile("(?i)^" + sb.String() + "$")
	if err != nil {
		return false
	}
	return re.MatchString(value)
}

func normalizeSingleTag(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	n := strings.ToLower(strings.TrimSpace(s))
	return n
}

// NormalizeRoutingTags normalizes and deduplicates routing tags.
func NormalizeRoutingTags(value any) []string {
	var raw []any
	switch v := value.(type) {
	case []any:
		raw = v
	case []string:
		for _, s := range v {
			raw = append(raw, s)
		}
	case string:
		for _, part := range strings.Split(v, ",") {
			raw = append(raw, part)
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range raw {
		n := normalizeSingleTag(r)
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Rule matching + sorting
// ---------------------------------------------------------------------------

func inputModels(input RoutingInput) []string {
	models := []string{input.SourceModel}
	return append(models, input.SourceModelAliases...)
}

func tagsMatch(rule ReasoningRoutingRule, requestTags []string) bool {
	if len(rule.RequestTags) == 0 {
		return true
	}
	available := map[string]bool{}
	for _, t := range NormalizeRoutingTags(requestTags) {
		available[t] = true
	}
	if rule.TagMatchMode == TagAll {
		for _, t := range rule.RequestTags {
			if !available[t] {
				return false
			}
		}
		return true
	}
	for _, t := range rule.RequestTags {
		if available[t] {
			return true
		}
	}
	return false
}

func modelMatches(rule ReasoningRoutingRule, model string) bool {
	if rule.ModelPattern == nil || *rule.ModelPattern == "" {
		return true
	}
	return globMatches(*rule.ModelPattern, model)
}

func isExactModelMatch(rule ReasoningRoutingRule, model string) bool {
	if rule.ModelPattern == nil {
		return false
	}
	p := *rule.ModelPattern
	if strings.ContainsAny(p, "?*") {
		return false
	}
	return strings.EqualFold(p, model)
}

func scopeMatches(rule ReasoningRoutingRule, input RoutingInput) bool {
	if input.ConnectionOnly {
		if rule.Scope != ScopeConnection {
			return false
		}
	} else if rule.Scope == ScopeConnection {
		return false
	}
	if rule.Scope == ScopeAPIKey && !strPtrEqual(rule.APIKeyID, input.APIKeyID) {
		return false
	}
	if rule.Scope == ScopeCombo && !strPtrEqual(rule.ComboID, input.ComboID) {
		return false
	}
	if rule.Scope == ScopeConnection && !strPtrEqual(rule.ConnectionID, input.ConnectionID) {
		return false
	}
	return rule.SourceEffort == "any" || string(rule.SourceEffort) == input.SourceEffort
}

func strPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func ruleMatches(rule ReasoningRoutingRule, input RoutingInput) bool {
	if !scopeMatches(rule, input) {
		return false
	}
	matched := false
	for _, m := range inputModels(input) {
		if modelMatches(rule, m) {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	return tagsMatch(rule, input.RequestTags)
}

func exactMatchScore(rule ReasoningRoutingRule, input RoutingInput) int {
	for _, m := range inputModels(input) {
		if isExactModelMatch(rule, m) {
			return 1
		}
	}
	return 0
}

func sortRules(rules []ReasoningRoutingRule, input RoutingInput) {
	sort.SliceStable(rules, func(i, j int) bool {
		a, b := rules[i], rules[j]
		if scopeRank[b.Scope] != scopeRank[a.Scope] {
			return scopeRank[b.Scope] < scopeRank[a.Scope] // higher rank first
		}
		if b.Priority != a.Priority {
			return b.Priority > a.Priority
		}
		ea, eb := exactMatchScore(a, input), exactMatchScore(b, input)
		if eb != ea {
			return eb > ea
		}
		if a.CreatedAt != b.CreatedAt {
			return a.CreatedAt < b.CreatedAt
		}
		return a.ID < b.ID
	})
}

// ---------------------------------------------------------------------------
// Target resolution + capability
// ---------------------------------------------------------------------------

func resolveTargetModel(rule ReasoningRoutingRule, sourceModel string, combo map[string]any) string {
	if combo != nil {
		if name, ok := combo["name"].(string); ok {
			return name
		}
		if rule.TargetComboID != nil {
			return *rule.TargetComboID
		}
		return sourceModel
	}
	if rule.TargetKind == TargetModel && rule.TargetModel != nil {
		return *rule.TargetModel
	}
	return sourceModel
}

func resolveTargetEffort(rule ReasoningRoutingRule, input RoutingInput) *ReasoningEffort {
	inherits := rule.EffortMode == EffortInherit ||
		(rule.EffortMode == EffortDefault && input.HasReasoningSignal)
	if !inherits {
		return rule.TargetEffort
	}
	if input.SourceEffort == "missing" || input.SourceEffort == "signal" {
		return nil
	}
	e := ReasoningEffort(input.SourceEffort)
	return &e
}

var (
	reCapUltra = regexp.MustCompile(`^gpt-5\.6-(?:sol|terra)(?:-|$)`)
	reCapMax   = regexp.MustCompile(`^gpt-5\.6-(?:sol|terra|luna)(?:-|$)`)
)

func capabilityFor(model string, targetEffort *ReasoningEffort, requiresReasoning bool, resolveCap CapabilityResolver) Capability {
	if !requiresReasoning || (targetEffort != nil && *targetEffort == EffortNone) {
		return CapSupported
	}
	caps := resolveCap(model)
	if caps.SupportsThinking != nil && !*caps.SupportsThinking {
		return CapUnsupported
	}
	if targetEffort != nil && (*targetEffort == EffortMax || *targetEffort == EffortUltra) {
		normalized := reCodexPrefix.ReplaceAllString(strings.ToLower(model), "")
		var supported bool
		if *targetEffort == EffortUltra {
			supported = reCapUltra.MatchString(normalized)
		} else {
			supported = reCapMax.MatchString(normalized)
		}
		if supported {
			return CapSupported
		}
		if caps.SupportsThinking == nil {
			return CapUnknown
		}
		return CapUnsupported
	}
	if caps.SupportsThinking == nil {
		return CapUnknown
	}
	return CapSupported
}

func resolveCapability(targetCombo map[string]any, targetModel string, targetEffort *ReasoningEffort, requiresReasoning bool, resolveCap CapabilityResolver) (Capability, []string) {
	if targetCombo == nil {
		return capabilityFor(targetModel, targetEffort, requiresReasoning, resolveCap), nil
	}
	models, _ := targetCombo["models"].([]any)
	var statuses []Capability
	for _, entry := range models {
		var model string
		if s, ok := entry.(string); ok {
			model = s
		} else if r := asRecord(entry); r != nil {
			if m, ok := r["model"].(string); ok {
				model = m
			}
		}
		if model == "" {
			statuses = append(statuses, CapUnknown)
		} else {
			statuses = append(statuses, capabilityFor(model, targetEffort, requiresReasoning, resolveCap))
		}
	}

	allUnsupported := len(statuses) > 0
	anyUnknown := false
	unsupportedCount := 0
	for _, s := range statuses {
		if s != CapUnsupported {
			allUnsupported = false
		}
		if s == CapUnknown {
			anyUnknown = true
		}
		if s == CapUnsupported {
			unsupportedCount++
		}
	}

	var cap Capability
	switch {
	case allUnsupported:
		cap = CapUnsupported
	case anyUnknown:
		cap = CapUnknown
	default:
		cap = CapSupported
	}

	var warnings []string
	if unsupportedCount > 0 && cap != CapUnsupported {
		warnings = append(warnings, "incompatible combo target(s) will be skipped")
	}
	return cap, warnings
}

// ---------------------------------------------------------------------------
// Main decision
// ---------------------------------------------------------------------------

// ResolveReasoningRoutingRule evaluates enabled rules against the input and
// returns the winning decision, or nil if no rule matches.
// rules must be pre-filtered to enabled rules. resolveCombo and resolveCap are
// injectable seams (may be nil for tests that don't exercise those paths).
func ResolveReasoningRoutingRule(input RoutingInput, rules []ReasoningRoutingRule, resolveCombo ComboResolver, resolveCap CapabilityResolver) (*ReasoningRuleDecision, error) {
	if resolveCap == nil {
		resolveCap = func(string) CapabilityInfo { return CapabilityInfo{} }
	}

	var candidates []ReasoningRoutingRule
	for _, r := range rules {
		if ruleMatches(r, input) {
			candidates = append(candidates, r)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	sortRules(candidates, input)
	rule := candidates[0]

	// Resolve target combo if needed.
	var targetCombo map[string]any
	if rule.TargetKind == TargetCombo && rule.TargetComboID != nil {
		if resolveCombo == nil {
			return nil, errComboResolverNil
		}
		combo, err := resolveCombo(*rule.TargetComboID)
		if err != nil {
			return nil, err
		}
		if combo == nil {
			return nil, errComboNotFound
		}
		targetCombo = combo
	}
	targetModel := resolveTargetModel(rule, input.SourceModel, targetCombo)
	targetEffort := resolveTargetEffort(rule, input)

	budgetAction := rule.BudgetAction
	if targetEffort != nil && *targetEffort == EffortNone {
		budgetAction = BudgetRemove
	}

	requiresReasoning := false
	if targetEffort == nil || *targetEffort != EffortNone {
		requiresReasoning = targetEffort != nil ||
			budgetAction == BudgetSet ||
			(budgetAction == BudgetPreserve && input.HasThinkingBudget)
	}

	capModel := targetModel
	if input.CapabilityModel != nil {
		capModel = *input.CapabilityModel
	}
	capability, warnings := resolveCapability(targetCombo, capModel, targetEffort, requiresReasoning, resolveCap)
	if capability == CapUnknown {
		warnings = append(warnings, "Reasoning capability could not be verified")
	}

	return &ReasoningRuleDecision{
		Rule:              rule,
		SourceModel:       input.SourceModel,
		SourceEffort:      input.SourceEffort,
		TargetModel:       targetModel,
		TargetCombo:       targetCombo,
		TargetEffort:      targetEffort,
		Capability:        capability,
		RequiresReasoning: requiresReasoning,
		Warnings:          warnings,
	}, nil
}

// ---------------------------------------------------------------------------
// Body mutation (attach / apply directive)
// ---------------------------------------------------------------------------

// AttachReasoningRuleDirective stamps the decision onto the request body.
func AttachReasoningRuleDirective(bodyInput any, decision *ReasoningRuleDecision) map[string]any {
	source := asRecord(bodyInput)
	body := make(map[string]any, len(source)+2)
	for k, v := range source {
		body[k] = v
	}
	if decision.Rule.Scope == ScopeConnection {
		// keep source model
	} else {
		body["model"] = decision.TargetModel
	}

	ba := decision.Rule.BudgetAction
	if decision.TargetEffort != nil && *decision.TargetEffort == EffortNone {
		ba = BudgetRemove
	}

	var te any
	if decision.TargetEffort != nil {
		te = string(*decision.TargetEffort)
	}
	var bt any
	if decision.Rule.BudgetTokens != nil {
		bt = *decision.Rule.BudgetTokens
	}

	body["_omnirouteReasoningRule"] = map[string]any{
		"id":           decision.Rule.ID,
		"effortMode":   string(decision.Rule.EffortMode),
		"targetEffort": te,
		"budgetAction": string(ba),
		"budgetTokens": bt,
	}
	body["_omnirouteReasoningRouteTrace"] = map[string]any{
		"ruleId":       decision.Rule.ID,
		"ruleName":     decision.Rule.Name,
		"scope":        string(decision.Rule.Scope),
		"sourceModel":  decision.SourceModel,
		"targetModel":  decision.TargetModel,
		"sourceEffort": decision.SourceEffort,
		"targetEffort": te,
		"effortMode":   string(decision.Rule.EffortMode),
		"budgetAction": string(ba),
		"capability":   string(decision.Capability),
		"warnings":     decision.Warnings,
	}
	return body
}

func clearReasoning(body map[string]any) {
	for _, k := range []string{"reasoning_effort", "reasoningEffort", "reasoning", "effort", "thinking", "thinkingLevel", "thinking_level", "thinking_budget", "thinkingBudget"} {
		delete(body, k)
	}
	if oc := asRecord(body["output_config"]); len(oc) > 0 {
		out := make(map[string]any, len(oc))
		for k, v := range oc {
			out[k] = v
		}
		delete(out, "effort")
		body["output_config"] = out
	}
}

func clearDiscreteReasoning(body map[string]any) {
	for _, k := range []string{"reasoning_effort", "reasoningEffort", "effort", "thinkingLevel", "thinking_level"} {
		delete(body, k)
	}
	if r := asRecord(body["reasoning"]); len(r) > 0 {
		rc := make(map[string]any, len(r))
		for k, v := range r {
			rc[k] = v
		}
		delete(rc, "effort")
		if len(rc) > 0 {
			body["reasoning"] = rc
		} else {
			delete(body, "reasoning")
		}
	}
	if oc := asRecord(body["output_config"]); len(oc) > 0 {
		out := make(map[string]any, len(oc))
		for k, v := range oc {
			out[k] = v
		}
		delete(out, "effort")
		if len(out) > 0 {
			body["output_config"] = out
		} else {
			delete(body, "output_config")
		}
	}
	thinking := asRecord(body["thinking"])
	if _, ok := body["thinking"]; ok {
		if _, isNum := thinking["budget_tokens"].(float64); !isNum {
			if _, isInt := thinking["budget_tokens"].(int); !isInt {
				delete(body, "thinking")
			}
		}
	}
}

func removeBudgets(body map[string]any) {
	delete(body, "thinking_budget")
	delete(body, "thinkingBudget")

	thinking := asRecord(body["thinking"])
	if len(thinking) > 0 {
		tc := make(map[string]any, len(thinking))
		for k, v := range thinking {
			tc[k] = v
		}
		delete(tc, "budget_tokens")
		delete(tc, "budgetTokens")
		if len(tc) > 0 {
			if _, ok := body["thinking"]; ok {
				body["thinking"] = tc
			}
		} else {
			delete(body, "thinking")
		}
	} else {
		delete(body, "thinking")
	}

	reasoning := asRecord(body["reasoning"])
	if len(reasoning) > 0 {
		rc := make(map[string]any, len(reasoning))
		for k, v := range reasoning {
			rc[k] = v
		}
		delete(rc, "budget_tokens")
		delete(rc, "budgetTokens")
		delete(rc, "max_tokens")
		if len(rc) > 0 {
			if _, ok := body["reasoning"]; ok {
				body["reasoning"] = rc
			}
		} else {
			delete(body, "reasoning")
		}
	} else if _, ok := body["reasoning"]; ok {
		delete(body, "reasoning")
	}

	gc := asRecord(body["generationConfig"])
	if len(gc) > 0 {
		gcc := make(map[string]any, len(gc))
		for k, v := range gc {
			gcc[k] = v
		}
		delete(gcc, "thinkingConfig")
		delete(gcc, "thinking_config")
		if len(gcc) > 0 {
			body["generationConfig"] = gcc
		} else {
			delete(body, "generationConfig")
		}
	}
}

func applyBudget(body map[string]any, action BudgetAction, budget *int) {
	if action == BudgetPreserve {
		return
	}
	removeBudgets(body)
	if action == BudgetRemove {
		return
	}
	// action == BudgetSet
	var bt any
	if budget != nil {
		bt = *budget
	}
	body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": bt}
}

// ApplyReasoningRuleDirective applies a stamped directive to the request body.
func ApplyReasoningRuleDirective(bodyInput any) any {
	source := asRecord(bodyInput)
	directive := asRecord(source["_omnirouteReasoningRule"])
	if _, ok := directive["id"]; !ok {
		return bodyInput
	}
	body := make(map[string]any, len(source))
	for k, v := range source {
		body[k] = v
	}
	delete(body, "_omnirouteReasoningRule")

	effortMode, _ := directive["effortMode"].(string)
	targetEffort := parseEffort(directive["targetEffort"])

	if effortMode == string(EffortForce) && targetEffort != nil && *targetEffort == EffortNone {
		clearReasoning(body)
	} else if (effortMode == string(EffortForce) || effortMode == string(EffortDefault)) && targetEffort != nil {
		if effortMode == string(EffortForce) {
			clearDiscreteReasoning(body)
		}
		body["reasoning_effort"] = string(*targetEffort)
		body["reasoning"] = mergeRecord(asRecord(body["reasoning"]), "effort", string(*targetEffort))
		body["output_config"] = mergeRecord(asRecord(body["output_config"]), "effort", string(*targetEffort))
	}

	ba := BudgetPreserve
	if s, ok := directive["budgetAction"].(string); ok {
		ba = BudgetAction(s)
	}
	var bt *int
	if n, ok := directive["budgetTokens"].(int); ok {
		bt = &n
	} else if f, ok := directive["budgetTokens"].(float64); ok {
		n := int(f)
		bt = &n
	}
	applyBudget(body, ba, bt)
	return body
}

func mergeRecord(base map[string]any, key string, value any) map[string]any {
	out := make(map[string]any, len(base)+1)
	for k, v := range base {
		out[k] = v
	}
	out[key] = value
	return out
}

// FilterComboForReasoningDecision removes unsupported models from a combo.
func FilterComboForReasoningDecision(comboInput any, decision *ReasoningRuleDecision, resolveCap CapabilityResolver) (map[string]any, []string) {
	combo := make(map[string]any)
	for k, v := range asRecord(comboInput) {
		combo[k] = v
	}
	models, ok := combo["models"].([]any)
	if !ok || !decision.RequiresReasoning {
		return combo, nil
	}
	if resolveCap == nil {
		resolveCap = func(string) CapabilityInfo { return CapabilityInfo{} }
	}
	var kept []any
	var removed []string
	for _, entry := range models {
		var model string
		if s, ok := entry.(string); ok {
			model = s
		} else if r := asRecord(entry); r != nil {
			if m, ok := r["model"].(string); ok {
				model = m
			}
		}
		if model == "" || capabilityFor(model, decision.TargetEffort, true, resolveCap) != CapUnsupported {
			kept = append(kept, entry)
		} else {
			removed = append(removed, model)
		}
	}
	combo["models"] = kept
	if len(kept) == 0 {
		return nil, removed
	}
	return combo, removed
}

// ---------------------------------------------------------------------------
// Codex helpers
// ---------------------------------------------------------------------------

var reCodexTarget = regexp.MustCompile(`^(?:codex|cx)/`)

// IsCodexTarget returns true if the model is a Codex target (no slash or codex/cx prefix).
func IsCodexTarget(model string) bool {
	return !strings.Contains(model, "/") || reCodexTarget.MatchString(model)
}

// ValidateCodexWsDecision validates a decision for Codex WebSocket transport.
func ValidateCodexWsDecision(decision *ReasoningRuleDecision) string {
	if decision.TargetCombo != nil {
		models, _ := decision.TargetCombo["models"].([]any)
		allCodex := len(models) > 0
		for _, entry := range models {
			var model string
			if s, ok := entry.(string); ok {
				model = s
			} else if r := asRecord(entry); r != nil {
				if m, ok := r["model"].(string); ok {
					model = m
				}
			}
			if model == "" || !IsCodexTarget(model) {
				allCodex = false
				break
			}
		}
		if !allCodex {
			return "Codex WebSocket reasoning rules require a Codex-only target"
		}
		return "Codex WebSocket transport cannot execute combo targets"
	}
	if IsCodexTarget(decision.TargetModel) {
		return ""
	}
	return "Codex WebSocket reasoning rules require a Codex target model"
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

var (
	errComboResolverNil = &reasoningError{"combo resolver is nil"}
	errComboNotFound    = &reasoningError{"Reasoning routing target combo does not exist"}
)

type reasoningError struct{ msg string }

func (e *reasoningError) Error() string { return e.msg }
