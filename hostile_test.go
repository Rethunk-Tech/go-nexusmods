package nexusmods

import (
	"bufio"
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Rethunk-Tech/go-safefs"
)

type answer struct {
	code   int
	header map[string]string
	body   []byte
}

// hostile serves every request with the answer last stored in a; header values are set as given, odd bytes
// included. One server serves a whole fuzz run: a server per input runs the machine out of ports.
func hostile(t testing.TB, a *atomic.Pointer[answer]) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cur := a.Load()
		for k, v := range cur.header {
			w.Header()[k] = []string{v}
		}
		w.WriteHeader(cur.code)
		_, _ = w.Write(cur.body)
	}))
	t.Cleanup(srv.Close)
	c := New("TestApp", "1.0")
	c.BaseURL = srv.URL
	return c.WithKey("k")
}

func serveOnce(t testing.TB, code int, body []byte) *Client {
	var a atomic.Pointer[answer]
	a.Store(&answer{code: code, body: body})
	return hostile(t, &a)
}

// FuzzHostileAnswer holds that no status, budget or Retry-After header, and no body, makes a call panic.
func FuzzHostileAnswer(f *testing.F) {
	f.Add(200, "0", "9999-12-31T23:59:59Z", "", []byte(`{"user_id":1}`))
	f.Add(429, "-9223372036854775808", "not a date", "99999999999999999999", []byte(`{}`))
	f.Add(429, "1", "", "Wed, 21 Oct 2015 07:28:00 GMT", []byte(nil))
	f.Add(302, "x", "", "-1", []byte(strings.Repeat("[", 20000)))
	var a atomic.Pointer[answer]
	a.Store(&answer{code: 200})
	c := hostile(f, &a)
	f.Fuzz(func(t *testing.T, code int, remaining, reset, retryAfter string, body []byte) {
		if code < 200 || code > 599 || code/100 == 3 {
			code = 200
		}
		a.Store(&answer{code: code, body: body, header: map[string]string{
			"X-Rl-Daily-Remaining": remaining, "X-Rl-Hourly-Remaining": remaining,
			"X-Rl-Daily-Reset": reset, "X-Rl-Hourly-Reset": reset, "Retry-After": retryAfter,
		}})
		_, _ = c.Validate(t.Context())
		_, _ = c.Page(t.Context(), stardew, 1)
		_, _ = c.ModsByDomain(t.Context(), stardew.Domain, []int{1})
		_ = c.Limits()
	})
}

func TestHostileBodiesFailCleanly(t *testing.T) {
	t.Parallel()
	for name, body := range map[string][]byte{
		"deeply nested": []byte(strings.Repeat(`{"a":`, 20000)),
		"over the cap":  bytes.Repeat([]byte(" "), maxBody+1),
	} {
		if _, err := serveOnce(t, http.StatusOK, body).Validate(t.Context()); err == nil {
			t.Errorf("%s: no error", name)
		} else if name == "over the cap" && !errors.Is(err, safefs.ErrTooLarge) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestALyingContentLengthFails(t *testing.T) {
	t.Parallel()
	for _, length := range []string{"1099511627776", "-1", "2, 3"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			conn, rw, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = conn.Close() }()
			writeRaw(rw, "HTTP/1.1 200 OK\r\nContent-Length: "+length+"\r\n\r\n{}")
		}))
		c := New("TestApp", "1.0")
		c.BaseURL = srv.URL
		_, err := c.WithKey("k").Validate(t.Context())
		srv.Close()
		if err == nil {
			t.Errorf("Content-Length %s: no error", strconv.Quote(length))
		}
	}
}

func writeRaw(rw *bufio.ReadWriter, s string) {
	_, _ = rw.WriteString(s)
	_ = rw.Flush()
}

func TestAResetFarAheadDoesNotStopTheClientForGood(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		for _, k := range []string{"X-Rl-Daily-Remaining", "X-Rl-Hourly-Remaining"} {
			w.Header().Set(k, "0")
		}
		for _, k := range []string{"X-Rl-Daily-Reset", "X-Rl-Hourly-Reset"} {
			w.Header().Set(k, "9999-12-31T23:59:59Z")
		}
		_, _ = w.Write([]byte(`{"user_id":1,"name":"x"}`))
	}))
	t.Cleanup(srv.Close)
	c := New("TestApp", "1.0")
	c.BaseURL = srv.URL
	c = c.WithKey("k")
	_, _ = c.Validate(t.Context())
	_, _ = c.Validate(t.Context())
	if hits.Load() != 2 {
		t.Fatalf("a reset in year 9999 refused every later call: %d requests sent", hits.Load())
	}
}
