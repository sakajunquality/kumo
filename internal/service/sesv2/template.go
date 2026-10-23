package sesv2

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// renderTemplate renders scalar placeholders and dotted paths without HTML
// escaping, matching SES. Advanced Handlebars helpers are not supported.
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
		start := strings.Index(source, "{{")
		if start < 0 {
			result.WriteString(source)

			return result.String(), nil
		}

		result.WriteString(source[:start])
		source = source[start+2:]

		end := strings.Index(source, "}}")
		if end < 0 {
			return "", fmt.Errorf("unclosed template placeholder")
		}

		path := strings.TrimSpace(source[:end])

		value, err := templateValue(values, path)
		if err != nil {
			return "", err
		}

		result.WriteString(value)

		source = source[end+2:]
	}
}

func templateValue(values map[string]any, path string) (string, error) {
	if path == "" || strings.ContainsAny(path, "{}#/>!&@[]()~ \t\r\n") || path == "else" {
		return "", fmt.Errorf("unsupported template expression %q", path)
	}

	var value any = values

	for _, key := range strings.Split(path, ".") {
		object, ok := value.(map[string]any)
		if !ok || key == "" {
			return "", fmt.Errorf("missing template attribute %q", path)
		}

		value, ok = object[key]
		if !ok {
			return "", fmt.Errorf("missing template attribute %q", path)
		}
	}

	switch value := value.(type) {
	case string:
		return value, nil
	case json.Number:
		return value.String(), nil
	case bool:
		return fmt.Sprint(value), nil
	default:
		return "", fmt.Errorf("template attribute %q must be a scalar", path)
	}
}

// resolveSendTemplate must be called with s.mu held.
func (s *MemoryStorage) resolveSendTemplate(template *Template) (*EmailTemplate, error) {
	if template.TemplateName == "" {
		return nil, &IdentityError{Code: errBadRequest, Message: "TemplateName is required"}
	}

	tmpl, exists := s.EmailTemplates[template.TemplateName]
	if !exists {
		return nil, &IdentityError{Code: errNotFound, Message: msgEmailTemplateNotExist}
	}

	return tmpl, nil
}
