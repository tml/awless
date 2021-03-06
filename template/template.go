/*
Copyright 2017 WALLIX

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package template

import (
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid"
	"github.com/wallix/awless/template/ast"
	"github.com/wallix/awless/template/driver"
)

type Template struct {
	*ast.AST
}

func (s *Template) Run(d driver.Driver) (*Template, error) {
	vars := map[string]interface{}{}

	current := &Template{AST: s.Clone()}

	for _, sts := range current.Statements {
		switch sts.Node.(type) {
		case *ast.ExpressionNode:
			expr := sts.Node.(*ast.ExpressionNode)
			fn := d.Lookup(expr.Action, expr.Entity)
			expr.ProcessRefs(vars)

			sts.Line = expr.String()
			if sts.Result, sts.Err = fn(expr.Params); sts.Err != nil {
				return current, sts.Err
			}
		case *ast.DeclarationNode:
			ident := sts.Node.(*ast.DeclarationNode).Left
			expr := sts.Node.(*ast.DeclarationNode).Right
			fn := d.Lookup(expr.Action, expr.Entity)
			expr.ProcessRefs(vars)

			sts.Result, sts.Err = fn(expr.Params)
			ident.Val = sts.Result
			sts.Line = expr.String()
			if sts.Err != nil {
				return current, sts.Err
			}
			vars[ident.Ident] = ident.Val
		}
	}

	return current, nil
}

func (s *Template) Compile(d driver.Driver) (*Template, error) {
	defer d.SetDryRun(false)
	d.SetDryRun(true)

	return s.Run(d)
}

func (t *Template) Visit(v Visitor) error {
	return v.Visit(t.Statements)
}

func (s *Template) GetHolesValuesSet() (values []string) {
	holes := make(map[string]bool)
	each := func(expr *ast.ExpressionNode) {
		for _, hole := range expr.Holes {
			holes[hole] = true
		}
	}
	s.visitExpressionNodes(each)

	for k, _ := range holes {
		values = append(values, k)
	}

	return
}

func (s *Template) GetNormalizedAliases() map[string]string {
	aliases := make(map[string]string)
	each := func(expr *ast.ExpressionNode) {
		for k, v := range expr.Aliases {
			if !strings.Contains(k, ".") {
				aliases[fmt.Sprintf("%s.%s", expr.Entity, k)] = v
			} else {
				aliases[k] = v
			}
		}
	}
	s.visitExpressionNodes(each)
	return aliases
}

func (s *Template) GetNormalizedParams() map[string]interface{} {
	params := make(map[string]interface{})
	each := func(expr *ast.ExpressionNode) {
		for k, v := range expr.Params {
			if !strings.Contains(k, ".") {
				params[fmt.Sprintf("%s.%s", expr.Entity, k)] = v
			} else {
				params[k] = v
			}
		}
	}
	s.visitExpressionNodes(each)
	return params
}

func (s *Template) MergeParams(newParams map[string]interface{}) {
	each := func(expr *ast.ExpressionNode) {
		for k, v := range newParams {
			if strings.SplitN(k, ".", 2)[0] == expr.Entity {
				if expr.Params == nil {
					expr.Params = make(map[string]interface{})
				}
				expr.Params[strings.SplitN(k, ".", 2)[1]] = v
			}
		}
	}
	s.visitExpressionNodes(each)
}

func (s *Template) ResolveHoles(refs ...map[string]interface{}) (map[string]interface{}, error) {
	all := make(map[string]interface{})
	for _, ref := range refs {
		for k, v := range ref {
			all[k] = v
		}
	}

	resolved := make(map[string]interface{})
	each := func(expr *ast.ExpressionNode) {
		processed := expr.ProcessHoles(all)
		for key, v := range processed {
			resolved[expr.Entity+"."+key] = v
		}
	}

	s.visitExpressionNodes(each)

	return resolved, nil
}

func (s *Template) visitExpressionNodes(fn func(n *ast.ExpressionNode)) {
	for _, sts := range s.Statements {
		var expr *ast.ExpressionNode

		switch sts.Node.(type) {
		case *ast.ExpressionNode:
			expr = sts.Node.(*ast.ExpressionNode)
		case *ast.DeclarationNode:
			expr = sts.Node.(*ast.DeclarationNode).Right
		}

		if expr != nil {
			fn(expr)
		}
	}
}

type TemplateExecution struct {
	ID       string
	Executed []*ExecutedStatement
}

type ExecutedStatement struct {
	Line, Err, Result string
}

func (ex *ExecutedStatement) IsRevertible() bool {
	if ex.Err != "" {
		return false
	}
	if ex.Result != "" {
		if strings.Contains(ex.Line, "create") || strings.Contains(ex.Line, "start") || strings.Contains(ex.Line, "stop") {
			return true
		}
	} else {
		return strings.Contains(ex.Line, "attach") || strings.Contains(ex.Line, "detach")
	}

	return false
}

func NewTemplateExecution(tpl *Template) *TemplateExecution {
	out := &TemplateExecution{
		ID: ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String(),
	}

	for _, sts := range tpl.Statements {
		var errMsg string
		if sts.Err != nil {
			errMsg = sts.Err.Error()
		}
		var result string
		switch sts.Result.(type) {
		case string:
			result = sts.Result.(string)
		}
		out.Executed = append(out.Executed,
			&ExecutedStatement{Line: sts.Line, Result: result, Err: errMsg},
		)
	}

	return out
}

func (te *TemplateExecution) HasErrors() (inError bool) {
	for _, ex := range te.Executed {
		if ex.Err != "" {
			inError = true
		}
	}
	return
}

func (te *TemplateExecution) IsRevertible() bool {
	for _, ex := range te.Executed {
		if ex.IsRevertible() {
			return true
		}
	}
	return false
}

func (te *TemplateExecution) lines() (lines []string) {
	for _, ex := range te.Executed {
		lines = append(lines, ex.Line)
	}

	return
}

func (te *TemplateExecution) Revert() (*Template, error) {
	var lines []string

	for i := len(te.Executed) - 1; i >= 0; i-- {
		if exec := te.Executed[i]; exec.IsRevertible() {
			n, err := ParseStatement(exec.Line)
			if err != nil {
				return nil, err
			}

			switch n.(type) {
			case *ast.ExpressionNode:
				node := n.(*ast.ExpressionNode)
				var revertAction string
				var params []string
				switch node.Action {
				case "create":
					revertAction = "delete"
				case "start":
					revertAction = "stop"
				case "stop":
					revertAction = "start"
				case "detach":
					revertAction = "attach"
				case "attach":
					revertAction = "detach"
				}

				switch node.Action {
				case "start", "stop", "attach", "detach":
					for k, v := range node.Params {
						params = append(params, fmt.Sprintf("%s=%s", k, v))
					}
				case "create":
					params = append(params, fmt.Sprintf("id=%s", exec.Result))
				}

				lines = append(lines, fmt.Sprintf("%s %s %s\n", revertAction, node.Entity, strings.Join(params, " ")))
			default:
				return nil, fmt.Errorf("cannot parse [%s] as expression node", exec.Line)
			}
		}
	}

	if len(lines) == 0 {
		return nil, fmt.Errorf("revert: found nothing to revert from:\n%s\n(note: no revert provided for statement in error)", strings.Join(te.lines(), "\n"))
	}

	tpl, err := Parse(strings.Join(lines, "\n"))
	if err != nil {
		return nil, fmt.Errorf("revert: \n%s\n%s", strings.Join(lines, "\n"), err)
	}

	return tpl, nil
}
