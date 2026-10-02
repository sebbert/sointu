// Package mcp lets a language model read and change the patch of a running
// tracker or plugin, through the Model Context Protocol.
//
// A tracker or a plugin instance (a Host) listens on a unix socket in the
// user's configuration directory, if the user turned it on. The command
// sointu-mcp, which an MCP client like Claude Code starts and talks to over
// its standard input and output, lists the instances that are running and
// passes each tool call on to one of them (see client.go). So nothing
// listens on a network port, and what can connect is what can open a file
// that only the user can.
//
// A tool call reaches the model as a func() message on the broker, the way
// a plugin host's request for the state does: it runs on the goroutine that
// owns the model, between two frames of the GUI, and never on the audio
// thread. The tools themselves are tracker.Remote.
package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vsariola/sointu/tracker"
	"github.com/vsariola/sointu/version"
)

type (
	// Host serves the tools for one model: a tracker, or an instance of a
	// plugin.
	Host struct {
		model  *tracker.Model
		broker *tracker.Broker
		kind   string
		id     string
		start  time.Time

		mu       sync.Mutex
		listener net.Listener
		socket   string
		infoFile string
	}

	// request is what the command sends over the socket, as a line of JSON,
	// and response what it gets back.
	request struct {
		Tool string          `json:"tool"`
		Args json.RawMessage `json:"args,omitempty"`
	}
	response struct {
		Text  string `json:"text,omitempty"`
		Error string `json:"error,omitempty"`
	}

	// Info tells the command about a Host. The file of a Host has the
	// first fields; the Host itself answers with all of them.
	Info struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"` // e.g. sointu-track, sointu-clap
		PID     int    `json:"pid"`
		Socket  string `json:"socket"`
		Version string `json:"version,omitempty"`
		Started string `json:"started,omitempty"`
		// Process is the program the Host runs in: the tracker, or the
		// plugin host
		Process     string   `json:"process,omitempty"`
		File        string   `json:"file,omitempty"`
		Instruments []string `json:"instruments,omitempty"`
	}
)

// modelTimeout is how long a tool call waits for the model, which is busy
// while the GUI draws a frame.
const modelTimeout = 10 * time.Second

var hostCount atomic.Int32

// NewHost returns a Host for the model, not listening yet. kind names the
// program, e.g. "sointu-track".
func NewHost(model *tracker.Model, kind string) *Host {
	return &Host{
		model:  model,
		broker: model.Broker(),
		kind:   kind,
		id:     fmt.Sprintf("%s-%d-%d", kind, os.Getpid(), hostCount.Add(1)),
		start:  time.Now(),
	}
}

// Dir returns the directory with the files of the running Hosts: mcp in the
// sointu directory of the user's configuration directory.
func Dir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "sointu", "mcp"), nil
}

// Enabled reports whether the Host is listening.
func (h *Host) Enabled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.listener != nil
}

// SetEnabled starts or stops listening.
func (h *Host) SetEnabled(on bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if on == (h.listener != nil) {
		return nil
	}
	if !on {
		h.listener.Close()
		os.Remove(h.socket)
		os.Remove(h.infoFile)
		h.listener = nil
		return nil
	}
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	socket := filepath.Join(dir, h.id+".sock")
	if len(socket) > 100 {
		// the path of a unix socket is at most 104 bytes on macOS: a
		// directory of the user's own in the temporary directory instead
		tmp := filepath.Join(os.TempDir(), "sointu-mcp-"+strconv.Itoa(os.Getuid()))
		if err := os.MkdirAll(tmp, 0700); err != nil {
			return err
		}
		if err := os.Chmod(tmp, 0700); err != nil {
			return err
		}
		socket = filepath.Join(tmp, h.id+".sock")
	}
	os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	if err := os.Chmod(socket, 0600); err != nil {
		listener.Close()
		return err
	}
	info, _ := json.Marshal(Info{ID: h.id, Kind: h.kind, PID: os.Getpid(), Socket: socket})
	infoFile := filepath.Join(dir, h.id+".json")
	if err := os.WriteFile(infoFile, info, 0600); err != nil {
		listener.Close()
		os.Remove(socket)
		return err
	}
	h.listener, h.socket, h.infoFile = listener, socket, infoFile
	go h.serve(listener)
	return nil
}

// Close stops listening.
func (h *Host) Close() { h.SetEnabled(false) }

func (h *Host) serve(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return // closed
		}
		go h.handle(conn)
	}
}

// handle answers the requests of a connection: a line of JSON each.
func (h *Host) handle(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReaderSize(conn, 1<<16)
	enc := json.NewEncoder(conn)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		var req request
		var resp response
		if err := json.Unmarshal(line, &req); err != nil {
			resp.Error = "malformed request: " + err.Error()
		} else if text, err := h.call(req); err != nil {
			resp.Error = err.Error()
		} else {
			resp.Text = text
		}
		if enc.Encode(resp) != nil {
			return
		}
	}
}

func (h *Host) call(req request) (text string, err error) {
	defer func() {
		// a tool must not take the tracker, or the plugin host, down
		if r := recover(); r != nil {
			err = fmt.Errorf("the tool %s failed: %v", req.Tool, r)
		}
	}()
	if req.Tool == infoTool {
		return h.info()
	}
	tool, ok := FindTool(req.Tool)
	if !ok {
		return "", fmt.Errorf("the tracker has no tool %q: it may be an older version than the sointu-mcp command", req.Tool)
	}
	return tool.call(h, req.Args)
}

// infoTool is the request that a Host answers with its Info.
const infoTool = "_info"

func (h *Host) info() (string, error) {
	info := Info{ID: h.id, Kind: h.kind, PID: os.Getpid(), Version: version.VersionOrHash, Started: h.start.Format(time.RFC3339)}
	if exe, err := os.Executable(); err == nil {
		info.Process = filepath.Base(exe)
	}
	h.mu.Lock()
	info.Socket = h.socket
	h.mu.Unlock()
	_, err := onModel(h, func(r *tracker.Remote) (struct{}, error) {
		info.File, info.Instruments = r.Label()
		return struct{}{}, nil
	})
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(info)
	return string(out), err
}

// onModel runs f on the goroutine that owns the model, and waits for it.
func onModel[T any](h *Host, f func(r *tracker.Remote) (T, error)) (T, error) {
	type result struct {
		value T
		err   error
	}
	done := make(chan result, 1)
	msg := tracker.MsgToModel{Data: func() {
		var res result
		defer func() {
			if r := recover(); r != nil {
				res.err = fmt.Errorf("the tracker failed: %v", r)
			}
			done <- res
		}()
		res.value, res.err = f(h.model.Remote())
	}}
	var zero T
	if !tracker.TrySend(h.broker.ToModel, msg) {
		return zero, errors.New("the tracker is busy: try again")
	}
	select {
	case res := <-done:
		return res.value, res.err
	case <-time.After(modelTimeout):
		return zero, errors.New("the tracker did not answer in time; if this was a change, read the song again to see whether it was made")
	}
}
