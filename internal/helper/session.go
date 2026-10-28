package helper

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
)

// SocketPath is where polkit 127's socket-activated helper listens
// (polkit-agent-helper.socket).
const SocketPath = "/run/polkit/agent-helper.socket"

// Transport is where the helper is found: Socket is tried first, and
// only when nothing exists at it is Program, the setuid
// polkit-agent-helper-1, run instead. A socket that exists but refuses
// the connection fails the attempt: falling back to a setuid helper
// polkit has moved away from would hide the broken socket.
type Transport struct {
	Socket  string
	Program string
}

// link is the conversation's channel to one helper.
type link interface {
	io.Reader
	io.Writer
	// abort stops the helper mid-conversation.
	abort()
	// finish releases the helper once it stopped talking, and returns
	// how it ended: nil, or the exit status of a helper process.
	finish() error
}

// Session is one PAM conversation through the helper. Read Events until
// it closes; the last event is Success or Failure. The responses written
// with Respond are never kept.
type Session struct {
	link   link
	events chan Event
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
	write  sync.Mutex
}

// Start connects to the helper and asks it to authenticate user for the
// polkit cookie.
func Start(ctx context.Context, t Transport, user, cookie string) (*Session, error) {
	if strings.ContainsAny(user, "\n\x00") || strings.ContainsAny(cookie, "\n\x00") || user == "" || cookie == "" {
		return nil, fmt.Errorf("helper for user %q: user and cookie must be non-empty single lines", user)
	}
	l, err := dial(ctx, t, user)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(l, cookie+"\n"); err != nil {
		l.abort()
		return nil, errors.Join(fmt.Errorf("send cookie to helper: %w", err), l.finish())
	}
	s := &Session{link: l, events: make(chan Event), stop: make(chan struct{}), done: make(chan struct{})}
	go s.read()
	return s, nil
}

func dial(ctx context.Context, t Transport, user string) (link, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", t.Socket)
	switch {
	case err == nil:
		s := &socketLink{conn: conn}
		if _, err := io.WriteString(conn, user+"\n"); err != nil {
			s.abort()
			return nil, fmt.Errorf("send user to helper socket %s: %w", t.Socket, err)
		}
		return s, nil
	case errors.Is(err, syscall.ENOENT):
		return spawn(t.Program, user)
	default:
		return nil, fmt.Errorf("connect to helper socket %s: %w", t.Socket, err)
	}
}

// Events delivers the conversation, closing after the verdict.
func (s *Session) Events() <-chan Event { return s.events }

// Respond answers the current prompt. A response holding a newline
// would be read by the helper as more than one answer, so it is refused.
func (s *Session) Respond(response string) error {
	if strings.ContainsAny(response, "\n\x00") {
		return errors.New("helper response holds a newline or NUL")
	}
	s.write.Lock()
	defer s.write.Unlock()
	if _, err := io.WriteString(s.link, response+"\n"); err != nil {
		return fmt.Errorf("answer helper: %w", err)
	}
	return nil
}

// Cancel stops the conversation, closing the socket or killing the
// helper, and returns once the helper is released and Events closed. It
// may be called after the verdict and more than once.
func (s *Session) Cancel() {
	s.once.Do(func() {
		close(s.stop)
		s.link.abort()
	})
	<-s.done
}

func (s *Session) read() {
	defer close(s.done)
	defer close(s.events)
	lines := bufio.NewScanner(s.link)
	for lines.Scan() {
		ev, err := ParseLine(lines.Text())
		if err != nil {
			s.link.abort()
			s.end(Failure{Cause: err})
			return
		}
		switch ev.(type) {
		case Success, Failure:
			s.end(ev)
			return
		}
		if !s.send(ev) {
			_ = s.link.finish()
			return
		}
	}
	cause := s.link.finish()
	if err := lines.Err(); err != nil {
		cause = errors.Join(fmt.Errorf("read helper: %w", err), cause)
	}
	if cause == nil {
		cause = errors.New("helper closed the conversation without a verdict")
	}
	s.send(Failure{Cause: cause})
}

// end delivers a verdict once the helper is released, so a Failure
// carries how the helper ended.
func (s *Session) end(verdict Event) {
	if err := s.link.finish(); err != nil {
		if f, ok := verdict.(Failure); ok {
			verdict = Failure{Cause: errors.Join(f.Cause, err)}
		}
	}
	s.send(verdict)
}

func (s *Session) send(ev Event) bool {
	select {
	case s.events <- ev:
		return true
	case <-s.stop:
		return false
	}
}

type socketLink struct {
	conn  net.Conn
	close sync.Once
}

func (l *socketLink) Read(p []byte) (int, error)  { return l.conn.Read(p) }
func (l *socketLink) Write(p []byte) (int, error) { return l.conn.Write(p) }
func (l *socketLink) abort()                      { _ = l.finish() }

func (l *socketLink) finish() error {
	var err error
	l.close.Do(func() { err = l.conn.Close() })
	if err != nil {
		return fmt.Errorf("close helper socket: %w", err)
	}
	return nil
}

type processLink struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	wait   sync.Once
	exit   error
}

func spawn(program, user string) (*processLink, error) {
	cmd := exec.Command(program, user) //nolint:gosec // the helper path is fixed at build time
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("helper %s stdin: %w", program, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("helper %s stdout: %w", program, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start helper %s: %w", program, err)
	}
	return &processLink{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

func (l *processLink) Read(p []byte) (int, error)  { return l.stdout.Read(p) }
func (l *processLink) Write(p []byte) (int, error) { return l.stdin.Write(p) }

func (l *processLink) abort() {
	_ = l.stdin.Close()
	_ = l.cmd.Process.Signal(syscall.SIGTERM)
}

func (l *processLink) finish() error {
	l.wait.Do(func() {
		_ = l.stdin.Close()
		if err := l.cmd.Wait(); err != nil {
			l.exit = fmt.Errorf("helper %s: %w", l.cmd.Path, err)
		}
	})
	return l.exit
}
