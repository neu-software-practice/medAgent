package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"medagent/internal/ai"
	"medagent/internal/consultlog"
)

type sessionStatus int

const (
	stActive sessionStatus = iota
	stDone
	stClosed
)

const (
	// maxSteps 是一次会话允许的 agent 决策步数（每步一次 LLM 工具调用）上限——step 预算护栏，
	// 替代旧版按问诊/收敛/处置分别计数。撞顶强制关闭会话。
	maxSteps = 40
	// maxInternalSteps 是单次 drive 内允许的连续决策步数上限（防内部纠正死循环）。
	maxInternalSteps = 8
	// compactKeepRecent 是上下文压缩时保留的最近原文消息条数，更早的折成摘要。
	compactKeepRecent = 6
)

// pendingCall 记录一次让出后等待后端回填的工具调用：name 决定哪个 Supply* 合法，id 用于配对 tool_result。
type pendingCall struct {
	id   string
	name string
}

type session struct {
	id               string
	snap             ai.Snapshot  // 急症守护镜像（Interview/TestResults/Diagnosis/Refusals/Profile/History）
	transcript       []ai.Message // 主 agent 上下文（含 tool_calls / tool 结果）
	status           sessionStatus
	pending          *pendingCall // 非空=挂起等回填；nil=未开始或刚终态
	steps            int          // 已消耗 step 预算
	purchased        bool         // 已走过购药回报，防 resume 二次购药
	tested           bool         // 已开过检验，防二次开检验
	drugInfoSupplied bool         // 已回填药品规格
	lastPromptTokens int          // 上一步 provider 返回的真实输入 token（压缩阈值用）
	record           SessionRecord
	lastActive       time.Time // 由 sess.mu 保护；reapOnce 用 TryLock 读
	mu               sync.Mutex
}

func (sess *session) addTurn(kind, text string) {
	sess.record.Turns = append(sess.record.Turns, RecordedTurn{At: nowSec(), Kind: kind, Text: text})
}

// closed 表示会话已不可推进（终态或被关闭）。
func (sess *session) closed() bool { return sess.status == stDone || sess.status == stClosed }

type Service struct {
	cfg          Config
	engine       *ai.Engine
	guardian     ai.Guardian
	ttl          time.Duration
	ctxTokens    int     // 模型上下文窗口（token）
	compactRatio float64 // 压缩触发占用比例

	mu       sync.RWMutex
	sessions map[string]*session

	stop chan struct{}
	wg   sync.WaitGroup
}

func newService(cfg Config, engine *ai.Engine, guardian ai.Guardian) *Service {
	ttl := cfg.SessionTTL
	if ttl == 0 {
		ttl = 30 * time.Minute
	}
	ctxTokens := cfg.ContextTokens
	if ctxTokens == 0 {
		ctxTokens = contextWindowFor(cfg.Model)
	}
	ratio := cfg.CompactRatio
	if ratio <= 0 || ratio >= 1 {
		ratio = 0.6
	}
	s := &Service{cfg: cfg, engine: engine, guardian: guardian, ttl: ttl,
		ctxTokens: ctxTokens, compactRatio: ratio,
		sessions: map[string]*session{}, stop: make(chan struct{})}
	s.wg.Add(1)
	go s.reaper()
	return s
}

// contextWindowFor 按模型名粗匹配上下文窗口（token），不命中给保守默认。
func contextWindowFor(model string) int {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "gpt-5"), strings.Contains(m, "gpt-4.1"), strings.Contains(m, "gpt-4o"), strings.Contains(m, "gpt-4.5"):
		return 128000
	case strings.Contains(m, "gpt-4"):
		return 128000
	case strings.Contains(m, "deepseek"):
		return 64000
	case strings.Contains(m, "qwen"):
		return 32000
	default:
		return 32000
	}
}

func (s *Service) Close() error {
	close(s.stop)
	s.wg.Wait()
	return nil
}

func newSessionID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read 在 Linux 上不会失败；若极端情况出错，用时间纳秒作回退熵。
		now := time.Now()
		_ = copy(b[:], fmt.Sprintf("%06d", now.UnixNano()%1_000_000))
	}
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

func (s *Service) Start(profile map[string]any, initial bool, prior []SessionRecord) (string, error) {
	id := newSessionID()
	var prof json.RawMessage
	if profile != nil {
		b, err := json.Marshal(profile)
		if err != nil {
			return "", fmt.Errorf("medagent: marshal profile: %w", err)
		}
		prof = b
	}
	sess := &session{
		id:         id,
		status:     stActive,
		snap:       ai.Snapshot{Subjective: map[string]any{}, Profile: prof, History: renderHistory(prior)},
		record:     SessionRecord{SessionID: id, Initial: initial, StartedAt: nowSec(), Profile: prof},
		lastActive: time.Now(),
	}
	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()
	return id, nil
}

func (s *Service) get(id string) (*session, error) {
	s.mu.RLock()
	sess, ok := s.sessions[id]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrSessionNotFound
	}
	return sess, nil
}

func (s *Service) Export(id string) (SessionRecord, error) {
	sess, err := s.get(id)
	if err != nil {
		return SessionRecord{}, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	cp := sess.record
	cp.Turns = append([]RecordedTurn(nil), sess.record.Turns...)
	return cp, nil
}

func (s *Service) End(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

func (s *Service) reaper() {
	defer s.wg.Done()
	tk := time.NewTicker(time.Minute)
	defer tk.Stop()
	for {
		select {
		case <-s.stop:
			return
		case now := <-tk.C:
			s.reapOnce(now)
		}
	}
}

func (s *Service) reapOnce(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.sessions {
		if sess.mu.TryLock() { // 忙会话本轮跳过
			expired := now.Sub(sess.lastActive) > s.ttl
			sess.mu.Unlock()
			if expired {
				delete(s.sessions, id)
			}
		}
	}
}

// withVisit 在 ctx 上绑 sessionID 供日志归档（consultlog 用；fake 忽略）。
func withVisit(ctx context.Context, id string) context.Context {
	return consultlog.WithVisitID(ctx, id)
}
