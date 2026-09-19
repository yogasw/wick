package workflow

import (
	"context"
	"testing"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	wfmcp "github.com/yogasw/wick/internal/agents/workflow/mcp"
	"github.com/yogasw/wick/internal/agents/workflow/service"
	"github.com/yogasw/wick/pkg/connector"
)

// fakeService implements only what the create path touches. Embedding the
// interface leaves every other method a loud nil-panic rather than a quiet
// zero value, so the test cannot drift into covering something else.
type fakeService struct {
	service.Service
	created wf.Workflow
	stored  wf.Workflow
}

func (f *fakeService) Create(id string, w wf.Workflow) error {
	w.ID = id
	f.created, f.stored = w, w
	return nil
}
func (f *fakeService) Load(string) (wf.Workflow, error) { return f.stored, nil }
func (f *fakeService) Update(_ string, w wf.Workflow) error {
	f.stored = w
	return nil
}
func (f *fakeService) FindByName(string, string) (string, error) { return "", nil }

// recorder captures the ownership registration.
type recorder struct {
	userID string
	wfID   string
	calls  int
}

func (r *recorder) RegisterOwner(_ context.Context, userID, workflowID string) {
	r.calls++
	r.userID, r.wfID = userID, workflowID
}

func createCtx(caller string) *connector.Ctx {
	c := connector.NewCtx(context.Background(), "", nil, map[string]string{
		"name":     "demo",
		"template": "empty",
	}, nil, nil, nil)
	if caller != "" {
		c.SetCallerUserID(caller)
	}
	return c
}

// TestCreateRecordsTheHumanAsOwner: a workflow an agent scaffolds belongs to
// the person behind the session. Without this it lands ownerless, and the
// listing rule (owner or admin) then hides it from the very person who asked
// for it — the only fix left being to make them an admin.
func TestCreateRecordsTheHumanAsOwner(t *testing.T) {
	svc := &fakeService{}
	rec := &recorder{}
	prev := workflowOwnership
	SetWorkflowOwnership(rec)
	t.Cleanup(func() { workflowOwnership = prev })

	h := &handlers{ops: &wfmcp.Ops{Service: svc}}
	out, err := h.create(createCtx("user-hana"))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := out.(map[string]any)["id"].(string)
	if id == "" {
		t.Fatalf("create returned no id: %+v", out)
	}
	if svc.created.CreatedBy != "user-hana" {
		t.Fatalf("created_by = %q, want the caller", svc.created.CreatedBy)
	}
	if rec.calls != 1 || rec.userID != "user-hana" || rec.wfID != id {
		t.Fatalf("owner registration = %+v, want one call for %s/%s", rec, "user-hana", id)
	}
}

// TestCreateWithNoHumanStaysOwnerless covers cron and system spawns: there
// is nobody to attribute the workflow to, and inventing an owner would hand
// it to an account no one can log in as.
func TestCreateWithNoHumanStaysOwnerless(t *testing.T) {
	svc := &fakeService{}
	rec := &recorder{}
	prev := workflowOwnership
	SetWorkflowOwnership(rec)
	t.Cleanup(func() { workflowOwnership = prev })

	h := &handlers{ops: &wfmcp.Ops{Service: svc}}
	if _, err := h.create(createCtx("")); err != nil {
		t.Fatal(err)
	}
	if svc.created.CreatedBy != "" {
		t.Fatalf("created_by = %q, want empty for a system caller", svc.created.CreatedBy)
	}
	if rec.calls != 0 {
		t.Fatalf("registered an owner for a system caller: %+v", rec)
	}
}
