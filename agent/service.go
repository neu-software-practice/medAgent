package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"medagent/internal/ai"
	"medagent/internal/consultlog"
)

type phase int

const (
	phInterview phase = iota
	phTriage
	phAwaitTests
	phTreatment
	phAwaitDrugInfo
	phAwaitPurchase
	phDone
	phClosed
)

const (
	maxInterviewTurns  = 20
	maxTriageRounds    = 10
	maxTreatmentRounds = 6 // 含 DRUG_QUERY 轮（查规格/购药/终决占 3 轮），余量留给能力缺失重决策
)

type session struct {
	id                       string
	snap                     ai.Snapshot
	phase                    phase
	iTurns, tRounds, pRounds int
	purchased                bool // 已走过购药回报，处置重决策不再二次购药
	drugInfoSupplied         bool // 已回填药品规格，处置据规格定盒数
	record                   SessionRecord
	lastActive               time.Time // 由 sess.mu 保护；reapOnce 用 TryLock 读，写方持 sess.mu
	mu                       sync.Mutex
}

func (sess *session) addTurn(kind, text string) {
	sess.record.Turns = append(sess.record.Turns, RecordedTurn{At: nowSec(), Kind: kind, Text: text})
}

type Service struct {
	cfg      Config
	layer    ai.DecisionLayer
	guardian ai.Guardian
	ttl      time.Duration

	mu       sync.RWMutex
	sessions map[string]*session

	stop chan struct{}
	wg   sync.WaitGroup
}

func newService(cfg Config, layer ai.DecisionLayer, guardian ai.Guardian) *Service {
	ttl := cfg.SessionTTL
	if ttl == 0 {
		ttl = 30 * time.Minute
	}
	s := &Service{cfg: cfg, layer: layer, guardian: guardian, ttl: ttl,
		sessions: map[string]*session{}, stop: make(chan struct{})}
	s.wg.Add(1)
	go s.reaper()
	return s
}

func (s *Service) Close() error {
	close(s.stop)
	s.wg.Wait()
	return nil
}

func newSessionID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
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
		phase:      phInterview,
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

// withVisit 在 ctx 上绑 sessionID 供日志归档（consultlog 用；FakeLLM 忽略）。
func withVisit(ctx context.Context, id string) context.Context {
	return consultlog.WithVisitID(ctx, id)
}
