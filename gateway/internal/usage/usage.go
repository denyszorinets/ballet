// Package usage meters LLM responses passing through the gateway: it reads
// token usage from Anthropic responses (JSON or server-sent events)
// without buffering them, and hands records to a sink.
package usage

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

// Record is the usage of one LLM request.
type Record struct {
	OccurredAt   time.Time `json:"occurred_at"`
	Run          string    `json:"run"` // run token subject
	Customer     string    `json:"customer"`
	Project      string    `json:"project"`
	Ticket       string    `json:"ticket"`
	Model        string    `json:"model"`
	Status       int       `json:"status"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	CacheRead    int64     `json:"cache_read_tokens"`
	CacheWrite   int64     `json:"cache_write_tokens"`
}

// Sink receives completed records.
type Sink func(Record)

// Observe returns a proxy.Anthropic Observe hook that meters responses and
// sends one record per request to sink when the body has been read.
func Observe(sink Sink, now func() time.Time) func(runtoken.Claims, *http.Response) error {
	return func(c runtoken.Claims, resp *http.Response) error {
		rec := Record{
			OccurredAt: now(), Run: c.Subject, Customer: c.Customer, Project: c.Project, Ticket: c.Ticket,
			Status: resp.StatusCode,
		}
		stream := strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream")
		resp.Body = &meter{body: resp.Body, rec: rec, stream: stream, sink: sink}
		return nil
	}
}

// apiUsage is the usage object of the Anthropic API.
type apiUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	CacheRead    int64 `json:"cache_read_input_tokens"`
	CacheWrite   int64 `json:"cache_creation_input_tokens"`
}

// meter passes the body through, feeding a copy to a parser.
type meter struct {
	body   io.ReadCloser
	rec    Record
	stream bool
	sink   Sink

	line []byte       // partial SSE line
	all  bytes.Buffer // whole JSON body (non-streaming responses only)
	once sync.Once
}

func (m *meter) Read(p []byte) (int, error) {
	n, err := m.body.Read(p)
	if n > 0 {
		m.feed(p[:n])
	}
	if err == io.EOF {
		m.finish()
	}
	return n, err
}

func (m *meter) Close() error {
	m.finish()
	return m.body.Close()
}

func (m *meter) feed(b []byte) {
	if !m.stream {
		if m.all.Len() < 4<<20 { // usage is near the top; cap memory
			m.all.Write(b)
		}
		return
	}
	m.line = append(m.line, b...)
	for {
		i := bytes.IndexByte(m.line, '\n')
		if i < 0 {
			return
		}
		m.sseLine(m.line[:i])
		m.line = m.line[i+1:]
	}
}

// sseLine handles one "data: {...}" line of the event stream.
func (m *meter) sseLine(line []byte) {
	data, ok := bytes.CutPrefix(bytes.TrimRight(line, "\r"), []byte("data: "))
	if !ok {
		return
	}
	var ev struct {
		Type    string `json:"type"`
		Message struct {
			Model string   `json:"model"`
			Usage apiUsage `json:"usage"`
		} `json:"message"`
		Usage apiUsage `json:"usage"`
	}
	if json.Unmarshal(data, &ev) != nil {
		return
	}
	switch ev.Type {
	case "message_start":
		m.rec.Model = ev.Message.Model
		m.add(ev.Message.Usage)
	case "message_delta":
		// Output tokens in message_delta are cumulative for the message.
		m.rec.OutputTokens = ev.Usage.OutputTokens
	}
}

func (m *meter) add(u apiUsage) {
	m.rec.InputTokens, m.rec.OutputTokens = u.InputTokens, u.OutputTokens
	m.rec.CacheRead, m.rec.CacheWrite = u.CacheRead, u.CacheWrite
}

func (m *meter) finish() {
	m.once.Do(func() {
		if !m.stream {
			var body struct {
				Model string   `json:"model"`
				Usage apiUsage `json:"usage"`
			}
			if json.Unmarshal(m.all.Bytes(), &body) == nil {
				m.rec.Model = body.Model
				m.add(body.Usage)
			}
		}
		if m.sink != nil {
			m.sink(m.rec)
		}
	})
}
