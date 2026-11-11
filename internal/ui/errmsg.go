package ui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"

	"github.com/heyjdp/vlt/internal/crypto"
	"github.com/heyjdp/vlt/internal/store"
)

// Error messages for people.
//
// The store, crypto and update packages wrap their errors with where they
// failed ("store.CopyTo \"/v\": write: …"), which is for developers and logs.
// Every error the ui shows goes through errorMessage (or showError), which
// keeps what a person can act on: known errors by meaning, a file error as
// what couldn't be done to which path and why, and otherwise the error's own
// words with the text each wrapper put in front of it dropped. Text a
// wrapper adds after the error it wraps (a server's name and what ssh said,
// "(the file at … may be incomplete)") is kept. So a new wrapper anywhere
// below needs no change here, and a message never shows a "pkg.Func:"
// prefix as long as such prefixes come before a %w.

// errorMessage returns err as text for people, led by doing (what failed,
// such as "Couldn't move the vault") when doing is not empty:
//
//	Couldn't move the vault: can't create the folder /Volumes/usb/v: permission denied
//
// The first letter is capitalised and no full stop is added, so a caller
// may go on with advice. A nil err gives just doing.
func errorMessage(doing string, err error) string {
	text := describe(err)
	switch {
	case doing == "":
	case text == "":
		text = doing
	default:
		text = doing + ": " + text
	}
	return capitalise(text)
}

// showError shows errorMessage(doing, err) in an error dialog over w.
func showError(w fyne.Window, doing string, err error) {
	dialog.ShowError(errors.New(errorMessage(doing, err)), w)
}

// isServerError reports whether err came from reaching a vault's server.
// It decides which screen and heading a failure gets, not its wording.
func isServerError(err error) bool {
	return errors.Is(err, store.ErrUnreachable) || errors.Is(err, store.ErrConnectionLost) ||
		errors.Is(err, store.ErrServerUnsupported)
}

// describe is err in plain words, without capitalisation.
func describe(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "it was cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "it took too long"
	case errors.Is(err, crypto.ErrWrongPassphrase):
		return "the passphrase is incorrect"
	}
	return withoutPrefixes(err)
}

// withoutPrefixes is err's text with each wrapper's leading text dropped
// and file errors put in words. A cause the wrapper's text doesn't repeat
// (it wrote its own message) is left as the wrapper wrote it.
func withoutPrefixes(err error) string {
	switch e := err.(type) { //nolint:errorlint // looks at this link only; the chain is walked below
	case *fs.PathError:
		return fmt.Sprintf("can't %s %s: %s", fileOp(e.Op), e.Path, fileProblem(e.Err))
	case *os.LinkError:
		return fmt.Sprintf("can't move %s to %s: %s", e.Old, e.New, fileProblem(e.Err))
	}
	text := err.Error()
	causes := unwrap(err)
	if len(causes) == 0 {
		return text
	}
	var b strings.Builder
	rest, placed := text, false
	for _, c := range causes {
		raw := c.Error()
		at := strings.Index(rest, raw)
		if at < 0 {
			continue
		}
		if placed { // text between causes (a separator) is kept; text before the first is a prefix
			b.WriteString(rest[:at])
		}
		b.WriteString(withoutPrefixes(c))
		rest, placed = rest[at+len(raw):], true
	}
	b.WriteString(rest)
	return b.String()
}

// unwrap returns the errors err directly wraps.
func unwrap(err error) []error {
	switch u := err.(type) { //nolint:errorlint // the Unwrap methods themselves, not a match in the chain
	case interface{ Unwrap() error }:
		if c := u.Unwrap(); c != nil {
			return []error{c}
		}
	case interface{ Unwrap() []error }:
		return u.Unwrap()
	}
	return nil
}

// fileOps names, in words, the operations of file errors.
var fileOps = map[string]string{
	"open":       "open",
	"read":       "read",
	"write":      "write",
	"close":      "finish writing",
	"sync":       "save",
	"mkdir":      "create the folder",
	"remove":     "delete",
	"unlinkat":   "delete",
	"rename":     "move",
	"stat":       "look at",
	"lstat":      "look at",
	"readdir":    "list",
	"readdirent": "list",
	"chmod":      "change the permissions of",
}

// fileOp is op (as in fs.PathError) in words.
func fileOp(op string) string {
	if words, ok := fileOps[op]; ok {
		return words
	}
	return op
}

// fileProblem is why a file operation failed, in words.
func fileProblem(err error) string {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	case errors.Is(err, fs.ErrNotExist):
		return "it does not exist"
	case errors.Is(err, fs.ErrExist):
		return "it already exists"
	}
	return withoutPrefixes(err)
}

// capitalise upper-cases s's first letter.
func capitalise(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}
