package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// The side of the sointu-mcp command: finding the Hosts that are running,
// and calling their tools.

// callTimeout is how long a tool call may take: a render of 30 s of a heavy
// patch is the longest.
const callTimeout = 5 * time.Minute

// Call calls a tool of the Host listening on the socket.
func Call(socket, tool string, args json.RawMessage) (string, error) {
	return call(socket, tool, args, callTimeout)
}

func call(socket, tool string, args json.RawMessage, timeout time.Duration) (string, error) {
	conn, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err != nil {
		return "", fmt.Errorf("the tracker cannot be reached: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))
	if err := json.NewEncoder(conn).Encode(request{Tool: tool, Args: args}); err != nil {
		return "", fmt.Errorf("the tracker cannot be reached: %w", err)
	}
	line, err := bufio.NewReaderSize(conn, 1<<16).ReadBytes('\n')
	if err != nil {
		return "", fmt.Errorf("the tracker did not answer: %w", err)
	}
	var resp response
	if err := json.Unmarshal(line, &resp); err != nil {
		return "", fmt.Errorf("the tracker's answer is malformed: %w", err)
	}
	if resp.Error != "" {
		return "", errors.New(resp.Error)
	}
	return resp.Text, nil
}

// Instances returns the Hosts that are running, the oldest first. The files
// of Hosts that are gone, e.g. of a plugin host that crashed, are removed.
func Instances() ([]Info, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var ret []Info
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var info Info
		if json.Unmarshal(data, &info) != nil || info.Socket == "" {
			continue
		}
		text, err := call(info.Socket, infoTool, nil, modelTimeout+time.Second)
		if err != nil {
			if !processRuns(info.PID) {
				os.Remove(file)
				os.Remove(info.Socket)
				continue
			}
			// running, but not answering: e.g. its window is busy
			ret = append(ret, info)
			continue
		}
		socket := info.Socket
		if json.Unmarshal([]byte(text), &info) != nil {
			continue
		}
		info.Socket = socket
		ret = append(ret, info)
	}
	sort.SliceStable(ret, func(i, j int) bool { return ret[i].Started < ret[j].Started })
	return ret, nil
}

func processRuns(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, os.ErrPermission)
}

// Describe lists instances for a language model to choose from.
func Describe(instances []Info) string {
	if len(instances) == 0 {
		return "No sointu tracker or plugin is listening. The user turns it on in the tracker or the plugin window: Edit > Enable MCP."
	}
	var b strings.Builder
	for _, info := range instances {
		fmt.Fprintf(&b, "%s: %s in %s (pid %d)", info.ID, info.Kind, info.Process, info.PID)
		if info.Started != "" {
			fmt.Fprintf(&b, ", since %s", info.Started)
		}
		if info.File != "" {
			fmt.Fprintf(&b, ", file %s", info.File)
		}
		if len(info.Instruments) > 0 {
			fmt.Fprintf(&b, ", instruments: %s", strings.Join(info.Instruments, ", "))
		}
		if info.Version == "" && info.Started == "" {
			b.WriteString(", not answering")
		}
		b.WriteString("\n")
	}
	if len(instances) > 1 {
		b.WriteString("Pass the id of one as instance to the other tools.")
	} else {
		b.WriteString("It is the only one: the other tools use it without instance.")
	}
	return b.String()
}

// Choose returns the instance that id names: its ID, or a part of it that
// only one has. Without an id, it is the only one running.
func Choose(instances []Info, id string) (Info, error) {
	if len(instances) == 0 {
		return Info{}, errors.New(Describe(nil))
	}
	if id == "" {
		if len(instances) == 1 {
			return instances[0], nil
		}
		return Info{}, fmt.Errorf("several trackers are listening: pass one as instance.\n%s", Describe(instances))
	}
	var found []Info
	for _, info := range instances {
		if info.ID == id {
			return info, nil
		}
		if strings.Contains(info.ID, id) {
			found = append(found, info)
		}
	}
	if len(found) == 1 {
		return found[0], nil
	}
	return Info{}, fmt.Errorf("no instance %q.\n%s", id, Describe(instances))
}
