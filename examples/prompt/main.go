// Command prompt runs an ACP agent, sends it a prompt and prints what it
// streams back: the agent's message, its thinking (with -v), tool calls,
// plans, and how the turn ended.
//
//	go run ./examples/prompt -p "list the files here" -- claude-agent-acp
//	go run ./examples/prompt -list -- codex-acp
//	go run ./examples/prompt -load <session id> -p "and now?" -- claude-agent-acp
//
// Permission requests are refused unless -yes is given (then the first
// option that allows once is picked). Ctrl-C cancels the turn
// (session/cancel); a second one quits.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"

	"github.com/afrase/acp-go"
)

type client struct {
	acp.UnimplementedClient
	verbose, yes bool
}

func (c *client) SessionUpdate(_ context.Context, n *acp.SessionNotification) error {
	u := n.Update
	switch {
	case u.AgentMessageChunk != nil:
		if t := u.AgentMessageChunk.Content.Text; t != nil {
			fmt.Print(t.Text)
		}
	case u.AgentThoughtChunk != nil && c.verbose:
		if t := u.AgentThoughtChunk.Content.Text; t != nil {
			fmt.Fprint(os.Stderr, "\x1b[2m"+t.Text+"\x1b[0m")
		}
	case u.UserMessageChunk != nil && c.verbose: // a loaded session's history
		if t := u.UserMessageChunk.Content.Text; t != nil {
			fmt.Printf("\n> %s\n", t.Text)
		}
	case u.ToolCall != nil:
		fmt.Fprintf(os.Stderr, "\n[%s] %s (%s)\n", u.ToolCall.Kind, u.ToolCall.Title, u.ToolCall.Status)
	case u.ToolCallUpdate != nil && u.ToolCallUpdate.Status != "":
		fmt.Fprintf(os.Stderr, "[%s: %s]\n", u.ToolCallUpdate.ToolCallID, u.ToolCallUpdate.Status)
	case u.Plan != nil:
		for _, e := range u.Plan.Entries {
			fmt.Fprintf(os.Stderr, "  plan: [%s] %s\n", e.Status, e.Content)
		}
	case c.verbose:
		fmt.Fprintf(os.Stderr, "[%s]\n", u.SessionUpdate)
	}
	return nil
}

func (c *client) RequestPermission(_ context.Context, req *acp.RequestPermissionRequest) (*acp.RequestPermissionResponse, error) {
	title := req.ToolCall.Title
	for _, o := range req.Options {
		if c.yes && o.Kind == acp.PermissionOptionKindAllowOnce {
			fmt.Fprintf(os.Stderr, "[allowed: %s]\n", title)
			return &acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: &acp.SelectedPermissionOutcome{OptionID: o.OptionID}}}, nil
		}
	}
	for _, o := range req.Options {
		if o.Kind == acp.PermissionOptionKindRejectOnce {
			fmt.Fprintf(os.Stderr, "[refused: %s]\n", title)
			return &acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: &acp.SelectedPermissionOutcome{OptionID: o.OptionID}}}, nil
		}
	}
	return &acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: true}}, nil
}

func main() {
	prompt := flag.String("p", "", "the prompt")
	cwd := flag.String("cwd", "", "the session's folder (default: this one)")
	load := flag.String("load", "", "a session to load (its history is printed with -v) before the prompt")
	list := flag.Bool("list", false, "list the agent's sessions")
	verbose := flag.Bool("v", false, "print thinking and every update")
	yes := flag.Bool("yes", false, "allow what the agent asks to do")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: prompt [flags] -- agent [args]")
		flag.PrintDefaults()
		os.Exit(2)
	}
	if err := run(context.Background(), flag.Args(), *prompt, *cwd, *load, *list, &client{verbose: *verbose, yes: *yes}); err != nil {
		fmt.Fprintln(os.Stderr, "prompt:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, command []string, prompt, cwd, load string, list bool, c *client) error {
	if cwd == "" {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			return err
		}
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...) //nolint:gosec // the agent the user names
	cmd.Stderr = os.Stderr
	agent, err := acp.Start(cmd, c)
	if err != nil {
		return err
	}
	defer func() {
		_ = agent.Shutdown(ctx)
	}()
	init, err := agent.Initialize(ctx, &acp.InitializeRequest{
		ProtocolVersion: acp.LatestProtocolVersion,
		ClientInfo:      &acp.Implementation{Name: "acp-go-prompt", Version: "0.1.0"},
	})
	if err != nil {
		return err
	}
	if init.AgentInfo != nil {
		fmt.Fprintf(os.Stderr, "%s %s, protocol %d\n", init.AgentInfo.Name, init.AgentInfo.Version, init.ProtocolVersion)
	}
	if list {
		return listSessions(ctx, agent, cwd)
	}
	var sid acp.SessionID
	if load != "" {
		sid = acp.SessionID(load)
		if _, err := agent.LoadSession(ctx, &acp.LoadSessionRequest{SessionID: sid, Cwd: cwd}); err != nil {
			return err
		}
	} else {
		s, err := agent.NewSession(ctx, &acp.NewSessionRequest{Cwd: cwd})
		if err != nil {
			return err
		}
		sid = s.SessionID
	}
	fmt.Fprintln(os.Stderr, "session", sid)
	if prompt == "" {
		return nil
	}
	return turn(ctx, agent, sid, prompt)
}

func turn(ctx context.Context, agent *acp.AgentConn, sid acp.SessionID, prompt string) error {
	interrupt := make(chan os.Signal, 2)
	signal.Notify(interrupt, os.Interrupt)
	defer signal.Stop(interrupt)
	wait, err := agent.StartPrompt(ctx, &acp.PromptRequest{SessionID: sid, Prompt: []acp.ContentBlock{{Text: &acp.TextContent{Text: prompt}}}})
	if err != nil {
		return err
	}
	go func() {
		<-interrupt
		fmt.Fprintln(os.Stderr, "\n[cancelling]")
		_ = agent.Cancel(ctx, &acp.CancelNotification{SessionID: sid})
		<-interrupt
		_ = agent.Close()
	}()
	res, err := wait(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "\n[%s]\n", res.StopReason)
	return nil
}

func listSessions(ctx context.Context, agent *acp.AgentConn, cwd string) error {
	req := &acp.ListSessionsRequest{Cwd: cwd}
	for {
		page, err := agent.ListSessions(ctx, req)
		var e *acp.Error
		if errors.As(err, &e) && e.Code == acp.ErrorCodeMethodNotFound {
			return errors.New("the agent can't list its sessions")
		}
		if err != nil {
			return err
		}
		for _, s := range page.Sessions {
			fmt.Printf("%s  %s  %s\n", s.SessionID, s.UpdatedAt, strings.TrimSpace(s.Title))
		}
		if page.NextCursor == "" {
			return nil
		}
		req.Cursor = page.NextCursor
	}
}
