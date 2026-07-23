package domain

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// pipeline.go — Multi-stage LLM pipeline engine.
// Port of src/domain/pipeline.ts
//
// Pure orchestration engine that delegates execution to a caller-provided
// StageExecutor. No side effects, no network calls.

type TaskType string

const (
	TaskCode      TaskType = "code"
	TaskMath      TaskType = "math"
	TaskReasoning TaskType = "reasoning"
	TaskCreative  TaskType = "creative"
	TaskMedium    TaskType = "medium"
	TaskSimple    TaskType = "simple"
)

type FitnessTier string

const (
	FitnessBestReasoning FitnessTier = "best-reasoning"
	FitnessCheapest      FitnessTier = "cheapest"
	FitnessModerate      FitnessTier = "moderate"
)

type PipelineStage struct {
	Name          StageName
	FitnessTier   FitnessTier
	SystemOverride string
}

type PipelineConfig struct {
	Stages   []PipelineStage
	Request  string
	TaskType TaskType
}

type StageResult struct {
	Stage        StageName
	Text         string
	Provider     string
	LatencyMs    int64
	InputTokens  int
	OutputTokens int
	Skipped      bool
	Error        string
}

type PipelineResult struct {
	Text           string
	Stages         []StageResult
	Fallback       bool
	ReflectVerdict string // "pass" | "fail" | ""
}

type StageExecutorArgs struct {
	Messages    []Message
	Stream      bool
	FitnessTier FitnessTier
}

type Message struct {
	Role    string
	Content string
}

type StageExecutorResult struct {
	Text         string
	Provider     string
	InputTokens  int
	OutputTokens int
}

// StageExecutor is a caller-provided function that executes a single LLM call.
type StageExecutor func(args StageExecutorArgs) (StageExecutorResult, error)

// TASK_STAGES maps task types to their pipeline stage templates.
var taskStages = map[TaskType][]PipelineStage{
	TaskCode: {
		{Name: StagePlan, FitnessTier: FitnessBestReasoning},
		{Name: StageExecute, FitnessTier: FitnessCheapest},
		{Name: StageReflect, FitnessTier: FitnessModerate},
		{Name: StageFix, FitnessTier: FitnessCheapest},
	},
	TaskMath: {
		{Name: StageExecute, FitnessTier: FitnessBestReasoning},
		{Name: StageReflect, FitnessTier: FitnessModerate},
	},
	TaskReasoning: {
		{Name: StageExecute, FitnessTier: FitnessBestReasoning},
		{Name: StageReflect, FitnessTier: FitnessModerate},
	},
	TaskCreative: {
		{Name: StageExecute, FitnessTier: FitnessModerate},
		{Name: StageReflect, FitnessTier: FitnessBestReasoning},
	},
	TaskMedium: {
		{Name: StageExecute, FitnessTier: FitnessModerate},
	},
	TaskSimple: {
		{Name: StageExecute, FitnessTier: FitnessCheapest},
	},
}

// BuildPipelineConfig builds a PipelineConfig for a given task type and request.
func BuildPipelineConfig(request string, taskType TaskType) PipelineConfig {
	stages, ok := taskStages[taskType]
	if !ok {
		stages = taskStages[TaskSimple]
	}
	return PipelineConfig{
		Request:  request,
		TaskType: taskType,
		Stages:   stages,
	}
}

// ---------------------------------------------------------------------------
// Reflect JSON parsing
// ---------------------------------------------------------------------------

type ReflectPass struct {
	Status       string `json:"status"`
	Confirmation string `json:"confirmation"`
}

type ReflectFail struct {
	Status    string   `json:"status"`
	Issues    []string `json:"issues"`
	Corrected string   `json:"corrected"`
}

type ReflectResult struct {
	Status       string
	Confirmation string
	Issues       []string
	Corrected    string
}

var (
	jsonBlockRe  = regexp.MustCompile("```(?:json)?\\s*([\\s\\S]*?)```")
	jsonObjectRe = regexp.MustCompile("\\{[\\s\\S]*\\}")
)

// ParseReflectJson parses the reflect stage output as structured JSON.
// Returns nil if the output cannot be parsed (conservative: treated as fail).
func ParseReflectJson(text string) *ReflectResult {
	if text == "" {
		return nil
	}
	jsonStr := strings.TrimSpace(text)

	// Try extracting from markdown code block first.
	if m := jsonBlockRe.FindStringSubmatch(jsonStr); m != nil {
		jsonStr = strings.TrimSpace(m[1])
	} else if m := jsonObjectRe.FindString(jsonStr); m != "" {
		jsonStr = m
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil
	}

	status, _ := raw["status"].(string)
	if status == "pass" {
		confirmation, ok := raw["confirmation"].(string)
		if !ok {
			return nil // TS: typeof parsed.confirmation === "string" required
		}
		return &ReflectResult{Status: "pass", Confirmation: confirmation}
	}
	if status == "fail" {
		var issues []string
		if arr, ok := raw["issues"].([]any); ok {
			for _, v := range arr {
				if s, ok := v.(string); ok {
					issues = append(issues, s)
				}
			}
		}
		corrected, _ := raw["corrected"].(string)
		return &ReflectResult{Status: "fail", Issues: issues, Corrected: corrected}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Pipeline execution
// ---------------------------------------------------------------------------

func executeStage(stage PipelineStage, request string, context map[string]string, executor StageExecutor) StageResult {
	vars := map[string]string{"original_request": request}
	for k, v := range context {
		vars[k] = v
	}
	rendered, err := RenderPrompt(stage.Name, vars)
	if err != nil {
		return StageResult{Stage: stage.Name, Error: err.Error()}
	}

	system := rendered.System
	if stage.SystemOverride != "" {
		system = stage.SystemOverride
	}
	messages := []Message{
		{Role: "system", Content: system},
		{Role: "user", Content: rendered.User},
	}

	start := time.Now()
	result, execErr := executor(StageExecutorArgs{
		Messages:    messages,
		Stream:      false,
		FitnessTier: stage.FitnessTier,
	})
	latencyMs := time.Since(start).Milliseconds()

	if execErr != nil {
		return StageResult{Stage: stage.Name, LatencyMs: latencyMs, Error: execErr.Error()}
	}
	return StageResult{
		Stage:        stage.Name,
		Text:         result.Text,
		Provider:     result.Provider,
		LatencyMs:    latencyMs,
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
	}
}

// ExecutePipeline executes a multi-stage pipeline.
//
// After the reflect stage, parses structured JSON:
//   - pass → skip fix stage
//   - fail → run fix stage with corrected output
//   - parse failure → treated as fail (conservative)
//
// Any stage failure triggers fallback:true and returns best available output.
func ExecutePipeline(config PipelineConfig, executor StageExecutor) PipelineResult {
	var results []StageResult
	fallback := false
	reflectVerdict := ""
	context := make(map[string]string)

	for _, stage := range config.Stages {
		// Skip fix if reflect passed.
		if stage.Name == StageFix && reflectVerdict == "pass" {
			results = append(results, StageResult{Stage: StageFix, Skipped: true})
			continue
		}

		result := executeStage(stage, config.Request, context, executor)
		results = append(results, result)

		// If a stage errored, mark fallback and break.
		if result.Error != "" {
			fallback = true
			break
		}

		// Thread context forward.
		switch stage.Name {
		case StagePlan:
			context["plan_context"] = result.Text
		case StageExecute:
			context["execution_response"] = result.Text
		case StageReflect:
			context["reflection_response"] = result.Text
			parsed := ParseReflectJson(result.Text)
			if parsed == nil {
				reflectVerdict = "fail"
			} else {
				reflectVerdict = parsed.Status
				if parsed.Status == "fail" && parsed.Corrected != "" {
					context["execution_response"] = parsed.Corrected
				}
			}
		case StageFix:
			context["execution_response"] = result.Text
		}
	}

	// Pick best available output: fix > reflect-corrected > execute > last successful.
	var bestText string
	for _, r := range results {
		if r.Stage == StageFix && !r.Skipped && r.Error == "" {
			bestText = r.Text
			break
		}
	}
	if bestText == "" && reflectVerdict == "fail" {
		bestText = context["execution_response"]
	}
	if bestText == "" {
		for _, r := range results {
			if r.Stage == StageExecute && r.Error == "" {
				bestText = r.Text
				break
			}
		}
	}
	if bestText == "" {
		for i := len(results) - 1; i >= 0; i-- {
			if results[i].Error == "" && !results[i].Skipped {
				bestText = results[i].Text
				break
			}
		}
	}

	return PipelineResult{
		Text:           bestText,
		Stages:         results,
		Fallback:       fallback,
		ReflectVerdict: reflectVerdict,
	}
}
