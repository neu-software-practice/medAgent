package medagent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"medagent/internal/ai"
)

func (s *Service) PatientSay(ctx context.Context, id, message string) (Step, error) {
	sess, err := s.get(id)
	if err != nil {
		return Step{}, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.lastActive = time.Now()
	if sess.phase == phDone || sess.phase == phClosed {
		return Step{}, ErrSessionClosed
	}
	if sess.phase != phInterview {
		return Step{}, ErrWrongStep
	}
	sess.snap.Interview = append(sess.snap.Interview, ai.DialogTurn{Role: "patient", Text: message})
	sess.addTurn("patient", message)
	return s.advance(ctx, sess)
}

func (s *Service) SupplyTestResults(ctx context.Context, id string, results []TestResult) (Step, error) {
	sess, err := s.get(id)
	if err != nil {
		return Step{}, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.lastActive = time.Now()
	if sess.phase == phDone || sess.phase == phClosed {
		return Step{}, ErrSessionClosed
	}
	if sess.phase != phAwaitTests {
		return Step{}, ErrWrongStep
	}
	sess.snap.TestResults = append(sess.snap.TestResults, testResultsToAI(results)...)
	for _, r := range results {
		sess.addTurn("test_result", r.Item+": "+r.Value)
	}
	sess.phase = phTriage
	return s.advance(ctx, sess)
}

func (s *Service) SupplyPurchaseResult(ctx context.Context, id string, results []DrugPurchase) (Step, error) {
	sess, err := s.get(id)
	if err != nil {
		return Step{}, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.lastActive = time.Now()
	if sess.phase == phDone || sess.phase == phClosed {
		return Step{}, ErrSessionClosed
	}
	if sess.phase != phAwaitPurchase {
		return Step{}, ErrWrongStep
	}
	bought := map[string]int{}
	for _, r := range results {
		if r.Bought {
			bought[r.Name] = r.Quantity
		} else {
			sess.snap.Refusals = append(sess.snap.Refusals, ai.RefusalRecord{What: "med_pay:" + r.Name})
		}
		sess.addTurn("purchase_result", fmt.Sprintf("%s 购买=%v 数量=%d", r.Name, r.Bought, r.Quantity))
	}
	if b, _ := json.Marshal(bought); b != nil {
		sess.snap.Subjective["购药结果"] = string(b)
	}
	sess.purchased = true
	sess.snap.Feedback = &ai.OrchestratorFeedback{NextExpected: "据购药结果给最终医嘱，勿重复开药"}
	sess.phase = phTreatment
	st, err := s.advance(ctx, sess)
	sess.snap.Feedback = nil
	return st, err
}

// advance 从当前 phase 推进到下一个需外部输入或终态。已持有 sess.mu。
func (s *Service) advance(ctx context.Context, sess *session) (Step, error) {
	cctx := withVisit(ctx, sess.id)
	for {
		switch sess.phase {
		case phInterview:
			sess.iTurns++
			if sess.iTurns > maxInterviewTurns {
				return Step{}, fmt.Errorf("%w: 问诊未收敛", ErrUpstream)
			}
			res, err := s.layer.Interview(cctx, sess.snap)
			if err != nil {
				return Step{}, ctxOrMap(cctx, err)
			}
			if res.Advance == nil {
				sess.snap.Interview = append(sess.snap.Interview, ai.DialogTurn{Role: "doctor", Text: res.Reply})
				sess.addTurn("doctor", res.Reply)
				return Step{Kind: StepAsk, DoctorSay: res.Reply}, nil
			}
			for k, v := range res.Advance.Subjective {
				sess.snap.Subjective[k] = v
			}
			sess.snap.Feedback = nil
			sess.phase = phTriage

		case phTriage:
			sess.tRounds++
			if sess.tRounds > maxTriageRounds {
				return Step{}, fmt.Errorf("%w: 收敛环未收敛", ErrUpstream)
			}
			td, err := s.layer.Triage(cctx, sess.snap)
			if err != nil {
				return Step{}, ctxOrMap(cctx, err)
			}
			switch td.Decision {
			case ai.TriageConfirm:
				sess.snap.Diagnosis = td.Diagnosis
				sess.addTurn("diagnosis", fmt.Sprintf("%s（%.2f）", td.Diagnosis.Name, td.Diagnosis.Confidence))
				sess.phase = phTreatment
			case ai.TriageInterview:
				sess.snap.Feedback = &ai.OrchestratorFeedback{MissingHint: td.MissingSubjective}
				sess.phase = phInterview
				// 立即再问一次拿追问句
				res, err := s.layer.Interview(cctx, sess.snap)
				if err != nil {
					return Step{}, ctxOrMap(cctx, err)
				}
				sess.snap.Feedback = nil
				if res.Advance != nil { // 模型直接补够：合并后继续收敛
					for k, v := range res.Advance.Subjective {
						sess.snap.Subjective[k] = v
					}
					sess.phase = phTriage
					continue
				}
				sess.snap.Interview = append(sess.snap.Interview, ai.DialogTurn{Role: "doctor", Text: res.Reply})
				sess.addTurn("doctor", res.Reply)
				return Step{Kind: StepAsk, DoctorSay: res.Reply}, nil
			case ai.TriageTest:
				sess.phase = phAwaitTests
				sess.addTurn("test_request", "血常规")
				return Step{Kind: StepNeedTests, TestItems: []string{"血常规"}}, nil
			default:
				return Step{}, fmt.Errorf("%w: 非法 triage %q", ErrModelOutput, td.Decision)
			}

		case phTreatment:
			sess.pRounds++
			if sess.pRounds > maxTreatmentRounds {
				return Step{}, fmt.Errorf("%w: 处置环未收敛", ErrUpstream)
			}
			tp, err := s.layer.Treatment(cctx, sess.snap)
			if err != nil {
				return Step{}, ctxOrMap(cctx, err)
			}
			if tp.Plan == ai.PlanTreatment && !s.cfg.Caps[tp.RequiredCapability] {
				sess.snap.Feedback = &ai.OrchestratorFeedback{LastReject: ai.RejectCapabilityMissing}
				continue
			}
			sess.snap.Feedback = nil
			if tp.Plan == ai.PlanMedication && !sess.purchased {
				sess.phase = phAwaitPurchase
				orders := ordersFromMeds(tp.Medications)
				sess.addTurn("purchase_request", fmt.Sprintf("%v", orders))
				return Step{Kind: StepPurchase, Orders: orders}, nil
			}
			// 已购药后重决策（含模型再返 MEDICATION）一律终决，不二次购药。
			return s.finish(sess, tp), nil

		default:
			return Step{}, ErrWrongStep
		}
	}
}

// finish 落终态：写 record.Outcome/EndedAt，phase=done。已持有 sess.mu。
func (s *Service) finish(sess *session, tp ai.TreatmentPlan) Step {
	r := resultFromPlan(tp, sess.snap.Diagnosis)
	sess.addTurn("advice", tp.Advice)
	sess.phase = phDone
	t := nowSec()
	sess.record.EndedAt = &t
	sess.record.Outcome = &r
	return Step{Kind: StepDone, Result: &r}
}

// ctxOrMap：ctx 取消优先以原始 ctx 错误返回，否则归一内部错误。
func ctxOrMap(ctx context.Context, err error) error {
	if ce := ctx.Err(); ce != nil {
		return ce
	}
	return mapErr(err)
}
