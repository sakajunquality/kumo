package sesv2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendEmail_ErrorStatus(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "unknown template is not found",
			body:       `{"Destination":{"ToAddresses":["a@example.com"]},"Content":{"Template":{"TemplateName":"missing"}}}`,
			wantStatus: http.StatusNotFound,
			wantCode:   errNotFound,
		},
		{
			name:       "empty content is a bad request",
			body:       `{"Destination":{"ToAddresses":["a@example.com"]},"Content":{}}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   errBadRequest,
		},
		{
			name:       "missing destination is a bad request",
			body:       `{"Content":{"Simple":{"Subject":{"Data":"S"}}}}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   errBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := New(NewMemoryStorage())

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/ses/v2/email/outbound-emails", strings.NewReader(tt.body))
			svc.SendEmail(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}

			var resp ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}

			if !strings.HasSuffix(resp.Type, tt.wantCode) {
				t.Fatalf("error type = %q, want %q", resp.Type, tt.wantCode)
			}
		})
	}
}
