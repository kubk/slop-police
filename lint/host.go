package lint

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/microsoft/typescript-go/internal/api"
	"github.com/microsoft/typescript-go/internal/api/encoder"
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/json"
	"github.com/microsoft/typescript-go/internal/tspath"
)

// Serve runs the linter as a TypeScript API server on in and out for the npm
// launcher, which runs plugin rules in Node. Plugins read ASTs and query
// checkers with the API methods, which share the linter's projects, and the
// launcher drives the run with two more:
//
//   - slopPolice/start loads the config given the plugin rules, opens the
//     projects, starts the built-in rules in the background and returns the
//     analyzed files and the enabled plugin rules.
//   - slopPolice/finish takes the plugin rules' findings, waits for the
//     built-in rules and returns the report.
func Serve(root string, in io.ReadCloser, out io.WriteCloser, timing *Timing) error {
	h := &host{Session: api.NewSession(newProjectSession(root), &api.SessionOptions{UseBinaryResponses: true}), timing: timing}
	defer h.Close()
	transport := api.NewStdioTransport(in, out)
	defer transport.Close()
	rwc, err := transport.Accept()
	if err != nil {
		return err
	}
	return api.NewSyncConn(rwc, api.NewMessagePackProtocol(rwc), h).Run(context.Background())
}

type host struct {
	*api.Session
	timing *Timing

	rules   []EnabledRule
	plugins map[string]int // indexes of the enabled plugin rules by name
	ctx     *Context
	perRule [][]Finding
	done    chan struct{} // closed once the built-in rules are done
	started time.Time     // when the launcher started running plugin rules
}

func (h *host) HandleRequest(ctx context.Context, method string, params json.Value) (any, error) {
	switch method {
	case "slopPolice/start":
		return handle(params, h.start)
	case "slopPolice/finish":
		return handle(params, h.finish)
	}
	return h.Session.HandleRequest(ctx, method, params)
}

func handle[P, R any](params json.Value, fn func(*P) (R, error)) (any, error) {
	p, err := api.UnmarshalParams[P](params)
	if err != nil {
		return nil, err
	}
	if p == nil {
		p = new(P)
	}
	return fn(p)
}

type startParams struct {
	Rules []struct {
		Name    string  `json:"name"`
		Options Options `json:"options"` // defaults
	} `json:"rules"`
}

// startResult has either Error (with its ExitCode) or the run's files and
// enabled plugin rules.
type startResult struct {
	Error    string      `json:"error"`
	ExitCode int         `json:"exitCode"`
	Files    []startFile `json:"files"`
	Rules    []startRule `json:"rules"`
}

type startFile struct {
	FileName string `json:"fileName"`
	Path     string `json:"path"` // relative to the root
	IsTest   bool   `json:"isTest"`
	Project  string `json:"project"` // the tsconfig the file is read from
}

type startRule struct {
	Name    string  `json:"name"`
	Options Options `json:"options"`
}

func (h *host) start(params *startParams) (*startResult, error) {
	if h.done != nil {
		return nil, errors.New("slopPolice/start was already called")
	}
	session := h.ProjectSession()
	// Not nil even without rules: plugins are loaded.
	pluginRules := make([]*Rule, 0, len(params.Rules))
	for _, r := range params.Rules {
		pluginRules = append(pluginRules, &Rule{Name: r.Name, DefaultOptions: r.Options})
	}
	config, err := LoadConfig(session.GetCurrentDirectory(), pluginRules)
	var ctx *Context
	if err == nil {
		ctx, err = newContext(session, config, h.timing)
	}
	if err != nil {
		return &startResult{Error: err.Error(), ExitCode: errorExitCode(err)}, nil
	}

	h.rules, h.ctx, h.done, h.started = config.Rules, ctx, make(chan struct{}), time.Now()
	go func() {
		defer close(h.done)
		h.perRule = runRules(ctx, config.Rules)
	}()

	result := &startResult{}
	h.plugins = map[string]int{}
	for i, enabled := range config.Rules {
		if slices.Contains(pluginRules, enabled.Rule) {
			h.plugins[enabled.Rule.Name] = i
			result.Rules = append(result.Rules, startRule{enabled.Rule.Name, enabled.Options})
		}
	}
	for _, f := range ctx.Files {
		result.Files = append(result.Files, startFile{f.Source.FileName(), f.RelPath, f.IsTest, f.Project.ConfigPath})
	}
	return result, nil
}

type finishParams struct {
	Findings []struct {
		Rule    string `json:"rule"`
		Node    string `json:"node"` // an API node handle
		Message string `json:"message"`
	} `json:"findings"`
}

type finishResult struct {
	Results  []Result `json:"results"`
	Output   string   `json:"output"`
	ExitCode int      `json:"exitCode"`
}

func (h *host) finish(params *finishParams) (*finishResult, error) {
	if h.done == nil {
		return nil, errors.New("slopPolice/finish was called before slopPolice/start")
	}
	h.timing.Span("plugin rules", time.Since(h.started))
	<-h.done
	for _, f := range params.Findings {
		i, ok := h.plugins[f.Rule]
		if !ok {
			return nil, fmt.Errorf("finding for a rule that isn't enabled: %s", f.Rule)
		}
		file, node, err := h.ctx.nodeByHandle(f.Node)
		if err != nil {
			return nil, err
		}
		// Findings outside the analyzed files (in ignored files or
		// libraries) are dropped.
		if file != nil {
			h.perRule[i] = append(h.perRule[i], Finding{File: file, Node: node, Message: f.Message})
		}
	}
	results := collectResults(h.rules, h.perRule)
	return &finishResult{Results: results, Output: Format(results), ExitCode: exitCode(results)}, nil
}

// nodeByHandle resolves an API node handle ("index.kind.path") to a node of
// an analyzed file, or returns a nil file for a node of another file.
func (c *Context) nodeByHandle(handle string) (*File, *ast.Node, error) {
	index, rest, _ := strings.Cut(handle, ".")
	kind, path, ok := strings.Cut(rest, ".")
	file := c.filesByPath[tspath.Path(path)]
	if !ok || file == nil {
		return nil, nil, nil
	}
	i, err := strconv.Atoi(index)
	nodes := encoder.GetNodeIndexTable(file.Source).Nodes
	// The kind catches a client that numbers nodes differently.
	if err != nil || i < 0 || i >= len(nodes) || nodes[i] == nil || strconv.Itoa(int(nodes[i].Kind)) != kind {
		return nil, nil, fmt.Errorf("node handle %s doesn't match %s", handle, file.RelPath)
	}
	return file, nodes[i], nil
}
