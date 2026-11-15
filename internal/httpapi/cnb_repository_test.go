package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentflow/internal/domain"
)

func fakeCNBAPI(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	previous := cnbAPIBase
	cnbAPIBase = srv.URL
	t.Cleanup(func() { cnbAPIBase = previous })
}

func TestBindCNBRepositoryProbesDefaultBranchWithTheBindersToken(t *testing.T) {
	f := cvBNewHTTPFixture(t)
	if err := f.s.PutProject(domain.Project{ID: "cnb-proj", Key: "CNBP", Name: "CNB", OwnerID: f.user.ID}); err != nil {
		t.Fatal(err)
	}
	var paths, auths []string
	fakeCNBAPI(t, func(w http.ResponseWriter, r *http.Request) {
		paths, auths = append(paths, r.URL.Path), append(auths, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"name":"develop"}`))
	})

	noToken := cvBRequest(t, f, http.MethodPost, "/api/repositories", map[string]any{"projectId": "cnb-proj", "repoUrl": "https://cnb.cool/org/team/repo"}, true)
	if noToken.Code != http.StatusBadRequest || !strings.Contains(noToken.Body.String(), "CNB") || len(paths) != 0 {
		t.Fatalf("binding without a CNB token must fail clearly (HTTP %d): %s", noToken.Code, noToken.Body.String())
	}

	if err := setCNBToken(f.s, f.user.ID, testCNBToken); err != nil {
		t.Fatal(err)
	}
	resp := cvBRequest(t, f, http.MethodPost, "/api/repositories", map[string]any{"id": "cnb-repo", "projectId": "cnb-proj", "repoUrl": "https://cnb.cool/org/team/repo/-/tree/main"}, true)
	var saved domain.Repository
	if err := json.Unmarshal(resp.Body.Bytes(), &saved); err != nil || resp.Code != http.StatusOK {
		t.Fatalf("bind: HTTP %d: %s", resp.Code, resp.Body.String())
	}
	if saved.Provider != domain.RepositoryProviderCNB || saved.GongfengProjectID != "cnb:org/team/repo" || saved.CloneURL != "https://cnb.cool/org/team/repo.git" || saved.WebURL != "https://cnb.cool/org/team/repo" || saved.DefaultBranch != "develop" {
		t.Fatalf("saved = %+v", saved)
	}
	if len(paths) != 1 || paths[0] != "/org/team/repo/-/git/head" || auths[0] != "Bearer "+testCNBToken {
		t.Fatalf("probe = %v %v", paths, auths)
	}
	if strings.Contains(resp.Body.String(), testCNBToken) {
		t.Fatal("response leaks the token")
	}
}

func TestBindCNBRepositoryReportsProbeFailureAndHonoursExplicitBranch(t *testing.T) {
	f := cvBNewHTTPFixture(t)
	if err := f.s.PutProject(domain.Project{ID: "cnb-proj", Key: "CNBP", Name: "CNB", OwnerID: f.user.ID}); err != nil {
		t.Fatal(err)
	}
	if err := setCNBToken(f.s, f.user.ID, testCNBToken); err != nil {
		t.Fatal(err)
	}
	calls := 0
	fakeCNBAPI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errcode":1,"errmsg":"no access"}`))
	})
	resp := cvBRequest(t, f, http.MethodPost, "/api/repositories", map[string]any{"projectId": "cnb-proj", "repoUrl": "https://cnb.cool/org/repo"}, true)
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "repo-code:r") {
		t.Fatalf("probe failure must name the missing scope (HTTP %d): %s", resp.Code, resp.Body.String())
	}
	resp = cvBRequest(t, f, http.MethodPost, "/api/repositories", map[string]any{"projectId": "cnb-proj", "repoUrl": "cnb:org/repo", "defaultBranch": "trunk"}, true)
	if resp.Code != http.StatusOK || calls != 1 || !strings.Contains(resp.Body.String(), `"defaultBranch":"trunk"`) {
		t.Fatalf("explicit default branch must skip the probe (HTTP %d, calls %d): %s", resp.Code, calls, resp.Body.String())
	}
}
