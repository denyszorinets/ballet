package process

import (
	"bufio"
	"encoding/json"
	"io"
	"sync"

	"github.com/denyszorinets/ballet/agent/internal/driver"
	"github.com/denyszorinets/ballet/kit/runnerproto"
)

// maxLine bounds one line of a session's output.
const maxLine = 16 << 20

// session talks to a coding-agent session's process through its driver:
// it sends the prompt, normalizes the output into events, and closes
// standard input when a turn ends with nothing more to say, which ends
// the session.
type session struct {
	drv    driver.Driver
	prompt string

	mu     sync.Mutex
	stdin  io.WriteCloser
	last   *runnerproto.Result
	turns  int // over all results: each reports its own
	closed bool
}

// begin sends the prompt.
func (s *session) begin(stdin io.WriteCloser) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stdin = stdin
	if _, err := stdin.Write(s.drv.Message(s.prompt)); err != nil {
		s.closeLocked()
	}
}

// read normalizes standard output until it closes.
func (s *session) read(wg *sync.WaitGroup, r io.Reader, out func(stream, text string)) {
	defer wg.Done()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	for sc.Scan() {
		p := s.drv.Parse(sc.Bytes())
		for _, e := range p.Events {
			b, _ := json.Marshal(e)
			out(runnerproto.StreamEvent, string(b)+"\n")
		}
		if p.Result != nil {
			s.turnEnded(p.Result)
		}
	}
	// Drain what a too long line left, so the process does not block.
	_, _ = io.Copy(io.Discard, r)
}

func (s *session) turnEnded(r *runnerproto.Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turns += r.Turns
	last := *r
	last.Turns = s.turns
	s.last = &last
	s.closeLocked()
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
