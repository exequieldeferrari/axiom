package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

const maxOutputBytes = 32 * 1024

const draftComment = `<!--
Generated from the pull request diff. This is a draft, not evidence.
Validation boxes are unchecked: this workflow does not run those commands.
-->`

var requiredHeadings = []string{
	"Summary",
	"What changed",
	"Evidence and semantics",
	"Validation",
	"Review focus",
}

// Sanitize turns model output into a template-shaped body.
//
// The model is allowed to draft prose. It is not allowed to decide Validation,
// invent extra sections, or leave a checked box anywhere in the body.
func Sanitize(modelOut, template string) (string, error) {
	cleaned := stripScripts(stripComments(modelOut))
	if len(cleaned) > maxOutputBytes {
		return "", fmt.Errorf("model output is %d bytes; limit is %d", len(cleaned), maxOutputBytes)
	}
	cleaned = normalize(cleaned)
	got := parseSections(cleaned)
	byName := make(map[string]string, len(got))
	for _, s := range got {
		name := canonicalHeading(s.name)
		if _, exists := byName[name]; exists {
			continue
		}
		byName[name] = strings.TrimSpace(uncheckBoxes(s.body))
	}
	for _, name := range requiredHeadings {
		if name == "Validation" {
			continue
		}
		body := byName[name]
		if body == "" || isPlaceholder(body) {
			return "", fmt.Errorf("generated %s is empty", name)
		}
	}
	validation := validationFromTemplate(template)
	if validation == "" {
		return "", fmt.Errorf("template has no Validation section")
	}

	var b strings.Builder
	b.WriteString(draftComment)
	b.WriteString("\n")
	for _, name := range requiredHeadings {
		b.WriteString("\n## ")
		b.WriteString(name)
		b.WriteString("\n\n")
		if name == "Validation" {
			b.WriteString(validation)
		} else {
			b.WriteString(byName[name])
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

func canonicalHeading(name string) string {
	for _, h := range requiredHeadings {
		if strings.EqualFold(name, h) {
			return h
		}
	}
	return name
}

func validationFromTemplate(template string) string {
	n := normalize(stripComments(template))
	for _, s := range parseSections(n) {
		if s.name != "Validation" {
			continue
		}
		items, ok := parseValidation(s.body)
		if !ok {
			return ""
		}
		lines := make([]string, 0, len(items))
		for _, it := range items {
			lines = append(lines, "- [ ] "+it.text)
		}
		return strings.Join(lines, "\n")
	}
	return ""
}

// EncodeBodyJSON wraps a Markdown body for PATCH /pulls/{n}.
func EncodeBodyJSON(body string) ([]byte, error) {
	return json.Marshal(struct {
		Body string `json:"body"`
	}{Body: body})
}
