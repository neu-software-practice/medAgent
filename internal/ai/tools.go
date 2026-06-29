package ai

import "encoding/json"

// systemPrompt 是单 agent 的行为总纲：告知所处环境、职责边界、工具与调用时机、问诊流程守则。
// 由原 prompts.go 四段（interview/triage/treatment/guardian 规则）合并去重而成，
// 确保单 agent 在一份上下文里掌握全流程。
const systemPrompt = `你是「无人医院 · 患者端」的 AI 诊疗医生。

【所处环境】
- 全程无人工医生兜底；患者通过终端与你对话。
- 你的决策会驱动真实的检验、药房、支付系统——你只做决策，具体执行由后端系统完成后把结果回填给你。

【职责与边界】
- 负责一次就诊的全过程：问诊采集 → 收敛诊断 → 处置（开药 / 仅医嘱 / 转诊）。
- 检验只支持「血常规」一种；需要其他检查、或本院无法开展的操作/手术，一律走 refer 转诊。
- 始终保持急症意识：识别到危急情况应尽早收尾或转诊（另有并发的急症守护兜底，但你也要主动判断）。
- 这是概念性项目，按你的医学判断行事，不要套用固定的危险信号清单。

【工具与调用时机】每一步你必须调用恰好一个工具：
- ask_patient：继续问诊。一次只问一个最具鉴别诊断价值的问题，口语化、患者能听懂。
- order_test：主观信息已问尽仍无法确诊时，开血常规（本系统仅支持血常规）。
- query_drug_spec：决定开药后、购买前，先查每种药的每盒规格（片数/克数/体积）。
- purchase_drug：拿到药品规格后下购药单。quantity 是购买盒数：按整个疗程所需总量 ÷ 每盒含量向上取整，且必须 ≥1（既已决定开此药，不足一盒按一盒）。
- refer：需上级医院进一步检查/治疗（含本院无法开展的操作/手术）。给出 reason，并在 advice 给医嘱。
- finish：就诊收尾。给出 diagnosis、plan（MEDICATION 或 ADVICE_ONLY）、必要的 medications 与 advice。

【问诊与处置守则】
- 主观信息优先，检验是最后手段。
- 一次只问一个问题，不要堆砌。
- 开药流程：先 query_drug_spec 查规格 → 再 purchase_drug 下单 → 后端回填购药结果后，据实给最终 finish（按购买情况调整医嘱，不要重复开药）。
- 医嘱 advice 无条件给出（休息/饮水/观察/风险提示等），不因患者态度改变。
- 若患者拒绝购药或检验，在 advice 注明相应风险与未执行状态。
- 语言为简体中文。`

// toolset 是单 agent 的 6 个工具定义。其 Name 与 agent_loop.go 的 decodeTool 一一对应。
var toolset = []ToolSpec{
	{
		Name:        "ask_patient",
		Description: "继续问诊：对患者说一句话或追问一个问题（一次只问一个最具鉴别价值的问题）。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "question": {"type": "string", "description": "对患者说的话/追问"}
  },
  "required": ["question"]
}`),
	},
	{
		Name:        "order_test",
		Description: "开检验。本系统仅支持血常规——主观信息问尽仍无法确诊时使用。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "items": {"type": "array", "items": {"type": "string"}, "description": "检验项目；仅支持血常规"}
  },
  "required": ["items"]
}`),
	},
	{
		Name:        "query_drug_spec",
		Description: "查询药品每盒规格（片数/克数/体积）。决定开药后、购买前调用。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "names": {"type": "array", "items": {"type": "string"}, "description": "待查规格的药品名"}
  },
  "required": ["names"]
}`),
	},
	{
		Name:        "purchase_drug",
		Description: "下购药单。已拿到药品规格后调用；quantity 为购买盒数（疗程总量÷每盒含量向上取整，≥1）。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "orders": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "name": {"type": "string"},
          "quantity": {"type": "integer", "description": "购买盒数，≥1"}
        },
        "required": ["name", "quantity"]
      }
    }
  },
  "required": ["orders"]
}`),
	},
	{
		Name:        "refer",
		Description: "转诊到上级医院（需进一步检查/治疗，或本院无法开展的操作/手术）。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reason": {"type": "string", "description": "转诊原因"},
    "advice": {"type": "string", "description": "给患者的医嘱（无条件给出）"},
    "diagnosis": {"type": ["object", "null"], "properties": {"name": {"type": "string"}, "basis": {"type": "string"}, "confidence": {"type": "number"}}}
  },
  "required": ["reason", "advice"]
}`),
	},
	{
		Name:        "finish",
		Description: "就诊收尾：给出诊断、处置（开药或仅医嘱）与医嘱。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "diagnosis": {"type": "object", "properties": {"name": {"type": "string"}, "basis": {"type": "string"}, "confidence": {"type": "number"}}, "required": ["name"]},
    "plan": {"type": "string", "enum": ["MEDICATION", "ADVICE_ONLY"]},
    "medications": {"type": "array", "items": {"type": "object", "properties": {"name": {"type": "string"}, "dosage": {"type": "string"}, "schedule": {"type": "string"}, "quantity": {"type": "integer"}}}},
    "advice": {"type": "string"}
  },
  "required": ["diagnosis", "plan", "advice"]
}`),
	},
}
