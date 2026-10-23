package sesv2

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	placeholderOpen  = "{{"
	placeholderClose = "}}"
	thisKeyword      = "this"
)

// renderTemplate renders scalar placeholders and dotted paths without HTML
// escaping, matching SES. Supported syntax: {{name}}, {{a.b}}, {{this.name}},
// array indexes ({{items.0}} or {{items.[0]}}), and the Handlebars escape
// \{{ for a literal "{{". A null value renders as an empty string. Advanced
// Handlebars helpers are not supported.
func renderTemplate(content *EmailTemplateContent, data string) (*EmailTemplateContent, error) {
	if content == nil {
		return nil, fmt.Errorf("template content is required")
	}

	if data == "" {
		data = "{}"
	}

	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.UseNumber()

	var values map[string]any
	if err := decoder.Decode(&values); err != nil || values == nil {
		return nil, fmt.Errorf("template data must be a JSON object")
	}

	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("template data must contain a single JSON object")
	}

	result := &EmailTemplateContent{}
	for _, part := range []struct {
		source string
		target *string
	}{
		{content.Subject, &result.Subject},
		{content.Text, &result.Text},
		{content.HTML, &result.HTML},
	} {
		rendered, err := renderTemplatePart(part.source, values)
		if err != nil {
			return nil, err
		}

		*part.target = rendered
	}

	return result, nil
}

func renderTemplatePart(source string, values map[string]any) (string, error) {
	var result strings.Builder

	for {
		start := strings.Index(source, placeholderOpen)
		if start < 0 {
			result.WriteString(source)

			return result.String(), nil
		}

		// Handlebars escaping: \{{ emits a literal "{{" and \\{{ emits a single
		// backslash followed by an evaluated placeholder.
		prefix, escaped := source[:start], false

		switch {
		case strings.HasSuffix(prefix, `\\`):
			prefix = prefix[:len(prefix)-1]
		case strings.HasSuffix(prefix, `\`):
			prefix, escaped = prefix[:len(prefix)-1], true
		}

		result.WriteString(prefix)

		source = source[start+len(placeholderOpen):]

		if escaped {
			result.WriteString(placeholderOpen)

			continue
		}

		end := strings.Index(source, placeholderClose)
		if end < 0 {
			return "", fmt.Errorf("unclosed template placeholder")
		}

		path := strings.TrimSpace(source[:end])

		value, err := templateValue(values, path)
		if err != nil {
			return "", err
		}

		result.WriteString(value)

		source = source[end+len(placeholderClose):]
	}
}

// splitTemplatePath splits a Handlebars path into segments. Segments are
// separated by dots; a segment may be wrapped in brackets, e.g. items.[0].
func splitTemplatePath(path string) ([]string, error) {
	var segments []string

	for rest := path; ; {
		var segment string

		if strings.HasPrefix(rest, "[") {
			end := strings.Index(rest, "]")
			if end < 0 {
				return nil, fmt.Errorf("unsupported template expression %q", path)
			}

			segment, rest = rest[1:end], rest[end+1:]
		} else {
			end := strings.Index(rest, ".")
			if end < 0 {
				end = len(rest)
			}

			segment, rest = rest[:end], rest[end:]
		}

		if segment == "" || strings.ContainsAny(segment, "{}#/>!&@[]()~ \t\r\n") {
			return nil, fmt.Errorf("unsupported template expression %q", path)
		}

		segments = append(segments, segment)

		if rest == "" {
			return segments, nil
		}

		if !strings.HasPrefix(rest, ".") || len(rest) == 1 {
			return nil, fmt.Errorf("unsupported template expression %q", path)
		}

		rest = rest[1:]
	}
}

func templateValue(values map[string]any, path string) (string, error) {
	if path == "else" {
		return "", fmt.Errorf("unsupported template expression %q", path)
	}

	segments, err := splitTemplatePath(path)
	if err != nil {
		return "", err
	}

	// "this" refers to the current context, which is always the root object.
	if segments[0] == thisKeyword {
		if len(segments) == 1 {
			return "", fmt.Errorf("template attribute %q must be a scalar", path)
		}

		segments = segments[1:]
	}

	var value any = values

	for _, key := range segments {
		var ok bool

		value, ok = lookupTemplateSegment(value, key)
		if !ok {
			return "", fmt.Errorf("missing template attribute %q", path)
		}
	}

	switch value := value.(type) {
	case nil:
		return "", nil
	case string:
		return value, nil
	case json.Number:
		return value.String(), nil
	case bool:
		return strconv.FormatBool(value), nil
	default:
		return "", fmt.Errorf("template attribute %q must be a scalar", path)
	}
}

// lookupTemplateSegment resolves one path segment against an object key or a
// numeric array index.
func lookupTemplateSegment(value any, key string) (any, bool) {
	switch container := value.(type) {
	case map[string]any:
		v, ok := container[key]

		return v, ok
	case []any:
		index, err := strconv.Atoi(key)
		if err != nil || index < 0 || index >= len(container) {
			return nil, false
		}

		return container[index], true
	default:
		return nil, false
	}
}

// resolveSendTemplate must be called with s.mu held.
func (s *MemoryStorage) resolveSendTemplate(template *Template) (*EmailTemplate, error) {
	if template.TemplateName == "" {
		if template.TemplateArn != "" {
			return nil, &IdentityError{
				Code:    errBadRequest,
				Message: "TemplateArn resolution is not supported; TemplateName is required",
			}
		}

		return nil, &IdentityError{Code: errBadRequest, Message: msgTemplateNameRequired}
	}

	tmpl, exists := s.EmailTemplates[template.TemplateName]
	if !exists {
		return nil, &IdentityError{Code: errNotFound, Message: msgEmailTemplateNotExist}
	}

	return tmpl, nil
}

// populateTemplatedEmail renders tmpl with data and fills the rendered fields of
// sent. It is shared by SendEmail and SendBulkEmail so both record identical
// SentEmail shapes. The render error is returned unwrapped so callers can map it
// to a request-level or per-entry failure.
func populateTemplatedEmail(sent *SentEmail, tmpl *EmailTemplate, templateName, data string) error {
	rendered, err := renderTemplate(tmpl.TemplateContent, data)
	if err != nil {
		return err
	}

	sent.Subject, sent.Body, sent.HTMLBody = rendered.Subject, rendered.Text, rendered.HTML
	sent.TemplateName = templateName
	sent.TemplateData = data

	return nil
}
