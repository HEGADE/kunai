package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchGrokBillingPaid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing bearer: %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"config":{"monthlyLimit":{"val":10000},"used":{"val":2500},"billingPeriodEnd":"2026-08-01T00:00:00+00:00"}}`))
	}))
	defer srv.Close()
	old := grokBillingURL
	grokBillingURL = srv.URL
	defer func() { grokBillingURL = old }()

	u, err := fetchGrokBilling(context.Background(), "tok")
	if err != nil {
		t.Fatal(err)
	}
	if u == nil || u.Weekly == nil || u.Weekly.Percent != 25 {
		t.Errorf("billing usage = %+v, want 25%% weekly", u)
	}
}

func TestFetchGrokBillingFreeReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"config":{"monthlyLimit":{"val":0},"used":{"val":0}}}`))
	}))
	defer srv.Close()
	old := grokBillingURL
	grokBillingURL = srv.URL
	defer func() { grokBillingURL = old }()

	u, err := fetchGrokBilling(context.Background(), "tok")
	if err != nil || u != nil {
		t.Errorf("free tier should map to nil billing, got %+v err=%v", u, err)
	}
}
