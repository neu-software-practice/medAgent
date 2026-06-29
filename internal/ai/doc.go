// Package ai 实现无人医院患者端的 AI 决策层：一个单 agent 工具循环引擎（Engine）
// 驱动 ask_patient/order_test/query_drug_spec/purchase_drug/refer/finish 六个工具，
// 外加一个与主流程并发的急症守护（Guardian）。
//
// AI 无状态：主决策的上下文（transcript）由调用方持有、每步传入；引擎只负责"调一次 LLM、
// 解码选定工具、做结构校验与包内自纠"。让出/续跑、轮数预算、上下文压缩等编排归调用方。
package ai
