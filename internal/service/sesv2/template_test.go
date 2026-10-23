package sesv2

import (
	"errors"
	"testing"
)

func TestRenderTemplate(t *testing.T) {
	tests := []struct {
		name, source, data, want string
		wantError                bool
	}{
		{name: "repeated", source: "Hi {{ name }} {{name}}", data: `{"name":"Ada"}`, want: "Hi Ada Ada"},
		{name: "nested", source: "{{user.name}}: {{count}} {{active}}", data: `{"user":{"name":"Ada"},"count":9007199254740993,"active":false}`, want: "Ada: 9007199254740993 false"},
		{name: "unescaped", source: "<p>{{name}}</p>", data: `{"name":"<b>A&B</b>"}`, want: "<p><b>A&B</b></p>"},
		{name: "no recursive expansion", source: "{{name}}", data: `{"name":"{{other}}"}`, want: "{{other}}"},
		{name: "no data", source: "Hello", want: "Hello"},
		{name: "missing", source: "{{name}}", data: `{}`, wantError: true},
		{name: "missing nested", source: "{{user.name}}", data: `{"user":{}}`, wantError: true},
		{name: "invalid JSON", data: `{`, wantError: true},
		{name: "trailing JSON", data: `{} {}`, wantError: true},
		{name: "array", data: `[]`, wantError: true},
		{name: "null", data: `null`, wantError: true},
		{name: "object value", source: "{{user}}", data: `{"user":{}}`, wantError: true},
		{name: "unclosed", source: "{{name", wantError: true},
		{name: "helper", source: "{{#each users}}", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := &EmailTemplateContent{Subject: tt.source, Text: tt.source, HTML: tt.source}
			got, err := renderTemplate(content, tt.data)

			if tt.wantError {
				if err == nil {
					t.Fatal("expected rendering error")
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if got.Subject != tt.want || got.Text != tt.want || got.HTML != tt.want {
				t.Fatalf("rendered %#v, want all parts %q", got, tt.want)
			}

			if content.Subject != tt.source || content.Text != tt.source || content.HTML != tt.source {
				t.Fatal("rendering modified the stored template")
			}
		})
	}
}

func TestSendEmail_Template(t *testing.T) {
	storage := NewMemoryStorage()
	newBulkSendFixture(t, storage)

	req := &SendEmailRequest{
		FromEmailAddress: testSenderEmail,
		Destination:      &Destination{ToAddresses: []string{testRecipientEmail}},
		Content: &EmailContent{Template: &Template{
			TemplateName: testTemplateName,
			TemplateData: `{"name":"Ada"}`,
		}},
	}

	id, err := storage.SendEmail(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}

	sent, err := storage.GetSentEmails(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if len(sent) != 1 || sent[0].MessageID != id || sent[0].Body != "Hello Ada" || sent[0].TemplateName != testTemplateName {
		t.Fatalf("unexpected sent emails: %#v", sent)
	}

	req.Content.Template.TemplateData = `{}`
	if _, err := storage.SendEmail(t.Context(), req); err == nil {
		t.Fatal("expected missing attribute error")
	}

	req.Content.Template.TemplateName = "missing"
	_, err = storage.SendEmail(t.Context(), req)

	var apiErr *IdentityError
	if !errors.As(err, &apiErr) || apiErr.Code != errNotFound {
		t.Fatalf("expected NotFoundException, got %v", err)
	}

	if len(storage.SentEmails) != 1 {
		t.Fatal("failed sends must not be recorded")
	}
}

func TestSendBulkEmail_Rendering(t *testing.T) {
	storage := NewMemoryStorage()
	req := newBulkSendFixture(t, storage)
	req.DefaultContent.Template.TemplateData = `{"name":"Default"}`
	req.BulkEmailEntries = append(req.BulkEmailEntries,
		BulkEmailEntry{Destination: &Destination{ToAddresses: []string{testRecipientEmail}}},
		BulkEmailEntry{
			Destination: &Destination{ToAddresses: []string{testRecipientEmail}},
			ReplacementEmailContent: &ReplacementEmailContent{
				ReplacementTemplate: &ReplacementTemplate{ReplacementTemplateData: `{}`},
			},
		},
		BulkEmailEntry{Destination: &Destination{ToAddresses: []string{testRecipientEmail}}},
	)

	resp, err := storage.SendBulkEmail(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}

	failed := resp.BulkEmailEntryResults[3]
	if failed.Status != "FAILED" || failed.Error == "" || failed.MessageID != "" {
		t.Fatalf("unexpected failed entry: %#v", failed)
	}

	if len(storage.SentEmails) != 4 {
		t.Fatalf("expected four successful sends, got %d", len(storage.SentEmails))
	}

	for i, want := range []string{"Hello A", "Hello B", "Hello Default", "Hello Default"} {
		if got := storage.SentEmails[i].Body; got != want {
			t.Errorf("email %d body = %q, want %q", i, got, want)
		}
	}
}
