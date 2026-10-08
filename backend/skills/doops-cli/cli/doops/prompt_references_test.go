package main

import "testing"

func TestAppendConversationReferencesUsesMetadataOnlyContext(t *testing.T) {
	got, err := appendConversationReferences("检查订单数据库", []string{"db_orders_prod"}, []string{"repo_orders@main"})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"数据库引用：db_orders_prod", "代码仓库引用：repo_orders@main", "用户任务：", "检查订单数据库"} {
		if !containsText(got, expected) {
			t.Fatalf("context missing %q: %s", expected, got)
		}
	}
}

func TestAppendConversationReferencesRejectsUnsafeReference(t *testing.T) {
	if _, err := appendConversationReferences("task", []string{"postgres://user:password@example/db"}, nil); err == nil {
		t.Fatal("credential-bearing database reference was accepted")
	}
}

func containsText(value, wanted string) bool {
	for start := 0; start+len(wanted) <= len(value); start++ {
		if value[start:start+len(wanted)] == wanted {
			return true
		}
	}
	return false
}
