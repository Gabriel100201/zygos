package provider

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Gabriel100201/zygos/internal/config"
)

// TestLinearLiveReadOnly smoke-tests every NEW GraphQL query added for the
// task-actions feature against the real Linear API, WITHOUT mutating anything.
// It is gated behind ZYGOS_LIVE=1 so it never runs in CI. A query is
// considered broken only if the API returns a GraphQL error; "not found"
// results are fine (they prove the query parsed and executed).
//
//	ZYGOS_LIVE=1 go test ./internal/provider/ -run TestLinearLiveReadOnly -v
func TestLinearLiveReadOnly(t *testing.T) {
	if os.Getenv("ZYGOS_LIVE") != "1" {
		t.Skip("set ZYGOS_LIVE=1 to run live Linear read-only checks")
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	// Pick the first Linear provider that returns at least one task.
	var lp *linearProvider
	var sample Task
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, pc := range cfg.Providers {
		if pc.Type != "linear" {
			continue
		}
		cand := NewLinear(pc.Name, pc.APIKey).(*linearProvider)
		tasks, err := cand.ListTasks(ctx, ListOpts{State: "all"})
		if err != nil {
			t.Logf("provider %s ListTasks failed: %v", pc.Name, err)
			continue
		}
		if len(tasks) > 0 {
			lp = cand
			sample = tasks[0]
			break
		}
	}
	if lp == nil {
		t.Skip("no Linear provider with tasks available")
	}
	t.Logf("using provider %q, sample issue %s", lp.name, sample.Identifier)

	// failIfGraphQLError treats a real query error as fatal but tolerates the
	// resolver's own "not found" messages.
	failIfGraphQLError := func(label string, err error) {
		if err != nil && strings.Contains(err.Error(), "GraphQL error") {
			t.Fatalf("%s: query is broken: %v", label, err)
		}
		t.Logf("%s: ok (%v)", label, err)
	}

	// 1. ListMembers — new `users` query.
	members, err := lp.ListMembers(ctx, "")
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	t.Logf("ListMembers: %d active users", len(members))

	// 2. resolveIssueRef — new issues{ team{ id key } } query.
	ref, err := lp.resolveIssueRef(ctx, sample.Identifier)
	if err != nil {
		t.Fatalf("resolveIssueRef(%s): %v", sample.Identifier, err)
	}
	t.Logf("resolveIssueRef: id=%s teamKey=%s", ref.id, ref.teamKey)

	// 3. resolveUserID("me") — new viewer lookup.
	me, err := lp.resolveUserID(ctx, "me")
	if err != nil {
		t.Fatalf(`resolveUserID("me"): %v`, err)
	}
	t.Logf("resolveUserID(me): %s", me)

	// 4. resolveStateID by name — reuses ListStates, exercises match.
	if _, err := lp.resolveStateID(ctx, ref.teamKey, sample.Status); err != nil {
		t.Fatalf("resolveStateID(%q): %v", sample.Status, err)
	}
	t.Logf("resolveStateID(%q): ok", sample.Status)

	// 5. resolveLabelIDs — new issueLabels query (unknown name → not-found is fine).
	_, err = lp.resolveLabelIDs(ctx, []string{"__zygos_nonexistent_label__"})
	failIfGraphQLError("resolveLabelIDs", err)

	// 6. resolveCycleID "active" — new team{ activeCycle } query.
	_, err = lp.resolveCycleID(ctx, ref.teamID, "active")
	failIfGraphQLError("resolveCycleID(active)", err)

	// 7. resolveCycleID by name — new cycles(filter) query (unknown → not-found).
	_, err = lp.resolveCycleID(ctx, ref.teamID, "__zygos_nonexistent_cycle__")
	failIfGraphQLError("resolveCycleID(name)", err)
}
