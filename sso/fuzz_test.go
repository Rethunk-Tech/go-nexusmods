package sso

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"
)

type canned []byte

func (c canned) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(bytes.NewReader(c)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

// FuzzHandle holds that a websocket sign-in message never panics, and the approval page opens only after a connection token.
func FuzzHandle(f *testing.F) {
	f.Add([]byte(`{"success":true,"data":{"connection_token":"tok"}}`))
	f.Add([]byte(`{"success":true,"data":{"api_key":"KEY"}}`))
	f.Add([]byte(`{"success":false,"error":"no"}`))
	f.Add([]byte(`[`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxTokenBody {
			return
		}
		var token string
		opened := false
		l := Legacy{
			Slug: "app", PageURL: "https://example.test/sso",
			OpenBrowser: func(string) error { return nil },
		}
		_, err := l.handle(raw, "id", &token, &opened)
		if err != nil {
			return
		}
		if opened && token == "" {
			t.Fatal("opened the page with no connection token")
		}
	})
}

// FuzzIdentify holds that a userinfo body never panics, and a kept account id is the sub as a decimal integer.
func FuzzIdentify(f *testing.F) {
	f.Add([]byte(`{"sub":"12345","name":"Someone","membership_roles":["member","lifetimepremium"]}`))
	f.Add([]byte(`{"sub":"12345","name":"Someone","membership_roles":["member","supporter"]}`))
	f.Add([]byte(`{"sub":"not-a-number"}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`[`))
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > maxTokenBody {
			return
		}
		o := OAuth{AuthBase: "http://127.0.0.1", HTTP: &http.Client{Transport: canned(body)}}
		id, err := o.Identify(t.Context(), "A1")
		if err != nil {
			return
		}
		var raw struct {
			Sub string `json:"sub"`
		}
		if json.Unmarshal(body, &raw) != nil {
			t.Fatalf("Identify accepted %q", body)
		}
		n, perr := strconv.Atoi(raw.Sub)
		if perr != nil || n != id.ID {
			t.Fatalf("id %d from sub %q", id.ID, raw.Sub)
		}
	})
}

// FuzzToken holds that a token endpoint body never panics, and a kept grant has an access token and a bounded lifetime.
func FuzzToken(f *testing.F) {
	f.Add([]byte(`{"access_token":"A1","refresh_token":"R1","expires_in":30}`))
	f.Add([]byte(`{"access_token":"A2","expires_in":3600}`))
	f.Add([]byte(`{"access_token":"A","expires_in":-1}`))
	f.Add([]byte(`{"access_token":"A","expires_in":9999999999}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`[`))
	now := time.Unix(1_700_000_000, 0)
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > maxTokenBody {
			return
		}
		o := OAuth{
			ClientID: "cid", AuthBase: "http://127.0.0.1",
			HTTP: &http.Client{Transport: canned(body)},
			Now:  func() time.Time { return now },
		}
		tok, err := o.exchange(t.Context(), url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"R"}})
		if err != nil {
			return
		}
		if tok.Access == "" {
			t.Fatal("accepted a grant with no access token")
		}
		maxLife := now.Add(time.Duration(maxExpiresIn) * time.Second)
		if tok.Expires.Before(now) || tok.Expires.After(maxLife) {
			t.Fatalf("unbounded expiry %v", tok.Expires)
		}
	})
}
