package agent

import (
	"context"
	"encoding/json"
	"time"

	"medagent/internal/ai"
)

type guardResult struct {
	ei  ai.EmergencyInterrupt
	hit bool
	err error
}

// guarded 并发跑守护与 main；守护命中即取消 main 返回 EMERGENCY，否则返回 main 结果。
// 守护错误 fail-open（忽略，等 main）。已持有 sess.mu。
// 守护可能等到其 LLM 调用返回，调用方须通过 ctx 传 deadline 以防慢守护拖住会话。
//
// 并发保证：
//   - gch/mch 均 buffered(1)，两个子 goroutine 发完即退，不泄漏。
//   - snap 在启动 goroutine 前从 sess.snap 拷贝，消除守护与 advance 对 sess.snap 的并发读写。
//   - 守护使用外层 ctx（不被 main 完成取消），确保其 LLM 调用能得到真实判断结果；
//     main 使用独立的 mainCtx，守护命中时可立即 cancel，避免 advance 无谓继续。
//   - mch 分支：等守护完成后再返回（守护 fail-open 不阻塞：已持有结果或 ctx 超时退出）。
func (s *Service) guarded(ctx context.Context, sess *session, ev ai.Event, main func(context.Context) (Step, error)) (Step, error) {
	if s.cfg.DisableGuardian {
		return main(ctx)
	}

	// mainCtx 只用于取消 advance，不用于取消守护。
	mainCtx, cancelMain := context.WithCancel(ctx)
	defer cancelMain()

	// 守护用 cloneSnapshot 的深拷贝快照，与 advance 写 sess.snap 无共享、无竞争。
	snap := cloneSnapshot(sess.snap)

	gch := make(chan guardResult, 1)
	go func() {
		// 守护用外层 ctx：主决策先完成时不被取消，能返回真实判断。
		ei, hit, err := s.guardian.Assess(withVisit(ctx, sess.id), snap, ev)
		gch <- guardResult{ei, hit, err}
	}()

	mch := make(chan struct {
		st  Step
		err error
	}, 1)
	go func() {
		st, err := main(mainCtx)
		mch <- struct {
			st  Step
			err error
		}{st, err}
	}()

	select {
	case g := <-gch:
		if g.err == nil && g.hit {
			cancelMain()
			<-mch // 排空（advance 看到 ctx 取消后快速退出）
			return s.emergency(sess, g.ei.Reason), nil
		}
		// 守护放行或出错（fail-open）→ 等 main
		m := <-mch
		return m.st, m.err
	case m := <-mch:
		// main 先完成；等守护得出真实结论（守护使用外层 ctx，未被取消）。
		g := <-gch
		cancelMain()
		if g.err == nil && g.hit {
			return s.emergency(sess, g.ei.Reason), nil
		}
		return m.st, m.err
	}
}

func (s *Service) emergency(sess *session, reason string) Step {
	sess.addTurn("emergency", reason)
	sess.status = stClosed
	sess.record.Outcome = nil  // 急症终止：清掉可能已被 finish 写入的完成态，避免记录不一致
	sess.snap.Diagnosis = nil // 同步清理快照中的诊断
	t := nowSec()
	sess.record.EndedAt = &t
	return Step{Kind: StepEmergency, Emergency: reason}
}

// cloneSnapshot 深拷贝守护并发期间可能被 advance 写的字段，消除与 advance 的数据竞争。
func cloneSnapshot(s ai.Snapshot) ai.Snapshot {
	c := s // 复制值字段与 header；下面替换会被并发写的引用字段
	c.Interview = append([]ai.DialogTurn(nil), s.Interview...)
	c.TestResults = append([]ai.TestResult(nil), s.TestResults...)
	c.Refusals = append([]ai.RefusalRecord(nil), s.Refusals...)
	if s.Subjective != nil {
		c.Subjective = make(map[string]any, len(s.Subjective))
		for k, v := range s.Subjective {
			c.Subjective[k] = v
		}
	}
	if s.Profile != nil {
		c.Profile = append(json.RawMessage(nil), s.Profile...)
	}
	if s.Diagnosis != nil {
		dg := *s.Diagnosis
		c.Diagnosis = &dg
	}
	return c
}

// ReportVitals 上报体征并触发守护评估。守护可能等到其 LLM 调用返回，调用方须通过 ctx 传 deadline 以防慢守护拖住会话。
func (s *Service) ReportVitals(ctx context.Context, id string, vitals map[string]any) (Step, error) {
	sess, err := s.get(id)
	if err != nil {
		return Step{}, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.lastActive = time.Now()
	if sess.closed() {
		return Step{}, ErrSessionClosed
	}
	if s.cfg.DisableGuardian {
		return Step{Kind: StepOK}, nil
	}
	ev := ai.Event{Kind: "vital", Data: vitals}
	ei, hit, gerr := s.guardian.Assess(withVisit(ctx, sess.id), sess.snap, ev)
	if gerr == nil && hit {
		return s.emergency(sess, ei.Reason), nil
	}
	return Step{Kind: StepOK}, nil
}
