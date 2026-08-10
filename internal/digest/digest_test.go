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
		"command": digest.Command(value),
		"pattern": digest.Pattern(value),
		"error":   digest.Error(value),
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

// A digest must not be reversible by construction: the raw value never appears.
func TestDigestDoesNotContainValue(t *testing.T) {
	t.Parallel()

	const secret = "sk-live-abc123"
	if got := digest.Command("export TOKEN=" + secret); regexp.MustCompile(secret).MatchString(got) {
		t.Fatalf("digest %q leaked the input", got)
	}
}
