package domain

import (
	"regexp"
	"strings"
)

// prompts.go — Stage prompt templates with variable interpolation.
// Port of src/domain/prompts.ts
//
// Pure, stateless functions — no race concerns.

// StageName is a pipeline stage identifier.
type StageName string

const (
	StagePlan    StageName = "plan"
	StageExecute StageName = "execute"
	StageReflect StageName = "reflect"
	StageFix     StageName = "fix"
)

// StagePrompt holds system and user prompt templates.
type StagePrompt struct {
	System string
	User   string
}

// StagePrompts maps stage names to their prompt templates.
var StagePrompts = map[StageName]StagePrompt{
	StagePlan: {
		System: "You are a planning assistant. Analyze the user's request and produce a clear, step-by-step execution plan. Break complex tasks into atomic steps. Identify dependencies, constraints, and potential failure points. Output the plan as numbered steps with brief explanations.",
		User:   "Create a detailed execution plan for the following request.\n\nRequest: {original_request}",
	},
	StageExecute: {
		System: "You are a capable assistant. Execute the given task accurately and completely. Follow any provided plan precisely. Produce clear, well-structured output.",
		User:   "{plan_context}\nRequest: {original_request}",
	},
	StageReflect: {
		System: "You are a quality reviewer. Evaluate the execution output against the original request. You MUST respond with a JSON object in exactly this format:\n{\"status\":\"pass\",\"confirmation\":\"<brief explanation of why the output satisfies the request>\"}\nOR\n{\"status\":\"fail\",\"issues\":[\"<issue 1>\",\"<issue 2>\"],\"corrected\":\"<corrected output>\"}\nBe strict: only mark pass if the output fully satisfies the request. If there are any issues, omissions, or errors, mark as fail and provide a corrected version.",
		User:   "Original request: {original_request}\n\nExecution output:\n{execution_response}\n\nEvaluate the output and respond with the required JSON format.",
	},
	StageFix: {
		System: "You are a corrective assistant. The previous execution had issues identified during review. Apply the corrections and improvements specified in the reflection. Produce a final, polished output that addresses all identified issues.",
		User:   "Original request: {original_request}\n\nReflection feedback:\n{reflection_response}\n\nProduce the corrected output.",
	},
}

var interpolationRe = regexp.MustCompile(`\{(\w+)\}`)

// Interpolate substitutes {variable_name} placeholders in a template string.
func Interpolate(template string, variables map[string]string) string {
	return interpolationRe.ReplaceAllStringFunc(template, func(match string) string {
		key := match[1 : len(match)-1] // strip { }
		if val, ok := variables[key]; ok {
			return val
		}
		return match
	})
}

// RenderPrompt renders a stage prompt with the given variables.
func RenderPrompt(stage StageName, variables map[string]string) (StagePrompt, error) {
	template, ok := StagePrompts[stage]
	if !ok {
		return StagePrompt{}, &stageError{stage: string(stage)}
	}
	return StagePrompt{
		System: Interpolate(template.System, variables),
		User:   Interpolate(template.User, variables),
	}, nil
}

type stageError struct{ stage string }

func (e *stageError) Error() string { return "unknown stage: " + e.stage }

var _ = strings.TrimSpace // reserved
