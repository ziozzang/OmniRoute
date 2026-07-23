package domain

import (
	"errors"
	"testing"
)

// ---------------------------------------------------------------------------
// BuildPipelineConfig
// ---------------------------------------------------------------------------

func TestBuildPipelineConfig(t *testing.T) {
	cases := []struct {
		taskType TaskType
		stages   int
	}{
		{TaskCode, 4},
		{TaskMath, 2},
		{TaskReasoning, 2},
		{TaskCreative, 2},
		{TaskMedium, 1},
		{TaskSimple, 1},
		{TaskType("unknown"), 1}, // fallback to simple
	}
	for _, c := range cases {
		cfg := BuildPipelineConfig("test request", c.taskType)
		if len(cfg.Stages) != c.stages {
			t.Errorf("taskType=%s: stages=%d, want %d", c.taskType, len(cfg.Stages), c.stages)
		}
		if cfg.Request != "test request" {
			t.Errorf("request = %q", cfg.Request)
		}
	}
}

func TestCodePipelineStageOrder(t *testing.T) {
	cfg := BuildPipelineConfig("build API", TaskCode)
	want := []StageName{StagePlan, StageExecute, StageReflect, StageFix}
	for i, s := range cfg.Stages {
		if s.Name != want[i] {
			t.Fatalf("stage[%d] = %q, want %q", i, s.Name, want[i])
		}
	}
	// Fitness tiers
	if cfg.Stages[0].FitnessTier != FitnessBestReasoning {
		t.Fatalf("plan tier = %q", cfg.Stages[0].FitnessTier)
	}
	if cfg.Stages[1].FitnessTier != FitnessCheapest {
		t.Fatalf("execute tier = %q", cfg.Stages[1].FitnessTier)
	}
}

// ---------------------------------------------------------------------------
// ParseReflectJson
// ---------------------------------------------------------------------------

func TestParseReflectJsonPass(t *testing.T) {
	r := ParseReflectJson(`{"status":"pass","confirmation":"looks good"}`)
	if r == nil || r.Status != "pass" || r.Confirmation != "looks good" {
		t.Fatalf("got %+v", r)
	}
}

func TestParseReflectJsonFail(t *testing.T) {
	r := ParseReflectJson(`{"status":"fail","issues":["missing error handling"],"corrected":"fixed code"}`)
	if r == nil || r.Status != "fail" {
		t.Fatalf("got %+v", r)
	}
	if len(r.Issues) != 1 || r.Issues[0] != "missing error handling" {
		t.Fatalf("issues = %v", r.Issues)
	}
	if r.Corrected != "fixed code" {
		t.Fatalf("corrected = %q", r.Corrected)
	}
}

func TestParseReflectJsonMarkdownBlock(t *testing.T) {
	text := "Here is my review:\n```json\n{\"status\":\"pass\",\"confirmation\":\"ok\"}\n```\nDone."
	r := ParseReflectJson(text)
	if r == nil || r.Status != "pass" {
		t.Fatalf("markdown block: got %+v", r)
	}
}

func TestParseReflectJsonRawObject(t *testing.T) {
	text := "Some preamble {\"status\":\"fail\",\"issues\":[],\"corrected\":\"x\"} trailing"
	r := ParseReflectJson(text)
	if r == nil || r.Status != "fail" {
		t.Fatalf("raw object: got %+v", r)
	}
}

func TestParseReflectJsonInvalid(t *testing.T) {
	if r := ParseReflectJson(""); r != nil {
		t.Fatalf("empty: got %+v", r)
	}
	if r := ParseReflectJson("not json at all"); r != nil {
		t.Fatalf("non-json: got %+v", r)
	}
	if r := ParseReflectJson(`{"status":"unknown"}`); r != nil {
		t.Fatalf("unknown status: got %+v", r)
	}
	if r := ParseReflectJson(`{"status":"pass"}`); r != nil {
		t.Fatalf("pass without confirmation: got %+v", r)
	}
}

func TestParseReflectJsonNonStringIssues(t *testing.T) {
	r := ParseReflectJson(`{"status":"fail","issues":[1,"valid",null],"corrected":"x"}`)
	if r == nil {
		t.Fatal("nil")
	}
	if len(r.Issues) != 1 || r.Issues[0] != "valid" {
		t.Fatalf("issues filtered = %v", r.Issues)
	}
}

// ---------------------------------------------------------------------------
// ExecutePipeline
// ---------------------------------------------------------------------------

// mockExecutor returns canned responses keyed by stage.
func mockExecutor(responses map[StageName]string, failStages map[StageName]bool) StageExecutor {
	return func(args StageExecutorArgs) (StageExecutorResult, error) {
		// Determine stage from system prompt content heuristically.
		// Simpler: use a counter via closure is hard; instead inspect fitnessTier mapping.
		// For tests we map by the user prompt content markers.
		userContent := ""
		for _, m := range args.Messages {
			if m.Role == "user" {
				userContent = m.Content
			}
		}
		var stage StageName
		switch {
		case containsStr(userContent, "execution plan"):
			stage = StagePlan
		case containsStr(userContent, "Evaluate the output"):
			stage = StageReflect
		case containsStr(userContent, "Reflection feedback"):
			stage = StageFix
		default:
			stage = StageExecute
		}
		if failStages[stage] {
			return StageExecutorResult{}, errors.New("stage failed: " + string(stage))
		}
		return StageExecutorResult{Text: responses[stage], Provider: "mock"}, nil
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestExecutePipelineSimple(t *testing.T) {
	cfg := BuildPipelineConfig("hello", TaskSimple)
	exec := mockExecutor(map[StageName]string{StageExecute: "world"}, nil)
	res := ExecutePipeline(cfg, exec)
	if res.Text != "world" {
		t.Fatalf("text = %q", res.Text)
	}
	if res.Fallback {
		t.Fatal("should not fallback")
	}
	if len(res.Stages) != 1 {
		t.Fatalf("stages = %d", len(res.Stages))
	}
}

func TestExecutePipelineReflectPassSkipsFix(t *testing.T) {
	cfg := BuildPipelineConfig("build it", TaskCode)
	responses := map[StageName]string{
		StagePlan:    "step 1: do X",
		StageExecute: "done X",
		StageReflect: `{"status":"pass","confirmation":"good"}`,
		StageFix:     "should not run",
	}
	exec := mockExecutor(responses, nil)
	res := ExecutePipeline(cfg, exec)

	if res.ReflectVerdict != "pass" {
		t.Fatalf("verdict = %q", res.ReflectVerdict)
	}
	// fix stage should be skipped
	var fixResult *StageResult
	for i := range res.Stages {
		if res.Stages[i].Stage == StageFix {
			fixResult = &res.Stages[i]
		}
	}
	if fixResult == nil || !fixResult.Skipped {
		t.Fatalf("fix should be skipped, got %+v", fixResult)
	}
	// best text = execute output (no fix ran)
	if res.Text != "done X" {
		t.Fatalf("text = %q, want execute output", res.Text)
	}
}

func TestExecutePipelineReflectFailRunsFix(t *testing.T) {
	cfg := BuildPipelineConfig("build it", TaskCode)
	responses := map[StageName]string{
		StagePlan:    "plan",
		StageExecute: "buggy code",
		StageReflect: `{"status":"fail","issues":["bug"],"corrected":"fixed code"}`,
		StageFix:     "final fixed code",
	}
	exec := mockExecutor(responses, nil)
	res := ExecutePipeline(cfg, exec)

	if res.ReflectVerdict != "fail" {
		t.Fatalf("verdict = %q", res.ReflectVerdict)
	}
	// fix stage should run (not skipped)
	var fixResult *StageResult
	for i := range res.Stages {
		if res.Stages[i].Stage == StageFix {
			fixResult = &res.Stages[i]
		}
	}
	if fixResult == nil || fixResult.Skipped {
		t.Fatalf("fix should run, got %+v", fixResult)
	}
	// best text = fix output
	if res.Text != "final fixed code" {
		t.Fatalf("text = %q, want fix output", res.Text)
	}
}

func TestExecutePipelineReflectParseFailConservative(t *testing.T) {
	cfg := BuildPipelineConfig("build it", TaskCode)
	responses := map[StageName]string{
		StagePlan:    "plan",
		StageExecute: "output",
		StageReflect: "I think it's fine but no JSON", // unparseable
		StageFix:     "corrected output",
	}
	exec := mockExecutor(responses, nil)
	res := ExecutePipeline(cfg, exec)
	// parse failure → conservative fail → fix runs
	if res.ReflectVerdict != "fail" {
		t.Fatalf("verdict = %q, want fail (conservative)", res.ReflectVerdict)
	}
}

func TestExecutePipelineStageErrorFallback(t *testing.T) {
	cfg := BuildPipelineConfig("build it", TaskCode)
	responses := map[StageName]string{StagePlan: "plan"}
	exec := mockExecutor(responses, map[StageName]bool{StageExecute: true})
	res := ExecutePipeline(cfg, exec)
	if !res.Fallback {
		t.Fatal("should fallback on stage error")
	}
	// execution stopped at execute error
	if len(res.Stages) != 2 {
		t.Fatalf("stages = %d, want 2 (plan + failed execute)", len(res.Stages))
	}
	if res.Stages[1].Error == "" {
		t.Fatal("execute should have error")
	}
}

func TestExecutePipelineSystemOverride(t *testing.T) {
	cfg := PipelineConfig{
		Request: "test",
		Stages: []PipelineStage{
			{Name: StageExecute, FitnessTier: FitnessCheapest, SystemOverride: "CUSTOM SYSTEM"},
		},
	}
	var gotSystem string
	exec := func(args StageExecutorArgs) (StageExecutorResult, error) {
		for _, m := range args.Messages {
			if m.Role == "system" {
				gotSystem = m.Content
			}
		}
		return StageExecutorResult{Text: "ok"}, nil
	}
	ExecutePipeline(cfg, exec)
	if gotSystem != "CUSTOM SYSTEM" {
		t.Fatalf("system = %q, want override", gotSystem)
	}
}

func TestExecutePipelineFitnessTierPassed(t *testing.T) {
	cfg := PipelineConfig{
		Request: "test",
		Stages:  []PipelineStage{{Name: StageExecute, FitnessTier: FitnessBestReasoning}},
	}
	var gotTier FitnessTier
	exec := func(args StageExecutorArgs) (StageExecutorResult, error) {
		gotTier = args.FitnessTier
		return StageExecutorResult{Text: "ok"}, nil
	}
	ExecutePipeline(cfg, exec)
	if gotTier != FitnessBestReasoning {
		t.Fatalf("fitnessTier = %q", gotTier)
	}
}
