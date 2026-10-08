package process

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/denyszorinets/ballet/agent/internal/driver"
	"github.com/denyszorinets/ballet/kit/runnerproto"
)

// maxLine bounds one line of a session's output.
const maxLine = 16 << 20

// Errors of Input.
var (
	ErrNoSession     = errors.New("no session of this run is running here")
	ErrSessionEnded  = errors.New("the session has ended")
	ErrNoInterrupt   = errors.New("the session's runtime cannot be interrupted")
	ErrInvalidInput  = errors.New("invalid input")
	errNoMessageText = errors.New("a message needs text")
)

// session talks to a coding-agent session's process through its driver:
// it sends the prompt, normalizes the output into events, and delivers
// human messages when a turn ends. A turn that ends with nothing more to
// say closes standard input, which ends the session.
type session struct {
	drv    driver.Driver
	prompt string
	out    func(stream, text string)

	mu     sync.Mutex
	stdin  io.WriteCloser
	queue  []string // messages for the next turn
	last   *runnerproto.Result
	turns  int // over all results: each reports its own
	closed bool
}

// begin sends the prompt.
func (s *session) begin(stdin io.WriteCloser) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stdin = stdin
	s.writeLocked(s.drv.Message(s.prompt))
}

// read normalizes standard output until it closes.
func (s *session) read(wg *sync.WaitGroup, r io.Reader) {
	defer wg.Done()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	for sc.Scan() {
		p := s.drv.Parse(sc.Bytes())
		for _, e := range p.Events {
			s.event(e)
		}
		if p.Result != nil {
			s.turnEnded(p.Result)
		}
	}
	// Drain what a too long line left, so the process does not block.
	_, _ = io.Copy(io.Discard, r)
}

func (s *session) event(e runnerproto.Event) {
	b, _ := json.Marshal(e)
	s.out(runnerproto.StreamEvent, string(b)+"\n")
}

// turnEnded delivers the next message, or ends the session.
func (s *session) turnEnded(r *runnerproto.Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turns += r.Turns
	last := *r
	last.Turns = s.turns
	s.last = &last
	if len(s.queue) == 0 {
		s.closeLocked()
		return
	}
	text := s.queue[0]
	s.queue = s.queue[1:]
	s.event(runnerproto.Event{Kind: runnerproto.EventUser, Text: text})
	s.writeLocked(s.drv.Message(text))
}

// input takes a human's input: a message waits for the end of the
// current turn; an interrupt stops the turn first.
func (s *session) input(kind, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSessionEnded
	}
	switch kind {
	case runnerproto.InputMessage:
		if text == "" {
			return errors.Join(ErrInvalidInput, errNoMessageText)
		}
		s.queue = append(s.queue, text)
	case runnerproto.InputInterrupt:
		stop := s.drv.Interrupt()
		if stop == nil {
			return ErrNoInterrupt
		}
		if text != "" {
			s.queue = append(s.queue, text)
		}
		s.writeLocked(stop)
	default:
		return ErrInvalidInput
	}
	return nil
}

func (s *session) writeLocked(b []byte) {
	if s.closed {
		return
	}
	if _, err := s.stdin.Write(b); err != nil {
		s.closeLocked()
	}
}

// end closes standard input once the process stopped talking.
func (s *session) end() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLocked()
}

func (s *session) closeLocked() {
	if !s.closed && s.stdin != nil {
		s.closed = true
		_ = s.stdin.Close()
	}
}

func (s *session) result() *runnerproto.Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}
