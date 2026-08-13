package digest_test

import (
	"regexp"
	"testing"

	"github.com/exequieldeferrari/axiom/internal/digest"
)

func TestDomainsDoNotCollide(t *testing.T) {
	t.Parallel()

	const value = "npm test"
	got := map[string]string{
		"command":      digest.Command(value),
		"pattern":      digest.Pattern(value),
		"error":        digest.Error(value),
		"harness_file": digest.HarnessFile([]byte(value)),
	}

	seen := make(map[string]string, len(got))
	for domain, sum := range got {
		if other, dup := seen[sum]; dup {
			t.Fatalf("domains %q and %q produced the same digest for %q", domain, other, value)
		}
		seen[sum] = domain
	}
}

func TestStableAndWellFormed(t *testing.T) {
	t.Parallel()

	hex := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for name, fn := range map[string]func(string) string{
		"Command": digest.Command,
		"Pattern": digest.Pattern,
		"Error":   digest.Error,
	} {
		first, second := fn("value"), fn("value")
		if first != second {
			t.Errorf("%s is not stable: %q != %q", name, first, second)
		}
		if !hex.MatchString(first) {
			t.Errorf("%s produced %q, want 64 lowercase hex characters", name, first)
		}
		if fn("value") == fn("other") {
			t.Errorf("%s produced the same digest for different values", name)
		}
	}
}

// A configuration file is identified by its bytes exactly as they were read.
// Anything folded together here would call two different files one file.
func TestHarnessFileIsNotNormalized(t *testing.T) {
	t.Parallel()

	base := digest.HarnessFile([]byte("# guidance\n"))
	for name, other := range map[string][]byte{
		"no trailing newline": []byte("# guidance"),
		"carriage return":     []byte("# guidance\r\n"),
		"leading whitespace":  []byte(" # guidance\n"),
		"case":                []byte("# Guidance\n"),
		"empty":               {},
	} {
		if digest.HarnessFile(other) == base {
			t.Errorf("%s digested the same as the original", name)
		}
	}
	if again := digest.HarnessFile([]byte("# guidance\n")); again != base {
		t.Error("the same bytes digested differently")
	}
}

// A digest must not be reversible by construction: the raw value never appears.
func TestDigestDoesNotContainValue(t *testing.T) {
	t.Parallel()

	const secret = "sk-live-abc123"
	if got := digest.Command("export TOKEN=" + secret); regexp.MustCompile(secret).MatchString(got) {
		t.Fatalf("digest %q leaked the input", got)
	}
}
