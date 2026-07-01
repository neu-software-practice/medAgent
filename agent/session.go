package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"medagent/internal/ai"
)

// PatientSay 处理患者发言：未开始时作首条 user 消息（并入资料/历史前缀），
// 等待 ask_patient 回答时作 tool 结果回填；随后推进 agent 循环。
func (s *Service) PatientSay(ctx context.Context, id, message string) (Step, error) {
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
	// 患者发言只在"未开始"或"模型在等患者回答(ask_patient)"时合法。
	if sess.pending != nil && sess.pending.name != "ask_patient" {
		return Step{}, ErrWrongStep
	}

	cp := sess.checkpoint()
	if sess.pending == nil {
		content := message
		if pre := sess.contextPrefix(); pre != "" {
			content = pre + "\n\n患者：" + message
		}
		sess.transcript = append(sess.transcript, ai.Message{Role: "user", Content: content})
	} else {
		sess.transcript = append(sess.transcript, ai.Message{Role: "tool", ToolCallID: sess.pending.id, Content: message})
		sess.pending = nil
	}
	sess.snap.Interview = append(sess.snap.Interview, ai.DialogTurn{Role: "patient", Text: message})
	sess.addTurn("patient", message)

	st, err := s.guarded(ctx, sess, ai.Event{Kind: "dialog", Data: message}, func(c context.Context) (Step, error) {
		return s.drive(c, sess)
	})
	if err != nil && !sess.closed() {
		sess.restore(cp)
	}
	return st, err
}

// SupplyTestResults 回填检验结果（响应 NEED_TESTS 后）作为 order_test 的 tool 结果，并续跑。
func (s *Service) SupplyTestResults(ctx context.Context, id string, results []TestResult) (Step, error) {
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
	if sess.pending == nil || sess.pending.name != "order_test" {
		return Step{}, ErrWrongStep
	}

	cp := sess.checkpoint()
	sess.transcript = append(sess.transcript, ai.Message{Role: "tool", ToolCallID: sess.pending.id, Content: renderTestResults(results)})
	sess.pending = nil
	sess.snap.TestResults = append(sess.snap.TestResults, testResultsToAI(results)...)
	for _, r := range results {
		sess.addTurn("test_result", r.Item+": "+r.Value)
	}

	st, err := s.guarded(ctx, sess, ai.Event{Kind: "test_result", Data: results}, func(c context.Context) (Step, error) {
		return s.drive(c, sess)
	})
	if err != nil && !sess.closed() {
		sess.restore(cp)
	}
	return st, err
}

// SupplyDrugInfo 回填药品规格（响应 DRUG_QUERY 后）作为 query_drug_spec 的 tool 结果，并续跑。
func (s *Service) SupplyDrugInfo(ctx context.Context, id string, infos []DrugInfo) (Step, error) {
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
	if sess.pending == nil || sess.pending.name != "query_drug_spec" {
		return Step{}, ErrWrongStep
	}

	cp := sess.checkpoint()
	var b strings.Builder
	b.WriteString("药品规格：")
	for _, di := range infos {
		fmt.Fprintf(&b, "%s：%s；", di.Name, di.Spec)
		sess.addTurn("drug_info", di.Name+": "+di.Spec)
	}
	sess.transcript = append(sess.transcript, ai.Message{Role: "tool", ToolCallID: sess.pending.id, Content: b.String()})
	sess.pending = nil
	sess.drugInfoSupplied = true

	st, err := s.guarded(ctx, sess, ai.Event{Kind: "drug_info", Data: infos}, func(c context.Context) (Step, error) {
		return s.drive(c, sess)
	})
	if err != nil && !sess.closed() {
		sess.restore(cp)
	}
	return st, err
}

// SupplyPurchaseResult 回报购药结果（响应 PURCHASE 后）作为 purchase_drug 的 tool 结果，并续跑。
// 拒绝（bought=false）记入 Refusals，模型据回填结果给最终医嘱。
func (s *Service) SupplyPurchaseResult(ctx context.Context, id string, results []DrugPurchase) (Step, error) {
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
	if sess.pending == nil || sess.pending.name != "purchase_drug" {
		return Step{}, ErrWrongStep
	}

	cp := sess.checkpoint()
	var b strings.Builder
	b.WriteString("购药结果：")
	for _, r := range results {
		if !r.Bought {
			sess.snap.Refusals = append(sess.snap.Refusals, ai.RefusalRecord{What: "med_pay:" + r.Name})
		}
		fmt.Fprintf(&b, "%s 购买=%v 数量=%d；", r.Name, r.Bought, r.Quantity)
		sess.addTurn("purchase_result", fmt.Sprintf("%s 购买=%v 数量=%d", r.Name, r.Bought, r.Quantity))
	}
	sess.transcript = append(sess.transcript, ai.Message{Role: "tool", ToolCallID: sess.pending.id, Content: b.String()})
	sess.pending = nil
	sess.purchased = true

	st, err := s.guarded(ctx, sess, ai.Event{Kind: "purchase_result", Data: results}, func(c context.Context) (Step, error) {
		return s.drive(c, sess)
	})
	if err != nil && !sess.closed() {
		sess.restore(cp)
	}
	return st, err
}

// drive 推进 agent 决策：每步调引擎产出一个工具调用，要么让出边界（持久化 pending、返回 Step），
// 要么终态收尾。多数情况一步即返回；仅在拦截"已购药后再次购药"时注入纠正结果并续跑（受
// maxInternalSteps 约束）。step 预算撞顶则关闭会话。已持有 sess.mu。
func (s *Service) drive(ctx context.Context, sess *session) (Step, error) {
	cctx := withVisit(ctx, sess.id)
	for i := 0; i < maxInternalSteps; i++ {
		if sess.steps >= maxSteps {
			sess.status = stClosed
			return Step{}, fmt.Errorf("%w: 诊疗步数超限未收敛", ErrUpstream)
		}
		s.maybeCompact(cctx, sess)

		res, err := s.engine.Step(cctx, sess.transcript)
		if err != nil {
			return Step{}, ctxOrMap(cctx, err)
		}
		sess.steps++
		sess.lastPromptTokens = res.PromptTokens
		sess.transcript = append(sess.transcript, res.Assistant)

		if res.Terminal != nil {
			return s.finishTerminal(sess, res.Terminal), nil
		}
		b := res.Boundary
		// 幂等护栏：已开检验后又调 order_test → 注入纠正结果、要求据已有结果继续，绝不二次开检验。
		if b.Kind == ai.BoundaryTest && sess.tested {
			sess.addTurn("warn", "模型在已开检验后再次请求检验，已拦截并要求继续问诊")
			sess.transcript = append(sess.transcript, ai.Message{Role: "tool", ToolCallID: b.ToolCall.ID,
				Content: "已开过检验（血常规），请勿重复开检验；请根据已获取的信息继续问诊或给出诊断（finish）。"})
			continue
		}
		// 幂等护栏：已购药后又调 purchase_drug → 注入纠正结果、要求据已购结果收尾，绝不二次购药。
		if b.Kind == ai.BoundaryPurchase && sess.purchased {
			sess.addTurn("warn", "模型在已购药后再次请求购药，已拦截并要求据已购结果收尾")
			sess.transcript = append(sess.transcript, ai.Message{Role: "tool", ToolCallID: b.ToolCall.ID,
				Content: "已完成购药，请勿重复购药；请根据已购结果直接给最终医嘱（finish）。"})
			continue
		}
		return s.yieldBoundary(sess, b)
	}
	sess.status = stClosed
	return Step{}, fmt.Errorf("%w: 内部纠正未收敛", ErrUpstream)
}

// maybeCompact 在上下文占用超过阈值时，调 LLM 把较早 transcript 压成摘要。
// 优先用上一步 provider 的真实 usage，无则零依赖估算；压缩失败则跳过、不影响诊疗。
// 切点避开 tool_call/tool_result 之间，避免保留孤儿结果。已持有 sess.mu。
func (s *Service) maybeCompact(ctx context.Context, sess *session) {
	if s.ctxTokens <= 0 || len(sess.transcript) <= compactKeepRecent+1 {
		return
	}
	occupied := sess.lastPromptTokens
	if occupied == 0 {
		occupied = ai.EstimateTokens(sess.transcript)
	}
	if occupied < int(s.compactRatio*float64(s.ctxTokens)) {
		return
	}
	cut := len(sess.transcript) - compactKeepRecent
	for cut > 0 && sess.transcript[cut].Role == "tool" {
		cut--
	}
	if cut <= 0 {
		return
	}
	summary, err := s.engine.Summarize(ctx, sess.transcript[:cut])
	if err != nil || summary == "" {
		return
	}
	compacted := make([]ai.Message, 0, compactKeepRecent+1)
	compacted = append(compacted, ai.Message{Role: "user", Content: "【既往对话摘要】\n" + summary})
	sess.transcript = append(compacted, sess.transcript[cut:]...)
	sess.lastPromptTokens = 0 // 压缩后占用未知，下一步用真实 usage 重估
	sess.addTurn("compact", fmt.Sprintf("上下文压缩：%d 条原文→摘要+保留最近 %d 条", cut, compactKeepRecent))
}

// yieldBoundary 持久化 pending、渲染镜像/纪要，返回让出给后端的 Step。已持有 sess.mu。
func (s *Service) yieldBoundary(sess *session, b *ai.Boundary) (Step, error) {
	sess.pending = &pendingCall{id: b.ToolCall.ID, name: b.ToolCall.Name}
	switch b.Kind {
	case ai.BoundaryAsk:
		sess.snap.Interview = append(sess.snap.Interview, ai.DialogTurn{Role: "doctor", Text: b.Question})
		sess.addTurn("doctor", b.Question)
		return Step{Kind: StepAsk, DoctorSay: b.Question}, nil
	case ai.BoundaryTest:
		sess.addTurn("test_request", "血常规")
		sess.tested = true
		return Step{Kind: StepNeedTests, TestItems: []string{"血常规"}}, nil
	case ai.BoundaryDrugQuery:
		sess.addTurn("drug_query", fmt.Sprintf("%v", b.Names))
		return Step{Kind: StepDrugQuery, DrugNames: b.Names}, nil
	case ai.BoundaryPurchase:
		orders := ordersFromAI(b.Orders)
		for i := range orders {
			if orders[i].Quantity <= 0 {
				sess.addTurn("warn", fmt.Sprintf("药品「%s」模型未给出有效盒数（%d），按 1 盒兜底，后端需复核", orders[i].Name, orders[i].Quantity))
				orders[i].Quantity = 1
			}
		}
		sess.addTurn("purchase_request", fmt.Sprintf("%v", orders))
		return Step{Kind: StepPurchase, Orders: orders}, nil
	default:
		return Step{}, fmt.Errorf("%w: 未知边界 %q", ErrModelOutput, b.Kind)
	}
}

// finishTerminal 落终态：写诊断/医嘱/Outcome，status=done。已持有 sess.mu。
func (s *Service) finishTerminal(sess *session, t *ai.Terminal) Step {
	if t.Diagnosis != nil {
		sess.snap.Diagnosis = t.Diagnosis
		sess.addTurn("diagnosis", fmt.Sprintf("%s（%.2f）", t.Diagnosis.Name, t.Diagnosis.Confidence))
	}
	r := resultFromPlan(t.Plan, t.Diagnosis)
	sess.addTurn("advice", t.Plan.Advice)
	sess.status = stDone
	tm := nowSec()
	sess.record.EndedAt = &tm
	sess.record.Outcome = &r
	return Step{Kind: StepDone, Result: &r}
}

// checkpoint 捕获一次推进前的可回滚状态；推进失败（非终态/关闭）时 restore 还原，便于后端重试同一步。
// transcript 存完整拷贝（压缩会缩短 transcript，长度法回滚不可靠）；snap/record 仅增长，用长度回滚。
type checkpoint struct {
	transcript                    []ai.Message
	nInterview, nTests, nRefusals int
	nTurns                        int
	pending                       *pendingCall
	purchased, drugInfo, tested   bool
	lastTokens                    int
}

func (sess *session) checkpoint() checkpoint {
	return checkpoint{
		transcript: append([]ai.Message(nil), sess.transcript...),
		nInterview: len(sess.snap.Interview),
		nTests:     len(sess.snap.TestResults),
		nRefusals:  len(sess.snap.Refusals),
		nTurns:     len(sess.record.Turns),
		pending:    sess.pending,
		purchased:  sess.purchased,
		tested:     sess.tested,
		drugInfo:   sess.drugInfoSupplied,
		lastTokens: sess.lastPromptTokens,
	}
}

func (sess *session) restore(c checkpoint) {
	sess.transcript = c.transcript
	sess.snap.Interview = sess.snap.Interview[:c.nInterview]
	sess.snap.TestResults = sess.snap.TestResults[:c.nTests]
	sess.snap.Refusals = sess.snap.Refusals[:c.nRefusals]
	sess.record.Turns = sess.record.Turns[:c.nTurns]
	sess.pending = c.pending
	sess.purchased = c.purchased
	sess.tested = c.tested
	sess.drugInfoSupplied = c.drugInfo
	sess.lastPromptTokens = c.lastTokens
}

// contextPrefix 把患者资料/历史渲染成首条 user 消息的前缀（系统提示词固定，按会话数据走前缀）。
func (sess *session) contextPrefix() string {
	var b strings.Builder
	if len(sess.snap.Profile) > 0 {
		fmt.Fprintf(&b, "【患者资料】%s\n", sess.snap.Profile)
	}
	if sess.snap.History != "" {
		fmt.Fprintf(&b, "【历史就诊记录】\n%s\n", sess.snap.History)
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderTestResults(results []TestResult) string {
	parts := make([]string, 0, len(results))
	for _, r := range results {
		parts = append(parts, r.Item+": "+r.Value)
	}
	return "检验结果：" + strings.Join(parts, "；")
}

// ctxOrMap：ctx 取消优先以原始 ctx 错误返回，否则归一内部错误。
func ctxOrMap(ctx context.Context, err error) error {
	if ce := ctx.Err(); ce != nil {
		return ce
	}
	return mapErr(err)
}
