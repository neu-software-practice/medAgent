// Package ai 实现无人医院患者端的 AI 决策层：问诊 / triage / 处置 / 急症守护
// 四个无状态 agent，及其 typed intent 契约、LLM 抽象与上下文构建。
// AI 无状态，每次调用从 Snapshot 重建上下文；语义校验与编排归调用方。
package ai
