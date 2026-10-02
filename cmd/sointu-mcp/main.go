// Command sointu-mcp is an MCP server, for clients like Claude Code, that
// reads and changes the patch of the sointu trackers and plugins that are
// running. A client starts it and talks to it over standard input and
// output; it passes the tool calls on to a tracker or a plugin instance,
// over the unix socket that the instance listens on once the user has
// turned it on (Edit > Enable MCP). See tracker/mcp.
//
//	claude mcp add sointu -- /path/to/sointu-mcp
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	sointumcp "github.com/vsariola/sointu/tracker/mcp"
	"github.com/vsariola/sointu/version"
)

func main() {
	list := flag.Bool("list", false, "list the trackers and plugins that are listening, and exit")
	flag.Parse()
	if *list {
		instances, err := sointumcp.Instances()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(sointumcp.Describe(instances))
		return
	}
	server := NewServer()
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}

type listArgs struct{}

// NewServer returns the MCP server with the tools of tracker/mcp, each with
// the argument instance added, and list_instances.
func NewServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "sointu", Title: "sointu tracker", Version: version.VersionOrHash},
		&mcp.ServerOptions{Instructions: sointumcp.Instructions})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_instances",
		Description: "The sointu trackers and plugin instances that are listening: their id, the program they run in, their file and their instruments. With several, pass one's id as instance to the other tools.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ listArgs) (*mcp.CallToolResult, any, error) {
		instances, err := sointumcp.Instances()
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(sointumcp.Describe(instances)), nil, nil
	})
	for _, tool := range sointumcp.Tools() {
		schema, err := jsonschema.ForType(tool.Args, &jsonschema.ForOptions{})
		if err != nil {
			panic(fmt.Sprintf("the arguments of %s: %v", tool.Name, err))
		}
		if !tool.Local {
			if schema.Properties == nil {
				schema.Properties = map[string]*jsonschema.Schema{}
			}
			schema.Properties["instance"] = &jsonschema.Schema{Type: "string",
				Description: "the id of the tracker or plugin instance, from list_instances; needed only when several are listening"}
		}
		destructive := !tool.ReadOnly
		server.AddTool(&mcp.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: schema,
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: tool.ReadOnly, DestructiveHint: &destructive},
		}, handler(tool))
	}
	return server
}

func handler(tool sointumcp.Tool) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		if tool.Local {
			text, err := tool.Run(args)
			if err != nil {
				return errorResult(err), nil
			}
			return textResult(text), nil
		}
		// the argument instance is ours: the others go on to the tracker
		var fields map[string]json.RawMessage
		if len(args) > 0 {
			if err := json.Unmarshal(args, &fields); err != nil {
				return errorResult(fmt.Errorf("the arguments are not an object: %w", err)), nil
			}
		}
		var id string
		if raw, ok := fields["instance"]; ok {
			if err := json.Unmarshal(raw, &id); err != nil {
				return errorResult(fmt.Errorf("instance is a string: %w", err)), nil
			}
			delete(fields, "instance")
		}
		rest, _ := json.Marshal(fields)
		instances, err := sointumcp.Instances()
		if err != nil {
			return errorResult(err), nil
		}
		info, err := sointumcp.Choose(instances, id)
		if err != nil {
			return errorResult(err), nil
		}
		text, err := sointumcp.Call(info.Socket, tool.Name, rest)
		if err != nil {
			return errorResult(err), nil
		}
		return textResult(text), nil
	}
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func errorResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}, IsError: true}
}

func init() {
	log.SetFlags(0)
	log.SetOutput(os.Stderr)
}
