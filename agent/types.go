// Package agent 是无人医院 AI 诊疗服务的唯一对外封装：以 HTTP/JSON 与外部组件通信，
// 内部按固定流程编排，多轮会话按 sessionID 内存持有。底层决策层在 internal/ 不外泄。
package agent

import (
	"encoding/json"
	"time"
)

type Config struct {
	Provider        string
	APIKey          string
	Model           string
	BaseURL         string
	LogDir          string
	Timeout         time.Duration
	SessionTTL      time.Duration
	DisableGuardian bool
}

type StepKind string

const (
	StepAsk       StepKind = "ASK"
	StepNeedTests StepKind = "NEED_TESTS"
	StepPurchase  StepKind = "PURCHASE"
	StepDrugQuery StepKind = "DRUG_QUERY"
	StepEmergency StepKind = "EMERGENCY"
	StepDone      StepKind = "DONE"
	StepOK        StepKind = "OK"
)

type Step struct {
	Kind      StepKind    `json:"kind"`
	DoctorSay string      `json:"doctor_say,omitempty"`
	TestItems []string    `json:"test_items,omitempty"`
	DrugNames []string    `json:"drug_names,omitempty"`
	Orders    []DrugOrder `json:"orders,omitempty"`
	Emergency string      `json:"emergency,omitempty"`
	Result    *Result     `json:"result,omitempty"`
}

type Result struct {
	Final       string       `json:"final"`
	Diagnosis   *Diagnosis   `json:"diagnosis,omitempty"`
	Plan        string       `json:"plan"`
	Medications []Medication `json:"medications,omitempty"`
	Advice      string       `json:"advice"`
}

type Diagnosis struct {
	Name       string  `json:"name"`
	Basis      string  `json:"basis"`
	Confidence float64 `json:"confidence"`
}

type Medication struct {
	Name     string `json:"name"`
	Dosage   string `json:"dosage"`
	Schedule string `json:"schedule"`
	Quantity int    `json:"quantity"`
}

type DrugOrder struct {
	Name     string `json:"name"`
	Quantity int    `json:"quantity"`
}

type DrugPurchase struct {
	Name     string `json:"name"`
	Bought   bool   `json:"bought"`
	Quantity int    `json:"quantity"`
}

type TestResult struct {
	Item  string `json:"item"`
	Value string `json:"value"`
}

type DrugInfo struct {
	Name string `json:"name"`
	Spec string `json:"spec"`
}

type SessionRecord struct {
	SessionID string          `json:"session_id"`
	Initial   bool            `json:"initial"`
	StartedAt time.Time       `json:"started_at"`
	EndedAt   *time.Time      `json:"ended_at,omitempty"`
	Profile   json.RawMessage `json:"profile,omitempty"`
	Turns     []RecordedTurn  `json:"turns"`
	Outcome   *Result         `json:"outcome,omitempty"`
}

type RecordedTurn struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
}
