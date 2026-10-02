package main

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/tracker"
	sointumcp "github.com/vsariola/sointu/tracker/mcp"
	"github.com/vsariola/sointu/vm"
)

// runTracker runs a model as a tracker would, with a Host listening.
func runTracker(t *testing.T) {
	t.Helper()
	broker := tracker.NewBroker()
	model := tracker.NewModel(broker, []sointu.Synther{vm.GoSynther{}}, tracker.NullMIDIContext{}, "")
	done, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		for {
			select {
			case msg := <-broker.ToModel:
				model.ProcessMsg(msg)
			case <-broker.ToPlayer:
			case <-done:
				return
			}
		}
	}()
	host := sointumcp.NewHost(model, "sointu-test")
	if err := host.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		host.Close()
		close(done)
		<-finished
		model.Close()
	})
}

func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := NewServer().Connect(ctx, serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func callText(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String(), res.IsError
}

func TestServerEndToEnd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	s := connect(t)
	if !strings.Contains(s.InitializeResult().Instructions, "list_instances") {
		t.Error("the client is not told how to start")
	}
	tools, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]*mcp.Tool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = tool
	}
	for _, want := range []string{"list_instances", "guide", "unit_types", "get_song", "edit_units", "render_note", "play_note", "add_module"} {
		if names[want] == nil {
			t.Errorf("the tool %s is missing", want)
		}
	}
	if schema, _ := names["edit_units"].InputSchema.(map[string]any); schema == nil || !strings.Contains(stringOf(schema), "instance") {
		t.Errorf("edit_units has no argument instance: %v", names["edit_units"].InputSchema)
	}
	// without a tracker, the local tools work and the others tell what to do
	if text, isErr := callText(t, s, "guide", nil); isErr || !strings.Contains(text, "The stack") {
		t.Errorf("guide: %s", text)
	}
	if text, isErr := callText(t, s, "get_song", nil); !isErr || !strings.Contains(text, "Enable MCP") {
		t.Errorf("get_song without a tracker: %s", text)
	}
	runTracker(t)
	if text, _ := callText(t, s, "list_instances", nil); !strings.Contains(text, "sointu-test-") || !strings.Contains(text, "Instr, Global") {
		t.Errorf("list_instances: %s", text)
	}
	text, isErr := callText(t, s, "add_units", map[string]any{"after": 2, "units": []any{map[string]any{"type": "filter", "params": map[string]any{"frequency": 30}}}})
	if isErr || !strings.Contains(text, "filter [2>2] stereo=0 frequency=30") {
		t.Fatalf("add_units: %s", text)
	}
	text, isErr = callText(t, s, "render_note", map[string]any{"instrument": "0", "notes": []int{48}, "hold_ms": 200, "tail_ms": 100, "edits": []any{map[string]any{"unit": 2, "params": map[string]any{"type": "trisaw"}}}})
	if isErr || !strings.Contains(text, "with 1 edits made to the copy only") || !strings.Contains(text, "octave bands") {
		t.Fatalf("render_note: %s", text)
	}
	if text, isErr = callText(t, s, "edit_units", map[string]any{"edits": []any{map[string]any{"unit": 2, "params": map[string]any{"gain": 300}}}}); !isErr || !strings.Contains(text, "0 to 128") {
		t.Errorf("a value out of range was not refused: %s", text)
	}
	if text, isErr = callText(t, s, "get_song", map[string]any{"instance": "nope"}); !isErr || !strings.Contains(text, "no instance") {
		t.Errorf("an instance that is not there was used: %s", text)
	}
	runTracker(t) // a second one: now the instance has to be given
	if text, isErr = callText(t, s, "get_song", nil); !isErr || !strings.Contains(text, "several") {
		t.Errorf("with two trackers, no instance was asked for: %s", text)
	}
}

func stringOf(v any) string {
	var b strings.Builder
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, x := range v {
				b.WriteString(k + " ")
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(v)
	return b.String()
}
