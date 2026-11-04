package sso

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIdentify(t *testing.T) {
	answer := `{"sub":"12345","name":"Someone","membership_roles":["member","lifetimepremium"]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/userinfo" || r.Header.Get("Authorization") != "Bearer A1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(answer))
	}))
	defer srv.Close()
	o := OAuth{AuthBase: srv.URL}

	got, err := o.Identify(t.Context(), "A1")
	if err != nil || got != (Identity{ID: 12345, Name: "Someone", Premium: true}) {
		t.Fatalf("Identify = %+v, %v", got, err)
	}
	answer = `{"sub":"12345","name":"Someone","membership_roles":["member","supporter"]}`
	if got, err := o.Identify(t.Context(), "A1"); err != nil || got.Premium {
		t.Fatalf("a supporter is not premium: %+v, %v", got, err)
	}
	if _, err := o.Identify(t.Context(), "wrong"); err == nil {
		t.Fatal("a refused token identified an account")
	}
	answer = `{"sub":"not-a-number"}`
	if _, err := o.Identify(t.Context(), "A1"); err == nil {
		t.Fatal("an unreadable account id passed")
	}
}
