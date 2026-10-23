package sesv2

import (
	"errors"
	"testing"
)

func TestSendEmail_ContentVariants(t *testing.T) {
	tests := []struct {
		name    string
		content *EmailContent
		message string
	}{
		{name: "no variant set", content: &EmailContent{}, message: "Content must contain one of Simple, Raw, or Template"},
		{
			name:    "template ARN only",
			content: &EmailContent{Template: &Template{TemplateArn: "arn:aws:ses:us-east-1:123456789012:template/promo"}},
			message: "TemplateArn resolution is not supported; TemplateName is required",
		},
		{name: "template without name", content: &EmailContent{Template: &Template{}}, message: msgTemplateNameRequired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := NewMemoryStorage()

			_, err := storage.SendEmail(t.Context(), &SendEmailRequest{
				FromEmailAddress: testSenderEmail,
				Destination:      &Destination{ToAddresses: []string{testRecipientEmail}},
				Content:          tt.content,
			})

			var apiErr *IdentityError
			if !errors.As(err, &apiErr) || apiErr.Code != errBadRequest || apiErr.Message != tt.message {
				t.Fatalf("expected BadRequestException %q, got %v", tt.message, err)
			}

			if len(storage.SentEmails) != 0 {
				t.Fatal("rejected sends must not be recorded")
			}
		})
	}
}

func TestSendEmail_ClonesDestination(t *testing.T) {
	storage := NewMemoryStorage()
	dest := &Destination{ToAddresses: []string{testRecipientEmail}}

	_, err := storage.SendEmail(t.Context(), &SendEmailRequest{
		FromEmailAddress: testSenderEmail,
		Destination:      dest,
		Content: &EmailContent{Simple: &SimpleEmail{
			Subject: &Content{Data: "S"},
			Body:    &Body{Text: &Content{Data: "B"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	dest.ToAddresses[0] = "mutated@example.com"

	if got := storage.SentEmails[0].Destination.ToAddresses[0]; got != testRecipientEmail {
		t.Fatalf("stored destination aliases the request: %q", got)
	}
}

func TestSendBulkEmail_ReplacementDataSemantics(t *testing.T) {
	tests := []struct {
		name        string
		replacement *string
		wantStatus  string
		wantBody    string
	}{
		{name: "absent uses default", replacement: nil, wantStatus: statusSuccess, wantBody: testHelloDefault},
		{name: "present replaces default", replacement: new(`{"name":"Entry"}`), wantStatus: statusSuccess, wantBody: "Hello Entry"},
		{name: "empty object is authoritative", replacement: new(`{}`), wantStatus: statusFailed},
		{name: "empty string is invalid JSON", replacement: new(``), wantStatus: statusFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := NewMemoryStorage()
			req := newBulkSendFixture(t, storage)
			req.DefaultContent.Template.TemplateData = `{"name":"Default"}`
			req.BulkEmailEntries = []BulkEmailEntry{{
				Destination:             &Destination{ToAddresses: []string{testRecipientEmail}},
				ReplacementEmailContent: &ReplacementEmailContent{ReplacementTemplate: &ReplacementTemplate{ReplacementTemplateData: tt.replacement}},
			}}

			resp, err := storage.SendBulkEmail(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}

			result := resp.BulkEmailEntryResults[0]
			if result.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q (error %q)", result.Status, tt.wantStatus, result.Error)
			}

			if tt.wantStatus == statusFailed {
				if result.Error == "" || result.MessageID != "" || len(storage.SentEmails) != 0 {
					t.Fatalf("failed entry must carry an error, no MessageId, and not be recorded: %#v", result)
				}

				return
			}

			if got := storage.SentEmails[0].Body; got != tt.wantBody {
				t.Fatalf("body = %q, want %q", got, tt.wantBody)
			}
		})
	}
}

func TestSendBulkEmail_FailedEntriesCarryNoMessageID(t *testing.T) {
	storage := NewMemoryStorage()
	req := newBulkSendFixture(t, storage)
	req.DefaultContent.Template.TemplateData = `{}`
	req.BulkEmailEntries = []BulkEmailEntry{
		{ /* destination missing */ },
		{Destination: &Destination{ToAddresses: []string{testRecipientEmail}}}, // render failure: name missing
	}

	resp, err := storage.SendBulkEmail(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}

	for i, result := range resp.BulkEmailEntryResults {
		if result.Status != statusFailed || result.Error == "" || result.MessageID != "" {
			t.Errorf("entry %d: unexpected result %#v", i, result)
		}
	}
}

func TestSendEmailAndBulk_RecordSameShape(t *testing.T) {
	storage := NewMemoryStorage()
	req := newBulkSendFixture(t, storage)
	req.BulkEmailEntries = req.BulkEmailEntries[:1]
	data := *req.BulkEmailEntries[0].ReplacementEmailContent.ReplacementTemplate.ReplacementTemplateData

	if _, err := storage.SendBulkEmail(t.Context(), req); err != nil {
		t.Fatal(err)
	}

	if _, err := storage.SendEmail(t.Context(), &SendEmailRequest{
		FromEmailAddress: testSenderEmail,
		Destination:      &Destination{ToAddresses: []string{testRecipientEmail}},
		Content:          &EmailContent{Template: &Template{TemplateName: testTemplateName, TemplateData: data}},
	}); err != nil {
		t.Fatal(err)
	}

	bulk, single := storage.SentEmails[0], storage.SentEmails[1]
	if bulk.Subject != single.Subject || bulk.Body != single.Body || bulk.HTMLBody != single.HTMLBody ||
		bulk.TemplateName != single.TemplateName || bulk.TemplateData != single.TemplateData {
		t.Fatalf("bulk and single sends diverge:\nbulk:   %#v\nsingle: %#v", bulk, single)
	}
}
