// Package digest produces domain-separated content digests.
//
// Axiom stores digests instead of raw values for input that may carry secrets
// or user intent, such as shell commands and search patterns. Digests from
// different domains never collide, so an analyzer that groups operations by
// digest cannot treat a shell command and a search pattern with identical text
// as the same operation.
package digest

import (
	"crypto/sha256"
	"encoding/hex"
)

// sum hashes value inside domain. The NUL byte separator keeps the boundary
// unambiguous: without it, a value that begins with another domain's name
// could be made to collide across domains.
func sum(domain, value string) string {
	h := sha256.New()
	h.Write([]byte(domain))
	h.Write([]byte{0})
	h.Write([]byte(value))
	return hex.EncodeToString(h.Sum(nil))
}

// Command digests a shell command.
func Command(value string) string { return sum("command", value) }

// Pattern digests a search pattern.
func Pattern(value string) string { return sum("pattern", value) }

// Error digests an agent-reported error message.
func Error(value string) string { return sum("error", value) }
