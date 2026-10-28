package helper_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/sockdir"

	"github.com/stubbedev/lys-polkit/internal/helper"
	"github.com/stubbedev/lys-polkit/internal/helper/helpertest"
)

func TestMain(m *testing.M) {
	helpertest.MaybeRun()
	os.Exit(m.Run())
}

const eventTimeout = 5 * time.Second

type transport struct {
	name string
	make func(*testing.T, *helpertest.Fake) helper.Transport
}

var transports = []transport{
	{"setuid", func(_ *testing.T, f *helpertest.Fake) helper.Transport { return f.Setuid() }},
	{"socket", func(t *testing.T, f *helpertest.Fake) helper.Transport { return f.Socket(t) }},
}

func next(t *testing.T, s *helper.Session) helper.Event {
	t.Helper()
	select {
	case ev, ok := <-s.Events():
		if !ok {
			t.Fatal("events closed before a verdict")
		}
		return ev
	case <-time.After(eventTimeout):
		t.Fatal("no helper event within " + eventTimeout.String())
		return nil
	}
}

func start(t *testing.T, tr helper.Transport) *helper.Session {
	t.Helper()
	s, err := helper.Start(t.Context(), tr, helpertest.User, helpertest.Cookie)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Cancel)
	return s
}

type step struct {
	want    helper.Event
	respond string
}

func TestSessionConversations(t *testing.T) {
	t.Parallel()

	password := helper.Prompt{Text: helpertest.PasswordPrompt}
	tests := []struct {
		name     string
		scenario helpertest.Scenario
		steps    []step
	}{
		{name: "a prompt answered", scenario: helpertest.Answer, steps: []step{
			{want: password, respond: helpertest.Password},
			{want: helper.Success{}},
		}},
		{name: "two prompts in a row", scenario: helpertest.TwoPrompts, steps: []step{
			{want: helper.Prompt{Text: helpertest.UserPrompt, Echo: true}, respond: helpertest.User},
			{want: password, respond: helpertest.Password},
			{want: helper.Success{}},
		}},
		{name: "info only", scenario: helpertest.InfoOnly, steps: []step{
			{want: helper.Info{Text: helpertest.FingerInfo}},
			{want: helper.Success{}},
		}},
		{name: "failure", scenario: helpertest.Answer, steps: []step{
			{want: password, respond: "wrong"},
			{want: helper.Error{Text: helpertest.SorryError}},
			{want: helper.Failure{}},
		}},
	}
	for _, tr := range transports {
		for _, tc := range tests {
			t.Run(tr.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				fake := helpertest.New(t, tc.scenario)
				s := start(t, tr.make(t, fake))
				for _, st := range tc.steps {
					if got := next(t, s); !reflect.DeepEqual(got, st.want) {
						t.Fatalf("event %#v, want %#v", got, st.want)
					}
					if st.respond != "" {
						if err := s.Respond(st.respond); err != nil {
							t.Fatalf("Respond: %v", err)
						}
					}
				}
				if ev, ok := <-s.Events(); ok {
					t.Errorf("event %#v after the verdict", ev)
				}
				s.Cancel()
				fake.AssertNoneLeft(t)
			})
		}
	}
}

func TestSessionEOFBeforeAVerdictFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		transport transport
		cause     string
	}{
		{transports[0], "exit status 3"},
		{transports[1], "without a verdict"},
	}
	for _, tc := range tests {
		t.Run(tc.transport.name, func(t *testing.T) {
			t.Parallel()

			fake := helpertest.New(t, helpertest.HangUp)
			s := start(t, tc.transport.make(t, fake))
			if got := next(t, s); got != (helper.Prompt{Text: helpertest.PasswordPrompt}) {
				t.Fatalf("event %#v, want the password prompt", got)
			}
			if err := s.Respond(helpertest.Password); err != nil {
				t.Fatalf("Respond: %v", err)
			}
			f, ok := next(t, s).(helper.Failure)
			if !ok || f.Cause == nil || !strings.Contains(f.Cause.Error(), tc.cause) {
				t.Fatalf("verdict %#v, want a Failure caused by %q", f, tc.cause)
			}
			s.Cancel()
			fake.AssertNoneLeft(t)
		})
	}
}

func TestSessionCancelDuringAPrompt(t *testing.T) {
	t.Parallel()

	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			t.Parallel()

			fake := helpertest.New(t, helpertest.Wait)
			s := start(t, tr.make(t, fake))
			if got := next(t, s); got != (helper.Prompt{Text: helpertest.PasswordPrompt}) {
				t.Fatalf("event %#v, want the password prompt", got)
			}
			s.Cancel()
			if ev, ok := <-s.Events(); ok {
				t.Errorf("event %#v after Cancel", ev)
			}
			s.Cancel()
			fake.AssertNoneLeft(t)
		})
	}
}

func TestStartWithABrokenSocketDoesNotFallBack(t *testing.T) {
	t.Parallel()

	fake := helpertest.New(t, helpertest.Answer)
	path := filepath.Join(sockdir.Dir(t), "stale")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := helper.Start(t.Context(), helper.Transport{Socket: path, Program: fake.Setuid().Program}, helpertest.User, helpertest.Cookie)
	if err == nil || !strings.Contains(err.Error(), "connect to helper socket "+path) {
		if s != nil {
			s.Cancel()
		}
		t.Fatalf("Start = %v, want the socket's connect error", err)
	}
	if n := fake.Started(t); n != 0 {
		t.Errorf("%d setuid helpers started, want none", n)
	}
}

func TestStartRefuses(t *testing.T) {
	t.Parallel()

	fake := helpertest.New(t, helpertest.Answer)
	tests := []struct {
		name         string
		tr           helper.Transport
		user, cookie string
		want         string
	}{
		{name: "no socket and no program", tr: helper.Transport{Socket: filepath.Join(sockdir.Dir(t), "none"), Program: filepath.Join(t.TempDir(), "none")}, user: helpertest.User, cookie: helpertest.Cookie, want: "start helper"},
		{name: "a user over two lines", tr: fake.Setuid(), user: "alice\nroot", cookie: helpertest.Cookie, want: "single lines"},
		{name: "a cookie over two lines", tr: fake.Setuid(), user: helpertest.User, cookie: "a\nb", want: "single lines"},
		{name: "no user", tr: fake.Setuid(), user: "", cookie: helpertest.Cookie, want: "non-empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if s, err := helper.Start(t.Context(), tc.tr, tc.user, tc.cookie); err == nil || !strings.Contains(err.Error(), tc.want) {
				if s != nil {
					s.Cancel()
				}
				t.Fatalf("Start = %v, want an error with %q", err, tc.want)
			}
		})
	}
	if n := fake.Started(t); n != 0 {
		t.Errorf("%d helpers started for refused requests, want none", n)
	}
}

func TestRespondRefusesANewline(t *testing.T) {
	t.Parallel()

	fake := helpertest.New(t, helpertest.Wait)
	s := start(t, fake.Setuid())
	next(t, s)
	err := s.Respond("hunter2\nmore")
	if err == nil {
		t.Fatal("Respond accepted a response over two lines")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("Respond's error %q carries the response", err)
	}
	s.Cancel()
	fake.AssertNoneLeft(t)
}
