// Package harness 是极简 mock 编排层：只够驱动 AI 决策层走通主干，
// 用于端到端测试与 demo。它不是真实编排层（无卡片/saga/持久化）。
package harness

import (
	"context"
	"fmt"

	"medagent/ai"
)

// Deps 是一次就诊所需的外部依赖（测试注入）。
type Deps struct {
	Layer       ai.DecisionLayer
	Caps        map[string]bool                      // 本院能力清单
	Patient     func(lastDoctorReply string) string  // 返回下一句患者消息
	TestResults func(items []string) []ai.TestResult // 桩化检验回填
	Prior       *ai.VisitSummary                     // 复诊上次摘要（可为 nil）
}

// Outcome 是一次就诊的终态。
type Outcome struct {
	Final     string // "ADVICE" | "REFERRAL"
	Diagnosis *ai.Diagnosis
	Plan      ai.PlanKind
	Advice    string
	Trace     []string
}

const (
	maxInterviewTurns = 20
	maxTriageRounds   = 10
)

// RunVisit 驱动一次就诊：问诊采集 → 收敛环 → 处置 → 终态。
func RunVisit(ctx context.Context, d Deps) (Outcome, error) {
	snap := ai.Snapshot{PriorVisit: d.Prior, Subjective: map[string]any{}}
	var trace []string

	if err := interviewPhase(ctx, d, &snap, &trace); err != nil {
		return Outcome{}, err
	}

	for round := 0; ; round++ {
		if round >= maxTriageRounds {
			return Outcome{}, fmt.Errorf("收敛环未在 %d 轮内收敛", maxTriageRounds)
		}
		td, err := d.Layer.Triage(ctx, snap)
		if err != nil {
			return Outcome{}, err
		}
		trace = append(trace, "triage:"+string(td.Decision))
		switch td.Decision {
		case ai.TriageConfirm:
			snap.Diagnosis = td.Diagnosis
			snap.Feedback = nil
			return treatmentPhase(ctx, d, &snap, trace)
		case ai.TriageInterview:
			snap.Feedback = &ai.OrchestratorFeedback{MissingHint: td.MissingSubjective}
			if err := interviewPhase(ctx, d, &snap, &trace); err != nil {
				return Outcome{}, err
			}
			snap.Feedback = nil
		case ai.TriageTest:
			snap.TestResults = append(snap.TestResults, d.TestResults(td.TestItems)...)
			trace = append(trace, "test_filled")
		default:
			return Outcome{}, fmt.Errorf("非法 triage decision %q", td.Decision)
		}
	}
}

func interviewPhase(ctx context.Context, d Deps, snap *ai.Snapshot, trace *[]string) error {
	reply := ""
	for turn := 0; turn < maxInterviewTurns; turn++ {
		msg := d.Patient(reply)
		snap.Interview = append(snap.Interview, ai.DialogTurn{Role: "patient", Text: msg})
		res, err := d.Layer.Interview(ctx, *snap)
		if err != nil {
			return err
		}
		if res.Advance != nil {
			for k, v := range res.Advance.Subjective {
				snap.Subjective[k] = v
			}
			*trace = append(*trace, "advance")
			return nil
		}
		snap.Interview = append(snap.Interview, ai.DialogTurn{Role: "doctor", Text: res.Reply})
		reply = res.Reply
	}
	return fmt.Errorf("问诊未在 %d 轮内收敛", maxInterviewTurns)
}

func treatmentPhase(ctx context.Context, d Deps, snap *ai.Snapshot, trace []string) (Outcome, error) {
	for {
		tp, err := d.Layer.Treatment(ctx, *snap)
		if err != nil {
			return Outcome{}, err
		}
		trace = append(trace, "treatment:"+string(tp.Plan))
		if tp.Plan == ai.PlanTreatment && !d.Caps[tp.RequiredCapability] {
			snap.Feedback = &ai.OrchestratorFeedback{LastReject: ai.RejectCapabilityMissing}
			continue
		}
		final := "ADVICE"
		if tp.Plan == ai.PlanReferral {
			final = "REFERRAL"
		}
		return Outcome{
			Final: final, Diagnosis: snap.Diagnosis, Plan: tp.Plan, Advice: tp.Advice, Trace: trace,
		}, nil
	}
}
