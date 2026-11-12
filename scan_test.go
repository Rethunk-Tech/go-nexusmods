package nexusmods

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/go-httpkit"
)

func TestScanStatusesCachesGraphQLResponse(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || r.URL.Path != "/v2/graphql" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"modFiles":[{"fileId":12,"scannedV2":"QUARANTINED"}]}}`))
	}))
	defer server.Close()

	client := New("TestApp", "test")
	client.BaseURL = server.URL
	first, err := client.ScanStatuses(t.Context(), stardew, 30597)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.ScanStatuses(t.Context(), stardew, 30597)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || first[12] != "QUARANTINED" || second[12] != "QUARANTINED" {
		t.Fatalf("requests=%d first=%v second=%v", requests, first, second)
	}
}

func TestScanStatusesDoesNotCacheGraphQLErrors(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_, _ = w.Write([]byte(`{"errors":[{"message":"too complex"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"modFiles":[{"fileId":12,"scannedV2":"QUARANTINED"}]}}`))
	}))
	defer server.Close()

	client := New("TestApp", "test")
	client.BaseURL = server.URL
	if _, err := client.ScanStatuses(t.Context(), stardew, 30597); err == nil || !strings.Contains(err.Error(), "too complex") {
		t.Fatalf("first: %v", err)
	}
	got, err := client.ScanStatuses(t.Context(), stardew, 30597)
	if err != nil || requests != 2 || got[12] != "QUARANTINED" {
		t.Fatalf("second: requests=%d got=%v err=%v", requests, got, err)
	}
}

func TestScanStatusesReturnsACopy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"modFiles":[{"fileId":12,"scannedV2":"QUARANTINED"}]}}`))
	}))
	defer server.Close()
	client := New("TestApp", "test")
	client.BaseURL = server.URL
	first, err := client.ScanStatuses(t.Context(), stardew, 30597)
	if err != nil {
		t.Fatal(err)
	}
	delete(first, 12)
	first[99] = "mutated"
	second, err := client.ScanStatuses(t.Context(), stardew, 30597)
	if err != nil || second[12] != "QUARANTINED" || second[99] != "" {
		t.Fatalf("second=%v err=%v", second, err)
	}
}

var stardew = Title{Domain: "stardewvalley", ID: 1303}

func TestScanStatusesTellARefusedKeyFromASpentQuota(t *testing.T) {
	code := http.StatusUnauthorized
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
	defer server.Close()
	client := New("TestApp", "test")
	client.BaseURL = server.URL
	if _, err := client.ScanStatuses(t.Context(), stardew, 1); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a refused key: %v", err)
	}
	code = http.StatusTooManyRequests
	var limited *httpkit.RateLimitError
	if _, err := client.ScanStatuses(t.Context(), stardew, 2); !errors.As(err, &limited) {
		t.Fatalf("a spent quota: %v", err)
	}
}
