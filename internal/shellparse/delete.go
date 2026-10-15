package shellparse

import "mvdan.cc/sh/v3/syntax"

// DeleteCall is one command invocation extracted for recursive-delete review.
// Name is the static command word (empty when the word is dynamic); Args are
// the static argument words (a non-static argument contributes "", so target
// positions stay visible). Unsafe marks a call the sequence rules cannot
// certify: nested control flow, background/negated placement, an assignment
// prefix, or any statement type the reviewer does not understand.
type DeleteCall struct {
	Name   string   `json:"name"`
	Args   []string `json:"args"`
	Unsafe bool     `json:"unsafe"`
}

// DeleteAnalysis is the static review of one Bash script for delete calls.
// SyntaxError is non-empty when the script could not be parsed; callers must
// treat an unparsable script as unsafe to run (fail-closed).
type DeleteAnalysis struct {
	Standalone  bool         `json:"standalone"`
	Calls       []DeleteCall `json:"calls"`
	SyntaxError string       `json:"syntax_error"`
	HostError   bool         `json:"host_error"`
}

// AnalyzeDeleteCalls walks the Bash AST of command and reports every command
// invocation with the sequence-safety facts the delete guard needs. The walk
// never evaluates anything: only the parser and literal-word resolver run.
//
// Sequence safety: a top-level call in a `;`/`&&`/`||` sequence with no
// assignment prefix is standalone-safe. Calls anywhere else (inside if/for/
// while/function bodies, subshells, pipelines, command substitutions, after
// `!` or `&`) are Unsafe, because a recursive delete there cannot prove which
// directory or which variable value it acts on. Statements whose type the
// reviewer does not recognize contribute an Unsafe marker call so a later
// recursive delete in the same script cannot claim a clean sequence.
func AnalyzeDeleteCalls(command string) (DeleteAnalysis, error) {
	file, err := ParseBash(command)
	if err != nil {
		return DeleteAnalysis{}, err
	}
	out := DeleteAnalysis{Standalone: true}
	safe := make(map[*syntax.CallExpr]bool)
	var sequence func(*syntax.Stmt)
	sequence = func(stmt *syntax.Stmt) {
		if stmt.Background || stmt.Negated {
			return
		}
		switch expr := stmt.Cmd.(type) {
		case *syntax.CallExpr:
			safe[expr] = len(expr.Assigns) == 0
		case *syntax.BinaryCmd:
			if expr.Op == syntax.AndStmt || expr.Op == syntax.OrStmt {
				sequence(expr.X)
				sequence(expr.Y)
			}
		}
	}
	for _, stmt := range file.Stmts {
		sequence(stmt)
	}
	syntax.Walk(file, func(node syntax.Node) bool {
		if stmt, ok := node.(*syntax.Stmt); ok {
			switch stmt.Cmd.(type) {
			case nil, *syntax.CallExpr, *syntax.BinaryCmd:
			default:
				out.Calls = append(out.Calls, DeleteCall{Unsafe: true})
			}
		}
		call, ok := node.(*syntax.CallExpr)
		if !ok {
			return true
		}
		if len(call.Args) == 0 {
			out.Calls = append(out.Calls, DeleteCall{Unsafe: true})
			return true
		}
		name, _ := StaticWord(call.Args[0])
		parsed := DeleteCall{Name: name, Unsafe: !safe[call]}
		for _, arg := range call.Args[1:] {
			value, _ := StaticWord(arg)
			parsed.Args = append(parsed.Args, value)
		}
		out.Calls = append(out.Calls, parsed)
		return true
	})
	return out, nil
}
