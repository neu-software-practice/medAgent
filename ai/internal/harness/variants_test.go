package harness

import (
	"context"
	"strings"
	"testing"

	"medagent/ai"
)

// 简单患者：按序返回预设答案，用尽后重复最后一句。
func seqPatient(answers ...string) func(string) string {
	i := 0
	return func(string) string {
		msg := answers[i]
		if i < len(answers)-1 {
			i++
		}
		return msg
	}
}

func TestVariantMultiRoundTest(t *testing.T) {
	triageN := 0
	llm := &ai.FakeLLM{On: func(req ai.CompletionRequest) (ai.CompletionResult, error) {
		switch req.Schema.Name {
		case "interview":
			return ai.StructuredOf(ai.InterviewResult{Advance: &ai.AdvanceToTriage{
				Subjective: map[string]any{"主诉": "腹痛"}}}), nil
		case "triage_decide":
			triageN++
			if triageN <= 2 {
				return ai.StructuredOf(ai.TriageDecision{
					Decision: ai.TriageTest, SubjectiveExhausted: true,
					Reason: "需进一步检验", TestItems: []string{"血常规"}}), nil
			}
			return ai.StructuredOf(ai.TriageDecision{Decision: ai.TriageConfirm,
				Diagnosis: &ai.Diagnosis{Name: "胃肠炎", Confidence: 0.85}}), nil
		case "treatment_plan":
			return ai.StructuredOf(ai.TreatmentPlan{Plan: ai.PlanAdviceOnly, Advice: "清淡饮食"}), nil
		}
		t.Fatalf("未预期 schema %q", req.Schema.Name)
		return ai.CompletionResult{}, nil
	}}
	out, err := RunVisit(context.Background(), Deps{
		Layer: ai.NewDecisionLayer(llm), Caps: map[string]bool{},
		Patient:     seqPatient("肚子疼"),
		TestResults: func([]string) []ai.TestResult { return []ai.TestResult{{Item: "血常规", Value: "正常"}} },
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.Join(out.Trace, ","), "test_filled"); n != 2 {
		t.Fatalf("期望 2 轮检验，得到 %d；轨迹=%v", n, out.Trace)
	}
}

func TestVariantInterviewBounce(t *testing.T) {
	triageN := 0
	llm := &ai.FakeLLM{On: func(req ai.CompletionRequest) (ai.CompletionResult, error) {
		switch req.Schema.Name {
		case "interview":
			return ai.StructuredOf(ai.InterviewResult{Advance: &ai.AdvanceToTriage{
				Subjective: map[string]any{"主诉": "头痛"}}}), nil
		case "triage_decide":
			triageN++
			if triageN == 1 {
				return ai.StructuredOf(ai.TriageDecision{Decision: ai.TriageInterview,
					MissingSubjective: []string{"持续时间"}}), nil
			}
			return ai.StructuredOf(ai.TriageDecision{Decision: ai.TriageConfirm,
				Diagnosis: &ai.Diagnosis{Name: "紧张性头痛", Confidence: 0.8}}), nil
		case "treatment_plan":
			return ai.StructuredOf(ai.TreatmentPlan{Plan: ai.PlanAdviceOnly, Advice: "规律作息"}), nil
		}
		t.Fatalf("未预期 schema %q", req.Schema.Name)
		return ai.CompletionResult{}, nil
	}}
	out, err := RunVisit(context.Background(), Deps{
		Layer: ai.NewDecisionLayer(llm), Caps: map[string]bool{},
		Patient:     seqPatient("头痛", "持续两天"),
		TestResults: func([]string) []ai.TestResult { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.Join(out.Trace, ","), "advance"); n != 2 {
		t.Fatalf("期望问诊两次（含 INTERVIEW 回退），得到 %d；轨迹=%v", n, out.Trace)
	}
}

func TestVariantCapabilityMissingReferral(t *testing.T) {
	treatN := 0
	llm := &ai.FakeLLM{On: func(req ai.CompletionRequest) (ai.CompletionResult, error) {
		switch req.Schema.Name {
		case "interview":
			return ai.StructuredOf(ai.InterviewResult{Advance: &ai.AdvanceToTriage{
				Subjective: map[string]any{"主诉": "心悸"}}}), nil
		case "triage_decide":
			return ai.StructuredOf(ai.TriageDecision{Decision: ai.TriageConfirm,
				Diagnosis: &ai.Diagnosis{Name: "心律失常", Confidence: 0.9}}), nil
		case "treatment_plan":
			treatN++
			if treatN == 1 {
				return ai.StructuredOf(ai.TreatmentPlan{Plan: ai.PlanTreatment,
					Advice: "尽快处理", RequiredCapability: "心脏介入"}), nil
			}
			return ai.StructuredOf(ai.TreatmentPlan{Plan: ai.PlanReferral,
				Advice: "尽快前往上级医院", ReferralReason: "本院无心脏介入能力"}), nil
		}
		t.Fatalf("未预期 schema %q", req.Schema.Name)
		return ai.CompletionResult{}, nil
	}}
	out, err := RunVisit(context.Background(), Deps{
		Layer: ai.NewDecisionLayer(llm), Caps: map[string]bool{"理疗": true}, // 不含"心脏介入"
		Patient:     seqPatient("心慌"),
		TestResults: func([]string) []ai.TestResult { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Final != "REFERRAL" || out.Plan != ai.PlanReferral {
		t.Fatalf("期望转诊终态，得到 %+v", out)
	}
	trace := strings.Join(out.Trace, ",")
	if !strings.Contains(trace, "treatment:TREATMENT") || !strings.Contains(trace, "treatment:REFERRAL") {
		t.Fatalf("期望能力不具备→重决策→转诊，轨迹=%s", trace)
	}
}

func TestVariantRevisitCarriesPrior(t *testing.T) {
	var firstInterviewCtx string
	llm := &ai.FakeLLM{On: func(req ai.CompletionRequest) (ai.CompletionResult, error) {
		switch req.Schema.Name {
		case "interview":
			if firstInterviewCtx == "" {
				firstInterviewCtx = req.Messages[0].Content
			}
			return ai.StructuredOf(ai.InterviewResult{Advance: &ai.AdvanceToTriage{
				Subjective: map[string]any{"主诉": "复诊咽痛未愈"}}}), nil
		case "triage_decide":
			return ai.StructuredOf(ai.TriageDecision{Decision: ai.TriageConfirm,
				Diagnosis: &ai.Diagnosis{Name: "急性咽炎", Confidence: 0.88}}), nil
		case "treatment_plan":
			return ai.StructuredOf(ai.TreatmentPlan{Plan: ai.PlanAdviceOnly, Advice: "继续观察"}), nil
		}
		t.Fatalf("未预期 schema %q", req.Schema.Name)
		return ai.CompletionResult{}, nil
	}}
	out, err := RunVisit(context.Background(), Deps{
		Layer: ai.NewDecisionLayer(llm), Caps: map[string]bool{},
		Patient:     seqPatient("还是嗓子痛"),
		TestResults: func([]string) []ai.TestResult { return nil },
		Prior:       &ai.VisitSummary{Diagnosis: &ai.Diagnosis{Name: "急性咽炎"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Final != "ADVICE" {
		t.Fatalf("复诊应正常收尾，得到 %+v", out)
	}
	if !strings.Contains(firstInterviewCtx, "上次就诊") {
		t.Fatalf("复诊上下文未携带 PriorVisit：%s", firstInterviewCtx)
	}
}
