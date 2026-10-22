package api

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func auditFixture() []byte {
	return []byte(`{"PolarisOutputVersion":"1.0","SourceType":"Cluster","Results":[{"Name":"web","Namespace":"blue","Kind":"Deployment","Results":{"cpuRequestsMissing":{"ID":"cpuRequestsMissing","Success":false,"Severity":"warning","Message":"PRIVATE-MESSAGE"}},"PodResult":null}],"secret":"PRIVATE-ENV"}`)
}
func TestOfflineAuditRequiresUIAndExplicitExistingContextWithoutConnecting(t *testing.T) {
	p := &krrAPIProvider{openable: newOpenable("a")}
	s, _ := newService(t, p)
	ctx := UIContext(t.Context())
	v, e := s.ListTargets(ctx)
	require.NoError(t, e)
	revision := v.Groups[0].Targets[0].ConfigRev
	require.NotEmpty(t, revision)
	req := OfflineAuditRequest{Command: "import", Version: "10.2.5", Target: "a", ConfigRev: revision, Data: auditFixture()}
	_, e = s.OfflineAudit(t.Context(), req)
	require.Error(t, e)
	agentService, _ := newService(t, &krrAPIProvider{openable: newOpenable("a")})
	call, e := agentService.AgentCall(ctx, "kubernetes", "a")
	require.NoError(t, e)
	defer call.Done()
	_, e = s.OfflineAudit(call.ctx, req)
	require.Error(t, e)
	out, e := s.OfflineAudit(ctx, req)
	require.NoError(t, e)
	require.NotNil(t, out.Selected)
	require.Empty(t, p.opened)
	id := out.Selected.Summary.ID
	require.Equal(t, "a", out.Selected.Summary.Target)
	require.Equal(t, revision, out.Selected.Summary.ConfigRev)
	safe, e := json.Marshal(out)
	require.NoError(t, e)
	require.NotContains(t, string(safe), "PRIVATE-")
	out.Selected.Report.Subjects[0].Name = "changed"
	got, e := s.OfflineAudit(ctx, OfflineAuditRequest{Command: "get", ID: id})
	require.NoError(t, e)
	require.Equal(t, "web", got.Selected.Report.Subjects[0].Name)
	for _, r := range []OfflineAuditRequest{{Command: "status", Data: auditFixture()}, {Command: "import", Version: "10.2.5", Target: "a", ConfigRev: "stale", Data: auditFixture()}, {Command: "import", Version: "10.2.5", Target: "missing", ConfigRev: revision, Data: auditFixture()}, {Command: "get", ID: id, Target: "a"}, {Command: "unknown"}} {
		_, e = s.OfflineAudit(ctx, r)
		require.Error(t, e)
		require.NotContains(t, e.Error(), "PRIVATE-")
	}
	_, e = s.OfflineAudit(ctx, OfflineAuditRequest{Command: "forget", ID: id})
	require.NoError(t, e)
	_, e = s.OfflineAudit(ctx, OfflineAuditRequest{Command: "get", ID: id})
	require.Error(t, e)
	require.Empty(t, p.opened)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, e = s.OfflineAudit(canceled, req)
	require.Error(t, e)
}
