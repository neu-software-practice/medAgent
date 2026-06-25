package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderSnapshotIncludesProfileAndHistory(t *testing.T) {
	s := Snapshot{
		Profile: json.RawMessage(`{"年龄":30,"性别":"男"}`),
		History: "第1次(初诊): 急性咽炎，已开药。",
	}
	got := renderSnapshotBlock(s, nil)
	if !strings.Contains(got, "【患者资料】") || !strings.Contains(got, `"年龄"`) {
		t.Errorf("缺患者资料块：%s", got)
	}
	if !strings.Contains(got, "【历史就诊记录】") || !strings.Contains(got, "急性咽炎") {
		t.Errorf("缺历史就诊块：%s", got)
	}
}

func TestRenderSnapshotOmitsEmptyProfileHistory(t *testing.T) {
	got := renderSnapshotBlock(Snapshot{}, nil)
	if strings.Contains(got, "【患者资料】") || strings.Contains(got, "【历史就诊记录】") {
		t.Errorf("空时不应出现这两块：%s", got)
	}
}
