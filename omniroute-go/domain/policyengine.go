package domain

import (
	"regexp"
	"sort"
	"sync"
)

// policyengine.go — Centralized policy evaluation engine.
// Port of src/domain/policyEngine.ts
//
// Combines lockout, budget, and fallback decisions into a single verdict.
// The PolicyEngine class is standalone (glob-based policy evaluation).
//
// RACE NOTE: PolicyEngine._policies is guarded by a sync.RWMutex.

// ---------------------------------------------------------------------------
// Policy types
// ---------------------------------------------------------------------------

// PolicyConditions holds policy match conditions.
type PolicyConditions struct {
	ModelPattern string
}

// PolicyActions holds policy actions to apply.
type PolicyActions struct {
	PreferProvider []string
	BlockModel     []string
	MaxTokens      *int
}

// Policy is a single policy rule.
type Policy struct {
	ID         string
	Name       string
	Type       string // routing | access | budget
	Enabled    bool
	Priority   int
	Conditions *PolicyConditions
	Actions    *PolicyActions
}

// PolicyEvalResult is the outcome of policy evaluation.
type PolicyEvalResult struct {
	Allowed            bool
	Reason             string
	PreferredProviders []string
	AppliedPolicies    []string
	MaxTokens          *int
}

// ---------------------------------------------------------------------------
// PolicyEngine — class-based policy evaluation
// ---------------------------------------------------------------------------

// PolicyEngine evaluates a set of policies against a request context.
type PolicyEngine struct {
	mu       sync.RWMutex
	policies []Policy
}

// NewPolicyEngine creates an empty PolicyEngine.
func NewPolicyEngine() *PolicyEngine {
	return &PolicyEngine{}
}

// LoadPolicies replaces all policies.
func (e *PolicyEngine) LoadPolicies(policies []Policy) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.policies = make([]Policy, len(policies))
	copy(e.policies, policies)
}

// AddPolicy appends a policy.
func (e *PolicyEngine) AddPolicy(p Policy) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.policies = append(e.policies, p)
}

// RemovePolicy removes a policy by ID.
func (e *PolicyEngine) RemovePolicy(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Policy
	for _, p := range e.policies {
		if p.ID != id {
			out = append(out, p)
		}
	}
	e.policies = out
}

// GetPolicies returns a copy of all policies.
func (e *PolicyEngine) GetPolicies() []Policy {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Policy, len(e.policies))
	copy(out, e.policies)
	return out
}

// Evaluate evaluates all enabled policies against a model context.
func (e *PolicyEngine) Evaluate(model string) PolicyEvalResult {
	e.mu.RLock()
	// Copy enabled policies and sort by priority.
	var sorted []Policy
	for _, p := range e.policies {
		if p.Enabled {
			sorted = append(sorted, p)
		}
	}
	e.mu.RUnlock()

	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Priority < sorted[j].Priority
	})

	result := PolicyEvalResult{
		Allowed:            true,
		PreferredProviders: []string{},
		AppliedPolicies:    []string{},
	}

	for _, policy := range sorted {
		// Check model condition.
		if policy.Conditions != nil && policy.Conditions.ModelPattern != "" {
			if !globMatch(policy.Conditions.ModelPattern, model) {
				continue
			}
		}

		switch policy.Type {
		case "routing":
			if policy.Actions != nil && len(policy.Actions.PreferProvider) > 0 {
				result.PreferredProviders = append(result.PreferredProviders, policy.Actions.PreferProvider...)
			}
			result.AppliedPolicies = append(result.AppliedPolicies, policy.Name)

		case "access":
			if policy.Actions != nil && len(policy.Actions.BlockModel) > 0 {
				blocked := false
				for _, pattern := range policy.Actions.BlockModel {
					if globMatch(pattern, model) {
						blocked = true
						break
					}
				}
				if blocked {
					result.Allowed = false
					result.Reason = "Model \"" + model + "\" blocked by policy \"" + policy.Name + "\""
					result.AppliedPolicies = append(result.AppliedPolicies, policy.Name)
					return result
				}
			}
			result.AppliedPolicies = append(result.AppliedPolicies, policy.Name)

		case "budget":
			if policy.Actions != nil && policy.Actions.MaxTokens != nil {
				result.MaxTokens = policy.Actions.MaxTokens
			}
			result.AppliedPolicies = append(result.AppliedPolicies, policy.Name)
		}
	}

	return result
}

// globMatch matches a value against a glob pattern (* wildcard, case-sensitive).
func globMatch(pattern, value string) bool {
	escaped := regexp.QuoteMeta(pattern)
	// Replace escaped \* with .* for glob.
	escaped = replaceAll(escaped, `\*`, ".*")
	re, err := regexp.Compile("^" + escaped + "$")
	if err != nil {
		return false
	}
	return re.MatchString(value)
}

func replaceAll(s, old, new string) string {
	result := ""
	for i := 0; i < len(s); {
		if i+len(old) <= len(s) && s[i:i+len(old)] == old {
			result += new
			i += len(old)
		} else {
			result += string(s[i])
			i++
		}
	}
	return result
}
