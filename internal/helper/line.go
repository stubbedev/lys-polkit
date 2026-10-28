// Package helper runs one PAM conversation through polkit's agent
// helper, polkit-agent-helper-1, over its socket or as the setuid
// program, and reports each step as a typed Event.
package helper

import (
	"errors"
	"fmt"
	"strings"
)

// Event is one line of the helper's conversation: Prompt, Info, Error,
// Success or Failure, or a Diagnostic, which a Session logs instead of
// delivering.
type Event interface{ event() }

// Prompt asks for one answer, shown as typed when Echo is set.
type Prompt struct {
	Text string
	Echo bool
}

// Info is a PAM_TEXT_INFO message, such as a fingerprint reader's
// "Place your finger".
type Info struct{ Text string }

// Error is a PAM_ERROR_MSG message; the conversation goes on.
type Error struct{ Text string }

// Success is the helper's verdict that the user authenticated; the
// helper has told polkitd itself.
type Success struct{}

// Failure ends the conversation without authenticating. Cause is nil
// when the helper said FAILURE, else why it ended without a verdict.
type Failure struct{ Cause error }

// Diagnostic is a message the helper wrote to stderr, such as
// "pam_authenticate failed: Authentication failure". polkit 127's
// socket-activated helper unit leaves stderr on the socket, so these
// arrive among the protocol lines.
type Diagnostic struct{ Text string }

func (Prompt) event()     {}
func (Diagnostic) event() {}
func (Info) event()       {}
func (Error) event()      {}
func (Success) event()    {}
func (Failure) event()    {}

func (f Failure) String() string {
	if f.Cause == nil {
		return "authentication failed"
	}
	return "authentication failed: " + f.Cause.Error()
}

// The line prefixes polkit-agent-helper-1 writes (polkitagentsession.c).
const (
	promptEchoOff = "PAM_PROMPT_ECHO_OFF "
	promptEchoOn  = "PAM_PROMPT_ECHO_ON "
	errorMsg      = "PAM_ERROR_MSG "
	textInfo      = "PAM_TEXT_INFO "
	diagnostic    = "polkit-agent-helper-1: "
	success       = "SUCCESS"
	failure       = "FAILURE"
)

// lineKinds are the helper's lines that carry text, by prefix.
var lineKinds = [...]struct {
	prefix string
	make   func(string) Event
}{
	{promptEchoOff, func(s string) Event { return Prompt{Text: s} }},
	{promptEchoOn, func(s string) Event { return Prompt{Text: s, Echo: true} }},
	{errorMsg, func(s string) Event { return Error{Text: s} }},
	{textInfo, func(s string) Event { return Info{Text: s} }},
}

// ParseLine reads one line from the helper, without its newline. The
// helper escapes the text after a PAM prefix with g_strescape; a verdict
// is the bare word.
func ParseLine(line string) (Event, error) {
	switch line {
	case success:
		return Success{}, nil
	case failure:
		return Failure{}, nil
	}
	if text, ok := strings.CutPrefix(line, diagnostic); ok {
		return Diagnostic{Text: text}, nil
	}
	for _, k := range lineKinds {
		escaped, ok := strings.CutPrefix(line, k.prefix)
		if !ok {
			continue
		}
		text, err := Unescape(escaped)
		if err != nil {
			return nil, fmt.Errorf("helper line %q: %w", line, err)
		}
		return k.make(text), nil
	}
	return nil, fmt.Errorf("unknown helper line %q", line)
}

var errTrailingBackslash = errors.New("trailing backslash")

// Unescape undoes g_strescape as g_strcompress does: \b \f \n \r \t \v,
// one to three octal digits for a byte, and a backslash before any other
// character for that character. A lone trailing backslash, which
// g_strescape never writes, is an error, as is an octal value past a
// byte.
func Unescape(s string) (string, error) {
	if !strings.Contains(s, `\`) {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i == len(s) {
			return "", errTrailingBackslash
		}
		switch c = s[i]; c {
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte('\v')
		case '0', '1', '2', '3', '4', '5', '6', '7':
			v := 0
			end := min(i+3, len(s))
			for ; i < end && s[i] >= '0' && s[i] <= '7'; i++ {
				v = v*8 + int(s[i]-'0')
			}
			i--
			if v > 0xff {
				return "", fmt.Errorf("octal escape \\%o is past a byte", v)
			}
			b.WriteByte(byte(v))
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), nil
}
