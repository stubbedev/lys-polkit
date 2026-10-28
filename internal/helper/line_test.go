package helper_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stubbedev/lys-polkit/internal/helper"
	"github.com/stubbedev/lys-polkit/internal/helper/helpertest"
)

func TestParseLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		line string
		want helper.Event
	}{
		{line: "PAM_PROMPT_ECHO_OFF Password: ", want: helper.Prompt{Text: "Password: "}},
		{line: "PAM_PROMPT_ECHO_ON Username: ", want: helper.Prompt{Text: "Username: ", Echo: true}},
		{line: "PAM_PROMPT_ECHO_OFF ", want: helper.Prompt{}},
		{line: "PAM_ERROR_MSG Sorry, try again.", want: helper.Error{Text: "Sorry, try again."}},
		{line: "PAM_TEXT_INFO Place your finger", want: helper.Info{Text: "Place your finger"}},
		{line: `PAM_TEXT_INFO a\nb\tc`, want: helper.Info{Text: "a\nb\tc"}},
		{line: `PAM_TEXT_INFO \b\f\r\v`, want: helper.Info{Text: "\b\f\r\v"}},
		{line: `PAM_TEXT_INFO back\\slash \"quoted\"`, want: helper.Info{Text: `back\slash "quoted"`}},
		{line: `PAM_TEXT_INFO \342\234\213 hand`, want: helper.Info{Text: "✋ hand"}},
		{line: `PAM_TEXT_INFO \0`, want: helper.Info{Text: "\x00"}},
		{line: `PAM_TEXT_INFO \18`, want: helper.Info{Text: "\x018"}},
		{line: `PAM_TEXT_INFO \1234`, want: helper.Info{Text: "S4"}},
		{line: `PAM_TEXT_INFO \q`, want: helper.Info{Text: "q"}},
		{line: "polkit-agent-helper-1: pam_authenticate failed: Authentication failure", want: helper.Diagnostic{Text: "pam_authenticate failed: Authentication failure"}},
		{line: `polkit-agent-helper-1: raw \n`, want: helper.Diagnostic{Text: `raw \n`}},
		{line: "SUCCESS", want: helper.Success{}},
		{line: "FAILURE", want: helper.Failure{}},
	}
	for _, tc := range tests {
		t.Run(tc.line, func(t *testing.T) {
			t.Parallel()

			got, err := helper.ParseLine(tc.line)
			if err != nil {
				t.Fatalf("ParseLine: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseLine = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestParseLineRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		line string
		want string
	}{
		{line: "", want: `unknown helper line ""`},
		{line: "HELLO there", want: `unknown helper line "HELLO there"`},
		{line: "PAM_PROMPT_ECHO_OFF", want: "unknown helper line"},
		{line: "SUCCESS ", want: "unknown helper line"},
		{line: "success", want: "unknown helper line"},
		{line: `PAM_TEXT_INFO trailing\`, want: "trailing backslash"},
		{line: `PAM_TEXT_INFO \777`, want: "past a byte"},
	}
	for _, tc := range tests {
		t.Run(tc.line, func(t *testing.T) {
			t.Parallel()

			got, err := helper.ParseLine(tc.line)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ParseLine = %#v, %v; want an error with %q", got, err, tc.want)
			}
		})
	}
}

func TestUnescapeUndoesEscape(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"", "plain", helpertest.FingerInfo, "\x00\x01\x7f\xff", `\"\\`} {
		got, err := helper.Unescape(helpertest.Escape(s))
		if err != nil || got != s {
			t.Errorf("Unescape(Escape(%q)) = %q, %v", s, got, err)
		}
	}
}

func FuzzParseLine(f *testing.F) {
	for _, seed := range []string{
		"PAM_PROMPT_ECHO_OFF Password: ", `PAM_TEXT_INFO \342\234\213\n`, "SUCCESS", "FAILURE",
		`PAM_ERROR_MSG \`, `PAM_TEXT_INFO \7777`, "PAM_PROMPT_ECHO_ON ",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, line string) {
		ev, err := helper.ParseLine(line)
		if (ev == nil) == (err == nil) {
			t.Fatalf("ParseLine(%q) = %#v, %v: want exactly one of an event and an error", line, ev, err)
		}
		if prefix, rest, ok := strings.Cut(line, " "); ok && prefix == "PAM_TEXT_INFO" {
			if text, uerr := helper.Unescape(rest); uerr == nil {
				if again, rerr := helper.Unescape(helpertest.Escape(text)); rerr != nil || again != text {
					t.Fatalf("Escape round trip of %q = %q, %v", text, again, rerr)
				}
			}
		}
	})
}
