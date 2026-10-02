// Package docker runs sessions in fresh containers through the Docker
// Engine HTTP API, which Podman's compatible API also serves (ADR-0009).
package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/denyszorinets/ballet/kit/runnerproto"
)

// APIVersion is the Engine API version used (Docker 20.10+, Podman 3+).
const APIVersion = "v1.41"

// Workspace is where the run's workspace is inside the container; spec
// workdirs are relative to it.
const Workspace = "/workspace"

// Backend executes runs in containers.
type Backend struct {
	client  *http.Client
	baseURL string
	// NanoCPUs and MemoryBytes limit every container (0: unlimited).
	NanoCPUs    int64
	MemoryBytes int64
	// Network is the container network ("" : the engine default).
	Network string
	// StopGrace is how long a cancelled container gets before it is
	// killed (default 10 s).
	StopGrace time.Duration
}

// New returns a backend for host: "unix:///var/run/docker.sock",
// "tcp://host:2375" or an http(s) URL.
func New(host string) (*Backend, error) {
	u, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("docker host %q: %w", host, err)
	}
	switch u.Scheme {
	case "unix":
		sock := u.Path
		tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}}
		return &Backend{client: &http.Client{Transport: tr}, baseURL: "http://docker"}, nil
	case "tcp":
		return &Backend{client: &http.Client{}, baseURL: "http://" + u.Host}, nil
	case "http", "https":
		return &Backend{client: &http.Client{}, baseURL: strings.TrimSuffix(host, "/")}, nil
	}
	return nil, fmt.Errorf("docker host %q: scheme must be unix, tcp, http or https", host)
}

// apiError is an error response of the Engine API.
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string { return fmt.Sprintf("docker: %d: %s", e.Status, e.Message) }

func (b *Backend) do(ctx context.Context, method, p string, q url.Values, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(data)
	}
	u := b.baseURL + "/" + APIVersion + p
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker: %w", err)
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		var m struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &m)
		if m.Message == "" {
			m.Message = strings.TrimSpace(string(data))
		}
		return nil, &apiError{Status: resp.StatusCode, Message: m.Message}
	}
	return resp, nil
}

func (b *Backend) call(ctx context.Context, method, p string, q url.Values, body, out any) error {
	resp, err := b.do(ctx, method, p, q, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Ping checks that the engine answers.
func (b *Backend) Ping(ctx context.Context) error {
	return b.call(ctx, http.MethodGet, "/_ping", nil, nil, nil)
}

// Run executes spec in a fresh container and streams its output.
func (b *Backend) Run(ctx context.Context, runID string, spec runnerproto.Spec, out func(stream, text string)) (int, error) {
	if spec.Image == "" {
		return -1, errors.New("docker backend: the run spec has no image")
	}
	if err := b.ensureImage(ctx, spec.Image, out); err != nil {
		return -1, err
	}
	env := make([]string, 0, len(spec.Env))
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	workdir := path.Join(Workspace, strings.TrimPrefix(path.Clean("/"+spec.Workdir), "/"))
	host := map[string]any{"Init": true}
	if b.NanoCPUs > 0 {
		host["NanoCpus"] = b.NanoCPUs
	}
	if b.MemoryBytes > 0 {
		host["Memory"] = b.MemoryBytes
	}
	if b.Network != "" {
		host["NetworkMode"] = b.Network
	}
	var created struct {
		ID string `json:"Id"`
	}
	// Creation and clean-up must not be cut short by a cancellation.
	bg := context.WithoutCancel(ctx)
	err := b.call(bg, http.MethodPost, "/containers/create", url.Values{"name": {"ballet-run-" + runID}}, map[string]any{
		"Image": spec.Image, "Cmd": spec.Command, "Env": env, "WorkingDir": workdir,
		"Labels":     map[string]string{"ballet.run": runID},
		"HostConfig": host,
	}, &created)
	if err != nil {
		return -1, fmt.Errorf("create container: %w", err)
	}
	defer func() {
		_ = b.call(bg, http.MethodDelete, "/containers/"+created.ID, url.Values{"force": {"1"}, "v": {"1"}}, nil, nil)
	}()
	if err := b.call(bg, http.MethodPost, "/containers/"+created.ID+"/start", nil, nil, nil); err != nil {
		return -1, fmt.Errorf("start container: %w", err)
	}
	out(runnerproto.StreamSystem, fmt.Sprintf("docker backend: container %.12s from %s\n", created.ID, spec.Image))

	logsDone := make(chan error, 1)
	go func() { logsDone <- b.follow(bg, created.ID, out) }()

	waited := make(chan int, 1)
	waitErr := make(chan error, 1)
	go func() {
		var w struct {
			StatusCode int `json:"StatusCode"`
			Error      *struct {
				Message string `json:"Message"`
			} `json:"Error"`
		}
		if err := b.call(bg, http.MethodPost, "/containers/"+created.ID+"/wait", nil, nil, &w); err != nil {
			waitErr <- err
			return
		}
		waited <- w.StatusCode
	}()

	select {
	case code := <-waited:
		<-logsDone
		return code, nil
	case err := <-waitErr:
		return -1, fmt.Errorf("wait for container: %w", err)
	case <-ctx.Done():
		grace := b.StopGrace
		if grace <= 0 {
			grace = 10 * time.Second
		}
		_ = b.call(bg, http.MethodPost, "/containers/"+created.ID+"/stop",
			url.Values{"t": {fmt.Sprint(int(grace.Seconds()))}}, nil, nil)
		<-logsDone
		return -1, ctx.Err()
	}
}

// ensureImage pulls image unless the engine has it.
func (b *Backend) ensureImage(ctx context.Context, image string, out func(stream, text string)) error {
	err := b.call(ctx, http.MethodGet, "/images/"+image+"/json", nil, nil, nil)
	var api *apiError
	if err == nil {
		return nil
	}
	if !errors.As(err, &api) || api.Status != http.StatusNotFound {
		return fmt.Errorf("inspect image: %w", err)
	}
	out(runnerproto.StreamSystem, "pulling "+image+"\n")
	name, tag := image, "latest"
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		name, tag = image[:i], image[i+1:]
	}
	resp, err := b.do(ctx, http.MethodPost, "/images/create", url.Values{"fromImage": {name}, "tag": {tag}}, nil)
	if err != nil {
		return fmt.Errorf("pull %s: %w", image, err)
	}
	defer resp.Body.Close()
	// The pull reports progress as JSON lines; errors arrive in-band.
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		var m struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &m) == nil && m.Error != "" {
			return fmt.Errorf("pull %s: %s", image, m.Error)
		}
	}
	return sc.Err()
}

// follow streams a container's output until it ends. Without a TTY the
// engine multiplexes stdout and stderr in frames with an 8-byte header.
func (b *Backend) follow(ctx context.Context, id string, out func(stream, text string)) error {
	resp, err := b.do(ctx, http.MethodGet, "/containers/"+id+"/logs",
		url.Values{"follow": {"1"}, "stdout": {"1"}, "stderr": {"1"}}, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)
	var header [8]byte
	for {
		if _, err := io.ReadFull(r, header[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		size := binary.BigEndian.Uint32(header[4:])
		payload := make([]byte, size)
		if _, err := io.ReadFull(r, payload); err != nil {
			return err
		}
		stream := runnerproto.StreamStdout
		if header[0] == 2 {
			stream = runnerproto.StreamStderr
		}
		out(stream, string(payload))
	}
}
