package pwa

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

type sentCall struct{ user, endpoint string }

// testHandler wires a handler whose sends are recorded and whose delays
// are captured instead of slept.
func testHandler(sendErr error) (*PushHandler, *[]sentCall, *[]time.Duration, *[]func()) {
	var sent []sentCall
	var delays []time.Duration
	var pending []func()
	h := &PushHandler{
		sendTest: func(_ context.Context, user, endpoint string) (int, error) {
			sent = append(sent, sentCall{user, endpoint})
			if sendErr != nil {
				return 0, sendErr
			}
			return 1, nil
		},
		// u1 owns e1 and e2; e-other belongs to somebody else.
		ownsDevice: func(_ context.Context, user, endpoint string) (bool, error) {
			return user == "u1" && (endpoint == "e1" || endpoint == "e2"), nil
		},
		after: func(d time.Duration, f func()) { delays = append(delays, d); pending = append(pending, f) },
	}
	return h, &sent, &delays, &pending
}

func doTest(h *PushHandler, user *entity.User, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/push/test", strings.NewReader(body))
	if user != nil {
		r = r.WithContext(login.WithUser(r.Context(), user, nil))
	}
	w := httptest.NewRecorder()
	h.test(w, r)
	return w
}

func TestPushTestSendsNowWithoutDelay(t *testing.T) {
	h, sent, delays, _ := testHandler(nil)
	w := doTest(h, &entity.User{ID: "u1"}, `{"endpoint":"e1"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"sent":1`) {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if len(*sent) != 1 || len(*delays) != 0 {
		t.Fatalf("sent %v delays %v", *sent, *delays)
	}
}

func TestPushTestDelayIsScheduledAndClamped(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{{"5", 5}, {"300", maxTestDelay}} {
		h, sent, delays, pending := testHandler(nil)
		w := doTest(h, &entity.User{ID: "u1"}, `{"endpoint":"e1","delay_seconds":`+tc.in+`}`)
		if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"scheduled":true`) {
			t.Fatalf("delay %s: got %d %s", tc.in, w.Code, w.Body)
		}
		if len(*delays) != 1 || (*delays)[0] != time.Duration(tc.want)*time.Second {
			t.Fatalf("delay %s: scheduled %v", tc.in, *delays)
		}
		if len(*sent) != 0 {
			t.Fatalf("delay %s: sent before the delay", tc.in)
		}
		(*pending)[0]()
		if len(*sent) != 1 || (*sent)[0] != (sentCall{"u1", "e1"}) {
			t.Fatalf("delay %s: after firing sent %v", tc.in, *sent)
		}
	}
}

func TestPushTestNegativeDelaySendsNow(t *testing.T) {
	h, sent, delays, _ := testHandler(nil)
	if w := doTest(h, &entity.User{ID: "u1"}, `{"delay_seconds":-4}`); w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	if len(*sent) != 1 || len(*delays) != 0 {
		t.Fatalf("sent %v delays %v", *sent, *delays)
	}
}

func TestPushTestFailureIsReported(t *testing.T) {
	h, _, _, _ := testHandler(errors.New("subscription expired"))
	if w := doTest(h, &entity.User{ID: "u1"}, `{}`); w.Code != http.StatusBadGateway {
		t.Fatalf("got %d", w.Code)
	}
}

func TestPushTestNeedsLogin(t *testing.T) {
	h, sent, delays, _ := testHandler(nil)
	if w := doTest(h, nil, `{"delay_seconds":5}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", w.Code)
	}
	if len(*sent) != 0 || len(*delays) != 0 {
		t.Fatal("sent without a user")
	}
}

func TestPushTestWithoutEndpointTargetsAllDevices(t *testing.T) {
	h, sent, _, pending := testHandler(nil)
	if w := doTest(h, &entity.User{ID: "u1"}, `{"delay_seconds":5}`); w.Code != http.StatusAccepted {
		t.Fatalf("got %d", w.Code)
	}
	(*pending)[0]()
	if len(*sent) != 1 || (*sent)[0] != (sentCall{"u1", ""}) {
		t.Fatalf("sent %v", *sent)
	}
}

func TestPushTestOneOwnDevice(t *testing.T) {
	h, sent, _, pending := testHandler(nil)
	if w := doTest(h, &entity.User{ID: "u1"}, `{"endpoint":"e2","delay_seconds":5}`); w.Code != http.StatusAccepted {
		t.Fatalf("got %d", w.Code)
	}
	(*pending)[0]()
	if len(*sent) != 1 || (*sent)[0] != (sentCall{"u1", "e2"}) {
		t.Fatalf("sent %v", *sent)
	}
}

func TestPushTestRefusesAnotherUsersDevice(t *testing.T) {
	for _, body := range []string{`{"endpoint":"e-other","delay_seconds":5}`, `{"endpoint":"e-other"}`} {
		h, sent, delays, _ := testHandler(nil)
		if w := doTest(h, &entity.User{ID: "u1"}, body); w.Code != http.StatusNotFound {
			t.Fatalf("%s: got %d", body, w.Code)
		}
		if len(*sent) != 0 || len(*delays) != 0 {
			t.Fatalf("%s: sent to a device the caller does not own", body)
		}
	}
}
