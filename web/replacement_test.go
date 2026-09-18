package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReplacementVerificationHandlerReturnsResult(t *testing.T) {
	handler := APIReplacementVerificationHandler(func(_ context.Context, stableID string) (ReplacementVerification, error) {
		return ReplacementVerification{
			State:               ReplacementVerified,
			PreviousStableID:    stableID,
			StableID:            "new-id",
			PreviousAddress:     "192.0.2.10:443",
			CurrentAddress:      "192.0.2.20:443",
			ReplacementDetected: true,
			Online:              true,
			Message:             "verified",
		}, nil
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/replacements/old-id/verify", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Success bool                    `json:"success"`
		Data    ReplacementVerification `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Success || response.Data.State != ReplacementVerified || response.Data.StableID != "new-id" {
		t.Fatalf("response=%#v", response)
	}
}

func TestReplacementVerificationHandlerRejectsInvalidAction(t *testing.T) {
	handler := APIReplacementVerificationHandler(nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/replacements/old-id/wrong", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
