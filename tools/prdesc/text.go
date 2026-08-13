package main

import (
	"regexp"
	"strings"
)

var (
	htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	scriptTag   = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`)
	headingLine = regexp.MustCompile(`^##\s*(.+?)\s*$`)
	checkbox    = regexp.MustCompile(`^[-*]\s+\[([ xX])\]\s+(.+)$`)
	unchecked   = regexp.MustCompile(`(?i)^([-*]\s+)\[x\]`)
	gitFile     = regexp.MustCompile(`^diff --git a/(.+) b/(.+)$`)
)

func stripComments(s string) string {
	return htmlComment.ReplaceAllString(s, "")
}

func stripScripts(s string) string {
	return scriptTag.ReplaceAllString(s, "")
}

func normalize(s string) string {
	s = strings.TrimPrefix(s, "\uFEFF")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	raw := strings.Split(s, "\n")
	lines := make([]string, 0, len(raw))
	blank := false
	for _, line := range raw {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			if len(lines) == 0 || blank {
				continue
			}
			blank = true
			lines = append(lines, "")
			continue
		}
		blank = false
		lines = append(lines, line)
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

type section struct {
	name string
	body string
}

func parseSections(s string) []section {
	var sections []section
	var cur *section
	var body []string
	flush := func() {
		if cur == nil {
			return
		}
		cur.body = strings.TrimSpace(strings.Join(body, "\n"))
		sections = append(sections, *cur)
		body = nil
	}
	for _, line := range strings.Split(s, "\n") {
		if m := headingLine.FindStringSubmatch(line); m != nil {
			flush()
			cur = &section{name: strings.TrimSpace(m[1])}
			continue
		}
		if cur != nil {
			body = append(body, line)
		}
	}
	flush()
	return sections
}

func preamble(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if headingLine.MatchString(line) {
			return strings.TrimSpace(strings.Join(lines[:i], "\n"))
		}
	}
	return strings.TrimSpace(s)
}

func isPlaceholder(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "-" || line == "*" {
			continue
		}
		return false
	}
	return true
}

type validationItem struct {
	checked bool
	text    string
}

func parseValidation(body string) ([]validationItem, bool) {
	var items []validationItem
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := checkbox.FindStringSubmatch(line)
		if m == nil {
			return nil, false
		}
		items = append(items, validationItem{
			checked: m[1] != " ",
			text:    strings.TrimSpace(m[2]),
		})
	}
	return items, true
}

func uncheckBoxes(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = unchecked.ReplaceAllString(line, "${1}[ ]")
	}
	return strings.Join(lines, "\n")
}
