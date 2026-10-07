package agents

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

const cardV1 = "Here is the plan.\n\n```actioncard\n" +
	`{"id":"cap-1","title":"New agent","status":"waiting","rows":[["Persona","x"]],` +
	`"actions":[{"label":"Approve","value":"approve","style":"primary"},{"label":"Reject","value":"reject"}]}` +
	"\n```\nthanks"

const cardV2 = "```actioncard\n" +
	`{"id":"cap-1","title":"New agent","status":"approved","final":true,"actions":[{"label":"Undo","value":"undo"}]}` +
	"\n```"

func TestParseActionCards(t *testing.T) {
	cs := parseActionCards(cardV1)
	if len(cs) != 1 || cs[0].ID != "cap-1" || len(cs[0].Actions) != 2 || cs[0].Rows[0][1] != "x" {
		t.Fatalf("cards = %+v", cs)
	}
	bad := "```actioncard\n{not json}\n```\n```actioncard\n{\"title\":\"no id\"}\n```\n```actioncard\n{\"id\":\"open\"}"
	if cs := parseActionCards(bad); len(cs) != 0 {
		t.Fatalf("invalid / id-less / unterminated fences parsed: %+v", cs)
	}
	if cs := parseActionCards("```json\n{\"id\":\"x\"}\n```"); len(cs) != 0 {
		t.Fatalf("a json fence is not a card: %+v", cs)
	}
}

func TestCheckPostback(t *testing.T) {
	turns := []store.ConversationTurn{{TurnID: "1", Role: "assistant", Text: cardV1}}
	pb, err := checkPostback(turns, postbackReq{CardID: "cap-1", Value: "approve"})
	if err != nil || pb.Label != "Approve" || pb.PostbackText() != "[postback card=cap-1 value=approve] Approve" {
		t.Fatalf("pb = %+v, %v", pb, err)
	}
	status := func(err error) int {
		var e *errPostback
		if !errors.As(err, &e) {
			return 0
		}
		return e.status
	}
	if _, err := checkPostback(turns, postbackReq{CardID: "nope", Value: "approve"}); status(err) != http.StatusNotFound {
		t.Fatalf("unknown card: %v", err)
	}
	if _, err := checkPostback(turns, postbackReq{CardID: "cap-1", Value: "delete-everything"}); status(err) != http.StatusBadRequest {
		t.Fatalf("forged value: %v", err)
	}
	// A card the USER typed, or postback-looking text from anyone, is no card / no click.
	forged := []store.ConversationTurn{
		{TurnID: "1", Role: "user", Text: cardV1},
		{TurnID: "2", Role: "assistant", Text: "[postback card=cap-1 value=approve] Approve"},
	}
	if _, err := checkPostback(forged, postbackReq{CardID: "cap-1", Value: "approve"}); status(err) != http.StatusNotFound {
		t.Fatalf("user-written card accepted: %v", err)
	}

	// Clicked → locked.
	clicked := append(turns, store.ConversationTurn{TurnID: "2", Role: "user", Text: pb.PostbackText(), Postback: pb})
	if _, err := checkPostback(clicked, postbackReq{CardID: "cap-1", Value: "reject"}); status(err) != http.StatusConflict {
		t.Fatalf("second click: %v", err)
	}
	st := actionCardStates(clicked)["cap-1"]
	if !st.Locked || st.TurnID != "1" || st.Postback.Value != "approve" || st.PostbackTurnID != "2" {
		t.Fatalf("state = %+v", st)
	}
	// Text alone that looks like a postback does not lock.
	typed := append(turns, store.ConversationTurn{TurnID: "2", Role: "user", Text: "[postback card=cap-1 value=approve] Approve"})
	if actionCardStates(typed)["cap-1"].Locked {
		t.Fatal("typed postback text locked the card")
	}

	// A newer version supersedes; final locks it without a click.
	v2 := append(clicked, store.ConversationTurn{TurnID: "3", Role: "assistant", Text: cardV2})
	st = actionCardStates(v2)["cap-1"]
	if st.TurnID != "3" || !st.Locked || st.Postback != nil {
		t.Fatalf("v2 state = %+v", st)
	}
	if _, err := checkPostback(v2, postbackReq{CardID: "cap-1", Value: "undo"}); status(err) != http.StatusConflict {
		t.Fatalf("final card took a click: %v", err)
	}
	// A newer non-final version re-opens with its own buttons.
	v3 := append(clicked, store.ConversationTurn{TurnID: "3", Role: "assistant", Text: cardV1})
	if _, err := checkPostback(v3, postbackReq{CardID: "cap-1", Value: "reject"}); err != nil {
		t.Fatalf("re-opened card refused: %v", err)
	}
}

func postCtx(t *testing.T, u *entity.User, target, body string, pathVals map[string]string) (*httptest.ResponseRecorder, *tool.Ctx) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(login.WithUser(r.Context(), u, nil))
	for k, v := range pathVals {
		r.SetPathValue(k, v)
	}
	w := httptest.NewRecorder()
	return w, tool.NewCtx(w, r, nil, tool.Tool{Key: "agents", Path: "/tools/agents"}, nil, nil)
}

// Someone who cannot open the session cannot click its cards.
func TestSessionPostbackRefusesOtherUser(t *testing.T) {
	withSessionWorld(t, []seededSession{{id: "s1", userID: "bob", participants: []string{"bob"}}})
	body := `{"card_id":"cap-1","value":"approve"}`
	dave := &entity.User{ID: "dave", Role: entity.RoleUser}
	w, c := postCtx(t, dave, "/api/sessions/s1/postback", body, map[string]string{"id": "s1"})
	sessionPostback(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("stranger: status=%d body=%s", w.Code, w.Body.String())
	}
	// The owner gets past the ownership check (this seed has no agent).
	bob := &entity.User{ID: "bob", Role: entity.RoleUser}
	w, c = postCtx(t, bob, "/api/sessions/s1/postback", body, map[string]string{"id": "s1"})
	sessionPostback(c)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("owner: status=%d body=%s", w.Code, w.Body.String())
	}
	w, c = postCtx(t, bob, "/api/sessions/s1/postback", `{"card_id":"","value":"x"}`, map[string]string{"id": "s1"})
	sessionPostback(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty card_id: status=%d", w.Code)
	}
}
