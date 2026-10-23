//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/aws/smithy-go"
)

func createRenderingTemplate(t *testing.T, client *sesv2.Client, name string) {
	t.Helper()

	_, err := client.CreateEmailTemplate(t.Context(), &sesv2.CreateEmailTemplateInput{
		TemplateName: aws.String(name),
		TemplateContent: &types.EmailTemplateContent{
			Subject: aws.String("Hello {{contact.name}}"),
			Text:    aws.String("Welcome {{contact.name}}!"),
			Html:    aws.String("<p>{{contact.name}}</p>"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_, _ = client.DeleteEmailTemplate(context.Background(), &sesv2.DeleteEmailTemplateInput{
			TemplateName: aws.String(name),
		})
	})
}

func assertRenderedEmail(t *testing.T, messageID, name string) {
	t.Helper()

	resp, err := http.Get(testEndpoint() + "/kumo/ses/v2/sent-emails")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var result struct {
		SentEmails []struct {
			MessageID string `json:"MessageId"`
			Subject   string
			Body      string
			HTMLBody  string
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	for _, email := range result.SentEmails {
		if email.MessageID != messageID {
			continue
		}

		if email.Subject != "Hello "+name || email.Body != "Welcome "+name+"!" || email.HTMLBody != "<p>"+name+"</p>" {
			t.Fatalf("unexpected rendered email: %#v", email)
		}

		return
	}

	t.Fatalf("sent message %q not found", messageID)
}

func TestSESv2_SendEmail_TemplateRendering(t *testing.T) {
	client := newSESv2Client(t)
	name := "single-rendering"
	createRenderingTemplate(t, client, name)

	input := &sesv2.SendEmailInput{
		FromEmailAddress: aws.String("rendering@example.com"),
		Destination:      &types.Destination{ToAddresses: []string{"recipient@example.com"}},
		Content: &types.EmailContent{Template: &types.Template{
			TemplateName: aws.String(name),
			TemplateData: aws.String(`{"contact":{"name":"Ada & Bob"}}`),
		}},
	}
	output, err := client.SendEmail(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}

	assertRenderedEmail(t, aws.ToString(output.MessageId), "Ada & Bob")

	input.Content.Template.TemplateData = aws.String(`{}`)
	_, err = client.SendEmail(t.Context(), input)

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "BadRequestException" {
		t.Fatalf("expected BadRequestException, got %v", err)
	}

	input.Content.Template.TemplateName = aws.String("missing-rendering-template")
	_, err = client.SendEmail(t.Context(), input)
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "NotFoundException" {
		t.Fatalf("expected NotFoundException, got %v", err)
	}
}

func TestSESv2_SendBulkEmail_TemplateRendering(t *testing.T) {
	client := newSESv2Client(t)
	name := "bulk-rendering"
	createRenderingTemplate(t, client, name)

	entries := make([]types.BulkEmailEntry, 0, 3)
	for _, data := range []string{`{"contact":{"name":"Ada"}}`, `{}`, ""} {
		entry := types.BulkEmailEntry{
			Destination: &types.Destination{ToAddresses: []string{"recipient@example.com"}},
		}
		if data != "" {
			entry.ReplacementEmailContent = &types.ReplacementEmailContent{
				ReplacementTemplate: &types.ReplacementTemplate{ReplacementTemplateData: aws.String(data)},
			}
		}

		entries = append(entries, entry)
	}

	output, err := client.SendBulkEmail(t.Context(), &sesv2.SendBulkEmailInput{
		FromEmailAddress: aws.String("bulk-rendering@example.com"),
		DefaultContent: &types.BulkEmailContent{Template: &types.Template{
			TemplateName: aws.String(name),
			TemplateData: aws.String(`{"contact":{"name":"Default"}}`),
		}},
		BulkEmailEntries: entries,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(output.BulkEmailEntryResults) != 3 {
		t.Fatalf("expected three entry results, got %d", len(output.BulkEmailEntryResults))
	}

	for i, want := range []types.BulkEmailStatus{types.BulkEmailStatusSuccess, types.BulkEmailStatusFailed, types.BulkEmailStatusSuccess} {
		if got := output.BulkEmailEntryResults[i].Status; got != want {
			t.Fatalf("entry %d status %q, want %q", i, got, want)
		}
	}

	assertRenderedEmail(t, aws.ToString(output.BulkEmailEntryResults[0].MessageId), "Ada")
	assertRenderedEmail(t, aws.ToString(output.BulkEmailEntryResults[2].MessageId), "Default")
}
