package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWebhookPostSignsHeadersAndClassifiesResponses(t *testing.T) {
	var gotSignature, gotTimestamp, gotEvent, gotAttempt string
	var gotBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSignature = r.Header.Get("X-DoOps-Signature")
		gotTimestamp = r.Header.Get("X-DoOps-Timestamp")
		gotEvent = r.Header.Get("X-DoOps-Event")
		gotAttempt = r.Header.Get("X-DoOps-Attempt")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()
	d := newWebhookDispatcher(nil, 0)
	d.client = ts.Client()
	job := &webhookDeliveryJob{
		DeliveryID: "dlv_0123456789abcdef01234567", EndpointID: "wh_0123456789abcdef01234567",
		EventID: "evt_1", EventType: "execution.finished", Body: []byte(`{"id":"evt_1"}`),
		URL: ts.URL, Secret: "whsec_test", Attempt: 1,
	}
	status, category, retryable, _ := d.post(context.Background(), job)
	if status != http.StatusNoContent || category != "" || retryable {
		t.Fatalf("post = %d %q retryable=%v", status, category, retryable)
	}
	if gotEvent != job.EventType || gotAttempt != "1" {
		t.Fatalf("headers = event %q attempt %q", gotEvent, gotAttempt)
	}
	if string(gotBody) != string(job.Body) {
		t.Fatalf("body = %q", gotBody)
	}
	want := "v1=" + webhookSign(job.Secret, gotTimestamp, job.Body)
	if gotSignature != want {
		t.Fatalf("signature = %q want %q", gotSignature, want)
	}
	for _, tc := range []struct {
		code      int
		retryable bool
	}{
		{http.StatusBadRequest, false},
		{http.StatusTooManyRequests, true},
		{http.StatusServiceUnavailable, true},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.code)
		}))
		d.client = server.Client()
		testJob := *job
		testJob.URL = server.URL
		_, category, retryable, _ := d.post(context.Background(), &testJob)
		server.Close()
		if retryable != tc.retryable || category == "" {
			t.Fatalf("status %d = category %q retryable=%v, want retryable=%v", tc.code, category, retryable, tc.retryable)
		}
	}
}

func TestWebhookTLSErrorIsPermanent(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()
	d := newWebhookDispatcher(nil, 0)
	d.client = &http.Client{Transport: &http.Transport{}}
	job := &webhookDeliveryJob{URL: ts.URL, Secret: "whsec_test", Body: []byte(`{}`), Attempt: 1}
	_, category, retryable, _ := d.post(context.Background(), job)
	if retryable || category != "tls_error" {
		t.Fatalf("tls error = category %q retryable=%v", category, retryable)
	}
}

func TestWebhookRetryAfterIsCappedAtOneHour(t *testing.T) {
	if got := webhookRetryAfter("120"); got != 2*time.Minute {
		t.Fatalf("seconds = %v", got)
	}
	if got := webhookRetryAfter("999999"); got != time.Hour {
		t.Fatalf("cap = %v", got)
	}
	if got := webhookRetryAfter("not-a-date"); got != 0 {
		t.Fatalf("invalid = %v", got)
	}
	if got := webhookRetryAfter(time.Now().Add(2 * time.Minute).UTC().Format(http.TimeFormat)); got <= 0 || got > 2*time.Minute {
		t.Fatalf("date = %v", got)
	}
}
