package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// The pool built by NewServer passes every message through the team task
// gate; without it a cancel kills a task's turn a person's message is in.
func TestPoolOnSendIsTheTeamTaskGate(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "server.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		if k, ok := kv.Key.(*ast.Ident); !ok || k.Name != "OnSend" {
			return true
		}
		if sel, ok := kv.Value.(*ast.SelectorExpr); ok && sel.Sel.Name == "NoteSessionMessage" {
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "agentstool" {
				found = true
			}
		}
		return true
	})
	if !found {
		t.Fatal("server.go: pool config lacks OnSend: agentstool.NoteSessionMessage")
	}
}
