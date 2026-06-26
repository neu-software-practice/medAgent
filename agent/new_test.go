package agent

import "testing"

func TestNewValidates(t *testing.T) {
	if _, err := New(Config{Provider: "deepseek", Model: "deepseek-chat"}); err == nil {
		t.Error("缺 APIKey 应报错")
	}
	if _, err := New(Config{Provider: "无此", APIKey: "k", Model: "m"}); err == nil {
		t.Error("未知 provider 应报错")
	}
	s, err := New(Config{Provider: "deepseek", APIKey: "k", Model: "deepseek-chat"})
	if err != nil {
		t.Fatalf("合法配置应成功：%v", err)
	}
	s.Close()
}
