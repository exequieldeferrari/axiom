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
func sum(domain string, value []byte) string {
	h := sha256.New()
	h.Write([]byte(domain))
	h.Write([]byte{0})
	h.Write(value)
	return hex.EncodeToString(h.Sum(nil))
}

// Command digests a shell command.
func Command(value string) string { return sum("command", []byte(value)) }

// Pattern digests a search pattern.
func Pattern(value string) string { return sum("pattern", []byte(value)) }

// Error digests an agent-reported error message.
func Error(value string) string { return sum("error", []byte(value)) }

// HarnessFile digests an observed configuration file.
//
// The hashed input is the file's exact bytes as they were read, in full and
// unaltered: no trimming, no line-ending translation, no decoding, no
// re-encoding. A file that differs by one byte digests differently, which is
// the only property the observation needs and the only one it promises.
// Normalizing anything here would silently call two different files one file.
func HarnessFile(content []byte) string { return sum("harness_file", content) }
