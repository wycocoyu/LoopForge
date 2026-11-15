package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"agentflow/internal/domain"
	"agentflow/internal/repo"
)

func TestSubmitCNBPullCreatesThenUpdatesTheOpenOne(t *testing.T) {
	var writes []string
	openPulls := `[]`
	fakeCNBAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testCNBToken {
			t.Errorf("wrong credential %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/org/team/repo/-/pulls":
			_, _ = w.Write([]byte(openPulls))
		case r.Method == http.MethodPost && r.URL.Path == "/org/team/repo/-/pulls":
			raw, _ := io.ReadAll(r.Body)
			var body map[string]string
			_ = json.Unmarshal(raw, &body)
			writes = append(writes, "create "+body["head"]+"->"+body["base"]+" "+body["title"])
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number":"31","state":"open","base":{"ref":"refs/heads/main"},"head":{"ref":"refs/heads/feature/x"}}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/org/team/repo/-/pulls/31":
			raw, _ := io.ReadAll(r.Body)
			var body map[string]string
			_ = json.Unmarshal(raw, &body)
			writes = append(writes, "update "+body["title"])
			_, _ = w.Write([]byte(`{"number":"31","state":"open","base":{"ref":"refs/heads/main"},"head":{"ref":"refs/heads/feature/x"}}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	pr, err := submitCNBPull(context.Background(), testCNBToken, "org/team/repo", "feature/x", "main", "first", "body")
	if err != nil || pr.IID != 31 || pr.Existing || pr.Updated {
		t.Fatalf("create: %+v %v", pr, err)
	}
	openPulls = `[{"number":"31","state":"open","head":{"ref":"refs/heads/feature/x","repo":{"path":"org/team/repo"}},"base":{"ref":"refs/heads/main"}}]`
	pr, err = submitCNBPull(context.Background(), testCNBToken, "org/team/repo", "feature/x", "main", "second", "body")
	if err != nil || pr.IID != 31 || !pr.Existing || !pr.Updated {
		t.Fatalf("update: %+v %v", pr, err)
	}
	if strings.Join(writes, "|") != "create feature/x->main first|update second" {
		t.Fatalf("writes = %v", writes)
	}
}

func TestSubmitCNBPullReusesNothingFromAClosedPullAndReportsUpdateFailure(t *testing.T) {
	creates := 0
	fakeCNBAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[{"number":"5","state":"closed","head":{"ref":"refs/heads/f","repo":{"path":"org/repo"}},"base":{"ref":"refs/heads/main"}}]`))
		case r.Method == http.MethodPost:
			creates++
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number":"6","state":"open","base":{"ref":"refs/heads/main"},"head":{"ref":"refs/heads/f"}}`))
		default:
			t.Errorf("unexpected %s", r.Method)
		}
	})
	pr, err := submitCNBPull(context.Background(), testCNBToken, "org/repo", "f", "main", "t", "b")
	if err != nil || pr.IID != 6 || pr.Existing || creates != 1 {
		t.Fatalf("a closed pull must not be reused: %+v %v creates=%d", pr, err, creates)
	}

	fakeCNBAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[{"number":"5","state":"open","head":{"ref":"refs/heads/f","repo":{"path":"org/repo"}},"base":{"ref":"refs/heads/main"}}]`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errmsg":"no pr write"}`))
	})
	pr, err = submitCNBPull(context.Background(), testCNBToken, "org/repo", "f", "main", "t", "b")
	if err != nil || pr.IID != 5 || !pr.Existing || pr.Updated || !strings.Contains(pr.UpdateError, "HTTP 403") {
		t.Fatalf("a failed update must still return the pull and say why: %+v %v", pr, err)
	}
}

func TestSubmitCNBPullMapsFailuresToActionableErrors(t *testing.T) {
	for status, want := range map[int]string{http.StatusUnauthorized: "无效或已过期", http.StatusForbidden: "repo-pr:rw", http.StatusNotFound: "找不到仓库或分支"} {
		fakeCNBAPI(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"errmsg":"nope"}`))
		})
		if _, err := submitCNBPull(context.Background(), testCNBToken, "org/repo", "f", "main", "t", "b"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("HTTP %d: %v", status, err)
		}
	}
}

func TestCNBRepoSlugOnlyTrustsCNBProjectIdentity(t *testing.T) {
	for in, want := range map[string]string{"cnb:org/team/repo": "org/team/repo", "cnb:org/repo": "org/repo", "cnb:org": "", "cnb:../x/y": "", "github:org/repo": "", "org/repo": "", "": ""} {
		if got := cnbRepoSlug(domain.RepositoryContext{GongfengProjectID: in}); got != want {
			t.Errorf("cnbRepoSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWorkspaceSubmitMRRequiresACNBTokenForCNBRepositories(t *testing.T) {
	st, err := repo.OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := st.PutUser(domain.User{LoginName: "cnb-no-token", GitToken: "ghp_FAKEGLOBALPAT"}); err != nil {
		t.Fatal(err)
	}
	user, _ := st.UserByLoginName("cnb-no-token")
	run := domain.Run{ID: "run-cnb", Kind: domain.RunKindWorkspace, Status: "waiting", WorkflowSnapshot: domain.WorkflowDefinition{ID: domain.ConversationWorkspaceWorkflowID}, ExecutionContext: domain.TelemetryContext{ProjectID: "platform"},
		Repositories: []domain.RepositoryContext{{RepositoryID: "r", Name: "repo", Provider: domain.RepositoryProviderCNB, CloneURL: "https://cnb.cool/org/repo.git", GongfengProjectID: "cnb:org/repo", BaseBranch: "main", WorkBranch: "feature/x"}}, Nodes: map[string]*domain.NodeRun{}}
	if err := st.PutRun(run); err != nil {
		t.Fatal(err)
	}
	h := &agentToolGateway{deps: &deps{repo: st}}
	session := &AgentToolSession{RunID: run.ID, ProjectID: "platform", UserID: user.ID, User: user, Mode: AgentToolAssistant}
	if _, err := h.workspaceSubmitMR(context.Background(), session, map[string]any{"title": "x"}); err == nil || !strings.Contains(err.Error(), "CNB 访问令牌") || !strings.Contains(err.Error(), "cnb.cool/profile/token") {
		t.Fatalf("a GitHub token must not stand in for the CNB token: %v", err)
	}
	if err := setCNBToken(st, user.ID, testCNBToken); err != nil {
		t.Fatal(err)
	}
	if _, err := h.workspaceSubmitMR(context.Background(), session, map[string]any{"title": "x"}); err == nil || !strings.Contains(err.Error(), "执行引擎不可用") {
		t.Fatalf("with a CNB token the guard must pass (and stop at the missing engine): %v", err)
	}
}

func TestCNBTokenIsRedactedFromToolResults(t *testing.T) {
	got := redactAgentToolResult("failed with "+testCNBToken+" in text", domain.User{CNBToken: testCNBToken})
	if strings.Contains(got, testCNBToken) {
		t.Fatalf("token survived redaction: %s", got)
	}
}
