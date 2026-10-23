package sesv2

import (
	"errors"
	"testing"
)

const (
	testDataAda      = `{"name":"Ada"}`
	testDataItems    = `{"items":["a"]}`
	testPlaceholder  = "{{name}}"
	testHelloDefault = "Hello Default"
)

func TestRenderTemplate(t *testing.T) {
	tests := []renderCase{
		{name: "repeated", source: "Hi {{ name }} {{name}}", data: testDataAda, want: "Hi Ada Ada"},
		{name: "nested", source: "{{user.name}}: {{count}} {{active}}", data: `{"user":{"name":"Ada"},"count":9007199254740993,"active":false}`, want: "Ada: 9007199254740993 false"},
		{name: "unescaped", source: "<p>{{name}}</p>", data: `{"name":"<b>A&B</b>"}`, want: "<p><b>A&B</b></p>"},
		{name: "no recursive expansion", source: testPlaceholder, data: `{"name":"{{other}}"}`, want: "{{other}}"},
		{name: "no data", source: "Hello", want: "Hello"},
		{name: "missing", source: testPlaceholder, data: `{}`, wantError: true},
		{name: "missing nested", source: "{{user.name}}", data: `{"user":{}}`, wantError: true},
		{name: "invalid JSON", data: `{`, wantError: true},
		{name: "trailing JSON", data: `{} {}`, wantError: true},
		{name: "array", data: `[]`, wantError: true},
		{name: "null", data: `null`, wantError: true},
		{name: "object value", source: "{{user}}", data: `{"user":{}}`, wantError: true},
		{name: "unclosed", source: "{{name", wantError: true},
		{name: "unclosed after escape", source: `\{{name}} {{other`, data: `{}`, wantError: true},
		{name: "helper", source: "{{#each users}}", wantError: true},
	}

	runRenderCases(t, tests)
}

type renderCase struct {
	name, source, data, want string
	wantError                bool
}

func runRenderCases(t *testing.T, tests []renderCase) {
	t.Helper()

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

func TestRenderTemplate_Syntax(t *testing.T) {
	runRenderCases(t, []renderCase{
		{name: "escaped placeholder", source: `\{{name}}`, data: testDataAda, want: testPlaceholder},
		{name: "escaped placeholder keeps rest literal", source: `a \{{ b }} c`, data: `{}`, want: "a {{ b }} c"},
		{name: "escaped then evaluated", source: `\{{name}} {{name}}`, data: testDataAda, want: "{{name}} Ada"},
		{name: "double backslash evaluates", source: `\\{{name}}`, data: testDataAda, want: `\Ada`},
		{name: "lone backslash is literal", source: `C:\dir {{name}}`, data: testDataAda, want: `C:\dir Ada`},
		{name: "null renders empty", source: "[{{name}}]", data: `{"name":null}`, want: "[]"},
		{name: "null parent is missing", source: "{{a.b}}", data: `{"a":null}`, wantError: true},
		{name: "this prefix", source: "{{this.name}} {{ this.user.name }}", data: `{"name":"Ada","user":{"name":"Bob"}}`, want: "Ada Bob"},
		{name: "bare this is not scalar", source: "{{this}}", data: testDataAda, wantError: true},
		{name: "array index", source: "{{items.0}} {{items.1.name}}", data: `{"items":["a",{"name":"b"}]}`, want: "a b"},
		{name: "bracket array index", source: "{{items.[0]}} {{items.[1].name}}", data: `{"items":["a",{"name":"b"}]}`, want: "a b"},
		{name: "array index out of range", source: "{{items.2}}", data: testDataItems, wantError: true},
		{name: "array index not numeric", source: "{{items.x}}", data: testDataItems, wantError: true},
		{name: "array value is not scalar", source: "{{items}}", data: testDataItems, wantError: true},
		{name: "unterminated bracket", source: "{{items.[0}}", data: testDataItems, wantError: true},
		{name: "empty bracket", source: "{{items.[]}}", data: testDataItems, wantError: true},
		{name: "trailing dot", source: "{{items.}}", data: testDataItems, wantError: true},
	})
}

func TestSendEmail_Template(t *testing.T) {
	storage := NewMemoryStorage()
	newBulkSendFixture(t, storage)

	req := &SendEmailRequest{
		FromEmailAddress: testSenderEmail,
		Destination:      &Destination{ToAddresses: []string{testRecipientEmail}},
		Content: &EmailContent{Template: &Template{
			TemplateName: testTemplateName,
			TemplateData: testDataAda,
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
				ReplacementTemplate: &ReplacementTemplate{ReplacementTemplateData: new(`{}`)},
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

	for i, want := range []string{"Hello A", "Hello B", testHelloDefault, testHelloDefault} {
		if got := storage.SentEmails[i].Body; got != want {
			t.Errorf("email %d body = %q, want %q", i, got, want)
		}
	}
}
