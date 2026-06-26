package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	nInterview, nTurns := len(sess.snap.Interview), len(sess.record.Turns)
	sess.snap.Interview = append(sess.snap.Interview, ai.DialogTurn{Role: "patient", Text: message})
	sess.addTurn("patient", message)
	st, err := s.guarded(ctx, sess, ai.Event{Kind: "dialog", Data: message}, func(c context.Context) (Step, error) {
		return s.advance(c, sess)
	})
	if err != nil && sess.phase != phDone && sess.phase != phClosed {
		sess.snap.Interview = sess.snap.Interview[:nInterview]
		sess.record.Turns = sess.record.Turns[:nTurns]
		sess.phase = phInterview
	}
	return st, err
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
	nTests, nTurns := len(sess.snap.TestResults), len(sess.record.Turns)
	sess.snap.TestResults = append(sess.snap.TestResults, testResultsToAI(results)...)
	for _, r := range results {
		sess.addTurn("test_result", r.Item+": "+r.Value)
	}
	sess.phase = phTriage
	st, err := s.guarded(ctx, sess, ai.Event{Kind: "test_result", Data: results}, func(c context.Context) (Step, error) {
		return s.advance(c, sess)
	})
	if err != nil && sess.phase != phDone && sess.phase != phClosed {
		sess.snap.TestResults = sess.snap.TestResults[:nTests]
		sess.record.Turns = sess.record.Turns[:nTurns]
		sess.phase = phAwaitTests
	}
	return st, err
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
	nRefusals, nTurns := len(sess.snap.Refusals), len(sess.record.Turns)
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
	st, err := s.guarded(ctx, sess, ai.Event{Kind: "purchase_result", Data: results}, func(c context.Context) (Step, error) {
		return s.advance(c, sess)
	})
	sess.snap.Feedback = nil
	if err != nil && sess.phase != phDone && sess.phase != phClosed {
		sess.snap.Refusals = sess.snap.Refusals[:nRefusals]
		sess.record.Turns = sess.record.Turns[:nTurns]
		delete(sess.snap.Subjective, "购药结果")
		sess.purchased = false
		sess.phase = phAwaitPurchase
	}
	return st, err
}

func (s *Service) SupplyDrugInfo(ctx context.Context, id string, infos []DrugInfo) (Step, error) {
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
	if sess.phase != phAwaitDrugInfo {
		return Step{}, ErrWrongStep
	}
	nTurns := len(sess.record.Turns)
	prevSpec, hadSpec := sess.snap.Subjective["药品规格"]
	var b strings.Builder
	for _, di := range infos {
		fmt.Fprintf(&b, "%s：%s；", di.Name, di.Spec)
		sess.addTurn("drug_info", di.Name+": "+di.Spec)
	}
	sess.snap.Subjective["药品规格"] = b.String()
	sess.drugInfoSupplied = true
	sess.phase = phTreatment
	st, err := s.guarded(ctx, sess, ai.Event{Kind: "drug_info", Data: infos}, func(c context.Context) (Step, error) {
		return s.advance(c, sess)
	})
	if err != nil && sess.phase != phDone && sess.phase != phClosed {
		sess.record.Turns = sess.record.Turns[:nTurns]
		if hadSpec {
			sess.snap.Subjective["药品规格"] = prevSpec
		} else {
			delete(sess.snap.Subjective, "药品规格")
		}
		sess.drugInfoSupplied = false
		sess.phase = phAwaitDrugInfo
	}
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
				sess.phase = phClosed
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
				sess.phase = phClosed
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
				// 此处 Interview 调用不计 iTurns，受 maxTriageRounds(10) 上界约束
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
				sess.phase = phClosed
				return Step{}, fmt.Errorf("%w: 处置环未收敛", ErrUpstream)
			}
			tp, err := s.layer.Treatment(cctx, sess.snap)
			if err != nil {
				return Step{}, ctxOrMap(cctx, err)
			}
			if tp.Plan == ai.PlanMedication && !sess.purchased {
				if !sess.drugInfoSupplied {
					names := drugNamesOf(tp.Medications)
					sess.phase = phAwaitDrugInfo
					sess.addTurn("drug_query", fmt.Sprintf("%v", names))
					return Step{Kind: StepDrugQuery, DrugNames: names}, nil
				}
				sess.phase = phAwaitPurchase
				orders := ordersFromMeds(tp.Medications)
				for i := range orders {
					if orders[i].Quantity <= 0 {
						sess.addTurn("warn", fmt.Sprintf("药品「%s」模型未给出有效盒数（%d），按 1 盒兜底，后端需复核", orders[i].Name, orders[i].Quantity))
						orders[i].Quantity = 1
					}
				}
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

func drugNamesOf(meds []ai.Medication) []string {
	out := make([]string, 0, len(meds))
	for _, m := range meds {
		out = append(out, m.Name)
	}
	return out
}
