// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package database

import (
	"regexp"
	"strconv"
	"strings"
)

// explainPrefixLen is the length of the "EXPLAIN " wrapper that GetQueryType
// and EstimateRowCount prepend to user queries for read/write classification
// and planner-row-count estimation. Shifted positions, offsets, and caret
// indents in sanitised error messages all use this value; hoisting it to a
// named constant keeps the intent visible rather than strewing the magic
// number 8 through three places.
const explainPrefixLen = len("EXPLAIN ")

// explainQuoteMarker is the substring we scan for when deciding whether an
// error was produced against our EXPLAIN-wrapped query. Neo4j syntax errors
// echo the offending query inside double quotes, so if we see the literal
// `"EXPLAIN ` in an error message we know the driver is reporting on our
// wrapped version rather than on a query the user submitted directly.
// Errors from code paths that don't wrap (for example executeStreaming's
// context-error branch) will not contain this marker and pass through
// untouched, which keeps sanitizeExplainPrefix safe to apply unconditionally.
const explainQuoteMarker = `"EXPLAIN `

// columnPattern and offsetPattern match the position references that Neo4j
// syntax errors include, typically shaped as "(line 1, column 26 (offset:
// 25))". Using regex rather than a hand-written scan keeps the intent visible
// at a glance — these are two specific, well-shaped occurrences, and \d+ is
// the most natural way to express their numeric boundary.
//
// Deliberately narrow: we match exactly "column N" and "offset: N", not
// other places digits might appear in an error message (line numbers,
// hash codes, server-returned counts). Shifting the wrong numbers would
// degrade the error rather than improve it.
var (
	columnPattern = regexp.MustCompile(`column (\d+)`)
	offsetPattern = regexp.MustCompile(`offset: (\d+)`)
)

// sanitisedError wraps an underlying error so that Error() returns a
// sanitised message while Unwrap() still exposes the original. This matters
// for two reasons:
//
//   1. errors.Is / errors.As. Upstream callers may want to recognise a
//      Neo4jError or a context cancellation wrapped somewhere deep in the
//      chain. Replacing the error entirely with errors.New(sanitisedMsg)
//      would break those checks silently. F3's handler-side classification
//      already depends on errors.Is(err, context.DeadlineExceeded) working
//      through wrapper layers; this keeps that contract intact for any
//      error type, not just context errors.
//
//   2. Debugging. slog emits the Error() output but Go's error-chain tools
//      (%+v verbs, custom formatters) can still walk to the underlying
//      driver error when needed. That preserves diagnostic fidelity while
//      the user-facing message stays clean.
type sanitisedError struct {
	msg   string
	inner error
}

func (e *sanitisedError) Error() string { return e.msg }

func (e *sanitisedError) Unwrap() error { return e.inner }

// sanitizeExplainPrefix removes the internally-injected "EXPLAIN " wrapper
// from driver error messages so callers see positions, offsets, and echoes
// that match the query they actually submitted. It is safe to apply
// unconditionally: errors that don't reference our wrapped query (no
// explainQuoteMarker substring) are returned unchanged.
//
// The three transformations performed are, in order:
//
//  1. Strip the literal `"EXPLAIN ` → `"` from the quoted query echo. This
//     is the most visible leak — the user sees a query they never typed.
//
//  2. Subtract explainPrefixLen from every "column N" and "offset: N"
//     reference. Position references are reported by the server against the
//     query text we sent (which starts with EXPLAIN); after stripping the
//     prefix from the echo, the positions need the same shift to continue
//     pointing at the right character.
//
//  3. Dedent caret lines by explainPrefixLen. The caret is drawn with leading
//     whitespace matching the column number of the reported error. Having
//     shifted the column itself, the caret's indentation must shift in
//     lockstep or the visual pointer will drift from the token it names.
//
// The three steps together produce an error that reads as if the user's
// original query had been sent directly, without our internal wrapping.
// Negative positions and offsets are clamped at 0 rather than passed through
// as-is; a negative column would be worse UX than a column of zero pointing
// at the start of the echo.
//
// If the driver's error message format changes in a future driver release,
// the unit tests for this function will fail with enough detail to point at
// which of the three transformations needs updating.
func sanitizeExplainPrefix(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if !strings.Contains(msg, explainQuoteMarker) {
		return err
	}
	msg = strings.ReplaceAll(msg, explainQuoteMarker, `"`)
	msg = shiftNumericMatches(msg, columnPattern, explainPrefixLen)
	msg = shiftNumericMatches(msg, offsetPattern, explainPrefixLen)
	msg = dedentCaretLines(msg, explainPrefixLen)
	return &sanitisedError{msg: msg, inner: err}
}

// shiftNumericMatches replaces every match of re in s with the same template
// text but with the captured decimal integer (submatch group 1) shifted by
// -shift. A pattern that fails to produce a submatch, or whose capture isn't
// parseable as an integer, is left untouched so malformed inputs don't mangle
// the surrounding text.
//
// The expected use is with columnPattern and offsetPattern, which each capture
// exactly one decimal group.
func shiftNumericMatches(s string, re *regexp.Regexp, shift int) string {
	return re.ReplaceAllStringFunc(s, func(match string) string {
		// Re-run the regex on the matched substring to recover the capture
		// groups. ReplaceAllStringFunc gives us the full match text only; it
		// doesn't pass through submatches. The alternative (FindAllSubmatchIndex
		// plus manual slicing) is strictly more code for no clarity gain on
		// matches this short.
		groups := re.FindStringSubmatch(match)
		if len(groups) < 2 {
			return match
		}
		n, parseErr := strconv.Atoi(groups[1])
		if parseErr != nil {
			return match
		}
		adjusted := n - shift
		if adjusted < 0 {
			adjusted = 0
		}
		return strings.Replace(match, groups[1], strconv.Itoa(adjusted), 1)
	})
}

// dedentCaretLines locates lines that consist of leading whitespace followed
// by a caret (`^`) and removes up to n leading spaces from them. The caret in
// the driver's echo points at the offending token in the quoted query above;
// having shrunk that quoted query by n characters (the "EXPLAIN " prefix), we
// must shift the caret left by the same amount to keep the pointer aligned
// with its target token.
//
// Lines that don't match the caret shape are skipped. This matters because the
// surrounding error text may include other `^` characters (in a regex pattern
// quoted in the message, say) that we must not touch.
//
// If the leading whitespace is shorter than n — which would only happen with
// a pathologically small column value — we remove all of it rather than going
// negative. The resulting caret will sit at column 0, which is a visually
// obvious signal that the shift clamped rather than a silent off-by-one.
func dedentCaretLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		if !strings.HasPrefix(trimmed, "^") {
			continue
		}
		leading := len(line) - len(trimmed)
		if leading == 0 {
			continue
		}
		removed := n
		if removed > leading {
			removed = leading
		}
		lines[i] = line[removed:]
	}
	return strings.Join(lines, "\n")
}
