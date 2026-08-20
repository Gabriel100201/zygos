package mcp

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func request(args map[string]any) mcp.CallToolRequest {
	var req mcp.CallToolRequest
	req.Params.Arguments = args
	return req
}

// optStr carries the distinction the update tools are built on: an absent key
// means "leave this field alone", while a present one — even "none" or "" —
// means "set it to this". Collapsing the two silently ignores every clear.
func TestOptStrDistinguishesAbsentFromEmpty(t *testing.T) {
	req := request(map[string]any{
		"assignee": "none",
		"project":  "",
		"cycle":    nil,
	})

	if got := optStr(req, "assignee"); got == nil || *got != "none" {
		t.Errorf(`optStr("assignee") = %v, want a pointer to "none"`, got)
	}
	if got := optStr(req, "project"); got == nil || *got != "" {
		t.Errorf(`optStr("project") = %v, want a pointer to ""`, got)
	}
	if got := optStr(req, "cycle"); got != nil {
		t.Errorf(`optStr("cycle") = %v, want nil for an explicit null`, *got)
	}
	if got := optStr(req, "due_date"); got != nil {
		t.Errorf(`optStr("due_date") = %v, want nil for an absent key`, *got)
	}
}

func TestOptStrCoercesNonStrings(t *testing.T) {
	req := request(map[string]any{"estimate": float64(3), "parent": 42})
	if got := optStr(req, "estimate"); got == nil || *got != "3" {
		t.Errorf(`optStr("estimate") = %v, want "3"`, got)
	}
	if got := optStr(req, "parent"); got == nil || *got != "42" {
		t.Errorf(`optStr("parent") = %v, want "42"`, got)
	}
}

// strPtr is the opposite policy, used by fields where an empty string can only
// mean "not provided".
func TestStrPtrTreatsEmptyAsAbsent(t *testing.T) {
	if got := strPtr(""); got != nil {
		t.Errorf("strPtr(%q) = %v, want nil", "", *got)
	}
	if got := strPtr("Nuevo título"); got == nil || *got != "Nuevo título" {
		t.Errorf("strPtr lost the value: %v", got)
	}
}

func TestSplitLabels(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"bug, urgente ,frontend", []string{"bug", "urgente", "frontend"}},
		{"solo", []string{"solo"}},
		{"  ", nil},
		{"", nil},
		{"a,,b", []string{"a", "b"}},
		{",", nil},
	}
	for _, tc := range cases {
		got := splitLabels(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("splitLabels(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("splitLabels(%q) = %v, want %v", tc.in, got, tc.want)
				break
			}
		}
	}
}

func TestStrParamDefaultsToEmpty(t *testing.T) {
	req := request(map[string]any{"provider": "work"})
	if got := strParam(req, "provider"); got != "work" {
		t.Errorf("strParam = %q, want %q", got, "work")
	}
	if got := strParam(req, "missing"); got != "" {
		t.Errorf("strParam on an absent key = %q, want empty", got)
	}
}

// The tool set is the project's public contract: an agent's prompt and a user's
// muscle memory both depend on these names not moving.
func TestServerRegistersTheDocumentedToolset(t *testing.T) {
	srv := NewServer(nil, "1.2.3")
	tools := srv.ListTools()

	want := []string{
		"tasks_list", "tasks_get", "tasks_create", "tasks_update", "tasks_search",
		"tasks_projects", "tasks_states", "tasks_members", "tasks_comment",
		"docs_list", "docs_get", "docs_create", "docs_update", "docs_delete", "docs_search",
	}
	for _, name := range want {
		if _, ok := tools[name]; !ok {
			t.Errorf("tool %q is not registered", name)
		}
	}
	if len(tools) != len(want) {
		var extra []string
		for name := range tools {
			known := false
			for _, w := range want {
				if w == name {
					known = true
					break
				}
			}
			if !known {
				extra = append(extra, name)
			}
		}
		t.Errorf("registered %d tools, expected %d; undocumented: %v", len(tools), len(want), extra)
	}
}

// Agents use readOnlyHint to decide what can run without asking the user first.
// Mislabelling a write as read-only means silent, unconfirmed mutations.
func TestWriteToolsAreNotMarkedReadOnly(t *testing.T) {
	tools := NewServer(nil, "test").ListTools()

	writes := []string{"tasks_create", "tasks_update", "tasks_comment", "docs_create", "docs_update", "docs_delete"}
	for _, name := range writes {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("tool %q is not registered", name)
		}
		if hint := tool.Tool.Annotations.ReadOnlyHint; hint != nil && *hint {
			t.Errorf("%q is annotated read-only but it mutates the tracker", name)
		}
	}

	reads := []string{"tasks_list", "tasks_get", "tasks_search", "tasks_projects", "tasks_states", "docs_list", "docs_get", "docs_search"}
	for _, name := range reads {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("tool %q is not registered", name)
		}
		if hint := tool.Tool.Annotations.ReadOnlyHint; hint == nil || !*hint {
			t.Errorf("%q is a read but is not annotated read-only", name)
		}
	}
}

func TestDeleteToolIsMarkedDestructive(t *testing.T) {
	tools := NewServer(nil, "test").ListTools()
	tool, ok := tools["docs_delete"]
	if !ok {
		t.Fatal("docs_delete is not registered")
	}
	if hint := tool.Tool.Annotations.DestructiveHint; hint == nil || !*hint {
		t.Error("docs_delete must carry a destructive hint — it removes a document for good")
	}
}
