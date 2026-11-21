// Command echo is an ACP agent that answers a prompt with its own text,
// streamed a word at a time: a small agent to try a client against.
//
//	go build -o echo ./examples/echo
//
// Any ACP client can run it, Zed for one ("agent_servers": {"Echo":
// {"command": "/path/to/echo"}}). A prompt with "think" in it thinks
// first; one with "plan" sends a plan; one with "slow" takes a second a
// word, so Stop (session/cancel) can be tried.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/afrase/acp-go"
)

type echo struct {
	acp.UnimplementedAgent
	client *acp.ClientConn

	mu        sync.Mutex
	turns     map[acp.SessionID]*turn  // each session's running turn
	cancelled map[acp.SessionID]uint64 // the sequence of each session's last Cancel
}

type turn struct{ cancel context.CancelFunc }

func (e *echo) Initialize(context.Context, *acp.InitializeRequest) (*acp.InitializeResponse, error) {
	return &acp.InitializeResponse{
		ProtocolVersion: acp.LatestProtocolVersion,
		AgentInfo:       &acp.Implementation{Name: "echo", Title: "Echo", Version: "0.1.0"},
	}, nil
}

func (e *echo) NewSession(context.Context, *acp.NewSessionRequest) (*acp.NewSessionResponse, error) {
	return &acp.NewSessionResponse{SessionID: acp.SessionID(rand.Text())}, nil
}

func (e *echo) Authenticate(context.Context, *acp.AuthenticateRequest) (*acp.AuthenticateResponse, error) {
	return &acp.AuthenticateResponse{}, nil
}

func (e *echo) Prompt(ctx context.Context, req *acp.PromptRequest) (*acp.PromptResponse, error) {
	// A Cancel that came after this prompt may run before it, or while it
	// sets up: the sequences say which came first.
	seq, _ := acp.SequenceFromContext(ctx)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	t := &turn{cancel: cancel}
	e.mu.Lock()
	if e.cancelled[req.SessionID] > seq {
		e.mu.Unlock()
		return &acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	}
	e.turns[req.SessionID] = t
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.turns[req.SessionID] == t {
			delete(e.turns, req.SessionID)
		}
	}()
	var text []string
	for _, b := range req.Prompt {
		if b.Text != nil {
			text = append(text, b.Text.Text)
		}
	}
	all := strings.Join(text, " ")
	send := func(u acp.SessionUpdate) error {
		return e.client.SessionUpdate(ctx, &acp.SessionNotification{SessionID: req.SessionID, Update: u})
	}
	chunk := func(s string) *acp.ContentChunk {
		return &acp.ContentChunk{Content: acp.ContentBlock{Text: &acp.TextContent{Text: s}}}
	}
	if strings.Contains(all, "think") {
		if err := send(acp.SessionUpdate{AgentThoughtChunk: chunk("Echoing it back seems best.")}); err != nil {
			return nil, err
		}
	}
	if strings.Contains(all, "plan") {
		if err := send(acp.SessionUpdate{Plan: &acp.Plan{Entries: []acp.PlanEntry{
			{Content: "Read the prompt", Priority: acp.PlanEntryPriorityHigh, Status: acp.PlanEntryStatusCompleted},
			{Content: "Say it back", Priority: acp.PlanEntryPriorityHigh, Status: acp.PlanEntryStatusInProgress},
		}}}); err != nil {
			return nil, err
		}
	}
	for word := range strings.FieldsSeq(all) {
		if strings.Contains(all, "slow") {
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
			}
		}
		if ctx.Err() != nil {
			return &acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
		}
		if err := send(acp.SessionUpdate{AgentMessageChunk: chunk(word + " ")}); err != nil {
			return nil, err
		}
	}
	return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (e *echo) Cancel(ctx context.Context, n *acp.CancelNotification) error {
	seq, _ := acp.SequenceFromContext(ctx)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cancelled[n.SessionID] = seq
	if t := e.turns[n.SessionID]; t != nil {
		t.cancel()
	}
	return nil
}

func main() {
	conn := acp.NewClientConn(os.Stdin, os.Stdout, func(c *acp.ClientConn) acp.Agent {
		return &echo{client: c, turns: map[acp.SessionID]*turn{}, cancelled: map[acp.SessionID]uint64{}}
	})
	<-conn.Done()
	if err := conn.Err(); !errors.Is(err, io.EOF) { // the client closed our stdin: done
		fmt.Fprintln(os.Stderr, "echo:", err)
		os.Exit(1)
	}
}
