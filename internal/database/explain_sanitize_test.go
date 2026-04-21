// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package database

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestSanitizeExplainPrefix exercises the full sanitiser end-to-end against
// fixtures taken from the live driver behaviour. Each case documents the
// aspect of the transformation it pins so that if the driver's error format
// drifts in a future release, the failure points directly at the affected
// behaviour.
func TestSanitizeExplainPrefix(t *testing.T) {
	t.Run("nil input returns nil", func(t *testing.T) {
		if got := sanitizeExplainPrefix(nil); got != nil {
			t.Fatalf("expected nil for nil input, got: %v", got)
		}
	})

	// Error doesn't match the marker: the sanitiser must be a no-op and
	// return the original error value (same identity, not just equal message).
	// This is load-bearing because executeStreaming wraps errors that never
	// touched the EXPLAIN path and relies on the original error chain — in
	// particular, errors.Is against context.DeadlineExceeded — remaining intact.
	t.Run("error without marker returned unchanged", func(t *testing.T) {
		original := errors.New("connection pool exhausted")
		got := sanitizeExplainPrefix(original)
		if got != original {
			t.Errorf("expected identity-preserved passthrough, got different error: %v", got)
		}
	})

	// The exact error captured during live stress testing against the canary.
	// Pinning this verbatim guards against silent behaviour drift: if the
	// driver's error format changes (new punctuation, different echo layout)
	// this test will fail with a clear diff pointing at which transform needs
	// adjustment.
	t.Run("live syntax error is fully sanitised", func(t *testing.T) {
		input := errors.New("Neo4jError: Neo.ClientError.Statement.SyntaxError (Invalid input 'RETURN': expected a parameter, '&', ')', ':', 'WHERE', '{' or '|' (line 1, column 26 (offset: 25))\n\"EXPLAIN MATCH (c:Company RETURN c\"\n                         ^)")
		want := "Neo4jError: Neo.ClientError.Statement.SyntaxError (Invalid input 'RETURN': expected a parameter, '&', ')', ':', 'WHERE', '{' or '|' (line 1, column 18 (offset: 17))\n\"MATCH (c:Company RETURN c\"\n                 ^)"

		got := sanitizeExplainPrefix(input).Error()
		if got != want {
			t.Errorf("sanitised output mismatch\n got:  %q\n want: %q", got, want)
		}
	})

	// Error text mentions the EXPLAIN marker implicitly — through column and
	// offset references — but the echo is already clean (no quoted EXPLAIN).
	// The marker check short-circuits and returns the input unchanged. This
	// protects against the sanitiser shifting position numbers in errors that
	// happen to mention "column N" for legitimate reasons unrelated to our
	// wrapping (for example server-side errors that cite a column in a
	// non-Cypher context).
	t.Run("error without quoted marker is not shifted", func(t *testing.T) {
		input := errors.New("some unrelated error mentioning column 42 and offset: 99")
		got := sanitizeExplainPrefix(input).Error()
		if got != input.Error() {
			t.Errorf("expected no change without EXPLAIN marker, got: %q", got)
		}
	})

	// Negative clamp: if a column or offset reference is smaller than the
	// prefix length, shifting would produce a negative number. We clamp to 0
	// instead. The scenario is not realistic in practice (the EXPLAIN prefix
	// occupies columns 1-7 so any real reference is at column 9 or later) but
	// a manipulated or synthetic error shouldn't produce negative positions.
	t.Run("small positions are clamped at zero", func(t *testing.T) {
		input := errors.New(`something at column 3 (offset: 2)
"EXPLAIN X"
  ^`)
		got := sanitizeExplainPrefix(input).Error()
		if !strings.Contains(got, "column 0") {
			t.Errorf("expected column clamped to 0, got: %q", got)
		}
		if !strings.Contains(got, "offset: 0") {
			t.Errorf("expected offset clamped to 0, got: %q", got)
		}
	})

	// Error chain preservation: the sanitiser wraps its result in a type that
	// keeps the original error reachable via errors.Unwrap / errors.Is /
	// errors.As. This is the contract that keeps F3's timeout classification
	// working: the handler does errors.Is(err, context.DeadlineExceeded)
	// against whatever it gets back from the service, and if we accidentally
	// replaced the error with a flat errors.New(msg) here, deadline checks
	// against errors that flowed through the EXPLAIN path would fail silently.
	// We synthesise a sentinel, pass it through the sanitiser, and verify
	// errors.Is still finds it.
	t.Run("error chain is preserved through sanitiser", func(t *testing.T) {
		sentinel := errors.New("underlying driver sentinel")
		wrapped := fmt.Errorf("some error with marker \"EXPLAIN bad query\"\n       ^\n(caused by: %w)", sentinel)

		sanitised := sanitizeExplainPrefix(wrapped)
		if sanitised == wrapped {
			t.Fatal("expected a new error value when marker is present, got identity passthrough")
		}
		if !errors.Is(sanitised, sentinel) {
			t.Errorf("expected errors.Is to reach the wrapped sentinel through the sanitised error, but it did not")
		}
	})
}

// TestShiftNumericMatches pins the helper's behaviour on the two patterns it's
// actually used with, plus edge cases that could silently mangle other text.
func TestShiftNumericMatches(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "single column match",
			input: "column 26",
			want:  "column 18",
		},
		{
			name:  "multiple column matches all shift",
			input: "column 26 ... later at column 40",
			want:  "column 18 ... later at column 32",
		},
		{
			name:  "column 8 shifts to 0",
			input: "at column 8",
			want:  "at column 0",
		},
		{
			name:  "column 5 clamps at 0 rather than going negative",
			input: "at column 5",
			want:  "at column 0",
		},
		{
			name:  "unrelated digits are left alone",
			input: "error 42 occurred",
			want:  "error 42 occurred",
		},
		{
			name:  "non-matching input passes through",
			input: "nothing to shift here",
			want:  "nothing to shift here",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shiftNumericMatches(tc.input, columnPattern, explainPrefixLen)
			if got != tc.want {
				t.Errorf("shiftNumericMatches(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestDedentCaretLines pins caret-line handling including cases where the
// surrounding error text contains `^` characters that must NOT be touched.
func TestDedentCaretLines(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "typical caret line is dedented",
			input: "header\n                          ^)\ntrailer",
			want:  "header\n                  ^)\ntrailer",
		},
		{
			name:  "caret at column 0 is unchanged",
			input: "header\n^\ntrailer",
			want:  "header\n^\ntrailer",
		},
		{
			name:  "leading whitespace shorter than shift is fully removed",
			input: "header\n  ^\ntrailer",
			want:  "header\n^\ntrailer",
		},
		{
			// A caret embedded in non-whitespace content (for example a regex
			// or math notation in an error string) must be preserved. Only
			// lines that LOOK like caret pointer lines — whitespace + ^ —
			// should be touched.
			name:  "caret embedded in text is not touched",
			input: "something like x^2 in the message",
			want:  "something like x^2 in the message",
		},
		{
			name:  "multiple caret lines all dedent",
			input: "a\n        ^\nb\n              ^",
			want:  "a\n^\nb\n      ^",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := dedentCaretLines(tc.input, explainPrefixLen)
			if got != tc.want {
				t.Errorf("dedentCaretLines(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
