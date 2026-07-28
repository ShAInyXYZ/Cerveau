package codeintel

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

func extractGo(path string, src []byte) ([]Symbol, []Call, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, err
	}
	var symbols []Symbol
	var calls []Call

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			kind := "func"
			if d.Recv != nil && len(d.Recv.List) > 0 {
				kind = "method"
			}
			symbols = append(symbols, Symbol{
				File: path, Name: d.Name.Name, Kind: kind,
				Signature: goSignature(fset, d), Line: fset.Position(d.Pos()).Line,
			})
			if d.Body != nil {
				ast.Inspect(d.Body, func(n ast.Node) bool {
					if call, ok := n.(*ast.CallExpr); ok {
						if name := calleeName(call.Fun); name != "" {
							calls = append(calls, Call{
								CallerFile: path, CallerSymbol: d.Name.Name, CalleeName: name,
								Line: fset.Position(call.Pos()).Line,
							})
						}
					}
					return true
				})
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				if sp, ok := spec.(*ast.TypeSpec); ok {
					kind := "type"
					switch sp.Type.(type) {
					case *ast.InterfaceType:
						kind = "interface"
					case *ast.StructType:
						kind = "struct"
					}
					symbols = append(symbols, Symbol{
						File: path, Name: sp.Name.Name, Kind: kind,
						Signature: d.Tok.String() + " " + sp.Name.Name,
						Line:      fset.Position(sp.Pos()).Line,
					})
				}
			}
		}
	}
	return symbols, calls, nil
}

func goSignature(fset *token.FileSet, fn *ast.FuncDecl) string {
	var sb strings.Builder
	sb.WriteString("func ")
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		sb.WriteString("(")
		sb.WriteString(typeString(fset, fn.Recv.List[0].Type))
		sb.WriteString(") ")
	}
	sb.WriteString(fn.Name.Name)
	sb.WriteString(typeString(fset, fn.Type))
	return sb.String()
}

func typeString(fset *token.FileSet, expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.FuncType:
		var params, results []string
		if t.Params != nil {
			for _, p := range t.Params.List {
				params = append(params, typeString(fset, p.Type))
			}
		}
		if t.Results != nil {
			for _, r := range t.Results.List {
				results = append(results, typeString(fset, r.Type))
			}
		}
		out := "(" + strings.Join(params, ", ") + ")"
		if len(results) == 1 {
			out += " " + results[0]
		} else if len(results) > 1 {
			out += " (" + strings.Join(results, ", ") + ")"
		}
		return out
	case *ast.StarExpr:
		return "*" + typeString(fset, t.X)
	case *ast.SelectorExpr:
		return typeString(fset, t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		return "[]" + typeString(fset, t.Elt)
	case *ast.MapType:
		return "map[" + typeString(fset, t.Key) + "]" + typeString(fset, t.Value)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr, *ast.IndexListExpr:
		return "generic"
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.Ellipsis:
		return "..." + typeString(fset, t.Elt)
	case *ast.ChanType:
		return "chan " + typeString(fset, t.Value)
	default:
		return "?"
	}
}

func calleeName(fun ast.Expr) string {
	switch t := fun.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.IndexExpr:
		return calleeName(t.X)
	case *ast.IndexListExpr:
		return calleeName(t.X)
	}
	return ""
}
