package controller

import (
	"encoding/json"
	"fmt"
	"text/template"
	"text/template/parse"
)

type detectedVar struct {
	Key   string `json:"key"`
	Scope string `json:"scope"` // "node" (v) | "config" (c)
}

// detectTemplateVars parses a Go text/template and extracts variables used via:
// - {{v "name"}}  -> scope "node"
// - {{c "name"}}  -> scope "config"
//
// It intentionally ignores ${ENV} so bash can expand it at runtime.
func detectTemplateVars(content string) (string, error) {
	// Provide dummy funcs so Parse succeeds even if the template uses them.
	t, err := template.New("tpl").Funcs(template.FuncMap{
		"v": func(string) string { return "" },
		"c": func(string) string { return "" },
	}).Parse(content)
	if err != nil {
		return "", fmt.Errorf("invalid template: %w", err)
	}
	if t.Tree == nil || t.Tree.Root == nil {
		b, _ := json.Marshal([]detectedVar{})
		return string(b), nil
	}

	var out []detectedVar
	seen := map[string]bool{}

	var walk func(node parse.Node)
	walk = func(node parse.Node) {
		if node == nil {
			return
		}
		switch n := node.(type) {
		case *parse.ListNode:
			if n == nil {
				return
			}
			for _, nn := range n.Nodes {
				walk(nn)
			}
		case *parse.ActionNode:
			if n.Pipe != nil {
				walk(n.Pipe)
			}
		case *parse.PipeNode:
			for _, cmd := range n.Cmds {
				walk(cmd)
			}
		case *parse.CommandNode:
			if len(n.Args) < 2 {
				return
			}
			id, ok := n.Args[0].(*parse.IdentifierNode)
			if !ok || id == nil {
				return
			}
			fn := id.Ident
			if fn != "v" && fn != "c" {
				return
			}
			arg, ok := n.Args[1].(*parse.StringNode)
			if !ok || arg == nil {
				return
			}
			key := arg.Text
			if key == "" {
				return
			}
			scope := "node"
			if fn == "c" {
				scope = "config"
			}
			dk := scope + ":" + key
			if seen[dk] {
				return
			}
			seen[dk] = true
			out = append(out, detectedVar{Key: key, Scope: scope})
		case *parse.TemplateNode:
			// Ignore nested template calls for now (can be expanded later).
			return
		default:
			// ignore
			return
		}
	}

	walk(t.Tree.Root)

	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

