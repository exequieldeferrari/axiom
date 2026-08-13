package main

// Eligible reports whether a pull request body is empty or still the blank
// template, and therefore may be replaced by a generated description.
//
// The model never makes this decision. A body is eligible only when, after
// HTML comments are stripped, it is empty or contains the template headings
// with placeholder bullets and every Validation box still unchecked.
func Eligible(body, template string) bool {
	bodyN := normalize(stripComments(body))
	if bodyN == "" {
		return true
	}
	if preamble(bodyN) != "" {
		return false
	}
	tmplN := normalize(stripComments(template))
	want := parseSections(tmplN)
	got := parseSections(bodyN)
	if len(want) == 0 || len(want) != len(got) {
		return false
	}
	for i := range want {
		if want[i].name != got[i].name {
			return false
		}
		if want[i].name == "Validation" {
			if !validationUnchanged(want[i].body, got[i].body) {
				return false
			}
			continue
		}
		if !isPlaceholder(got[i].body) {
			return false
		}
	}
	return true
}

func validationUnchanged(wantBody, gotBody string) bool {
	want, ok := parseValidation(wantBody)
	if !ok {
		return false
	}
	got, ok := parseValidation(gotBody)
	if !ok || len(want) != len(got) {
		return false
	}
	for i := range want {
		if got[i].checked || want[i].text != got[i].text {
			return false
		}
	}
	return true
}
