package desktop

import (
	"testing"

	core "github.com/afterlune/luneCore"
	publictool "github.com/afterlune/luneCore/tool"
)

func TestPendingApprovalViewIncludesSessionPathGrant(t *testing.T) {
	grant := &publictool.PathGrant{Path: `C:\Users\Code\private`, Access: publictool.PathReadWrite}
	view := pendingRequestView(core.PendingRequest{Kind: core.PendingApproval, Approval: &core.ApprovalRequest{
		ID: "approval", Tool: "write_file", PathGrant: grant,
	}})
	if view.PathGrant == nil || *view.PathGrant != *grant {
		t.Fatalf("approval view path grant = %#v, want %#v", view.PathGrant, grant)
	}
}
