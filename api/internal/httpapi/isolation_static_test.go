//go:build isolation

package httpapi_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestIsolationEveryRouteIsInTheMatrix: a route registered in the router that is neither in
// openapi.yaml (so the matrix never sees it) nor exempt is a hole in the isolation test.
func TestIsolationEveryRouteIsInTheMatrix(t *testing.T) {
	rep := newReport()
	spec := map[string]bool{}
	for _, op := range specOps(t) {
		spec[op.key()] = true
		if _, ok := probes[op.key()]; !ok {
			rep.add("%s: in openapi.yaml but not in the isolation matrix", op.key())
		}
	}
	// The router: every HandleFunc/Handle call with a literal pattern in the non-test sources.
	routerOnly := map[string]string{
		"/mcp": "the MCP endpoint; every tool is covered by TestIsolationMCP",
		"/":    "the authenticated subtree; every operation below it is in openapi.yaml and the matrix",
	}
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	registered := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle") {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			pat, _ := strconv.Unquote(lit.Value)
			registered[pat] = true
			return true
		})
	}
	for pat := range registered {
		if _, ok := routerOnly[pat]; ok || spec[pat] {
			continue
		}
		rep.add("%s: registered in the router but missing from openapi.yaml and the isolation matrix", pat)
	}
	for k := range spec {
		if !registered[k] {
			rep.add("%s: in openapi.yaml but not registered in the router (stale spec or stale matrix)", k)
		}
	}
	var stale []string
	for k := range probes {
		if !spec[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(stale)
	for _, k := range stale {
		rep.add("%s: in the isolation matrix but not in openapi.yaml", k)
	}
	rep.fail(t, "route coverage")
}

// ---------------------------------------------------------------- process-wide state

// stateAllow lists package-level variables that look like shared state but hold no tenant data.
var stateAllow = map[string]string{
	"match.accentStrippers": "sync.Pool of scratch buffers; holds no data between calls",
}

type globalVar struct {
	pkg, name string
	pos       token.Position
	typ       string
}

// TestIsolationNoProcessGlobals scans the production code for state that is shared by every household:
// package-level database handles, package-level variables that are written at run time (caches, the
// taught glossary), package-level locks, and the service graph being built once per process outside
// the tenant builder. Each finding is a "not isolated" line of the refactor checklist.
func TestIsolationNoProcessGlobals(t *testing.T) {
	root := "../.." // api/
	skipDir := map[string]bool{"spike": true, "e2e": true, "drill": true, "cmd/shop-spike": true}
	rep := newReport()
	fset := token.NewFileSet()

	type fileInfo struct {
		path string
		file *ast.File
		pkg  string // directory relative to api/
	}
	var files []fileInfo
	for _, dir := range []string{"internal", "cmd"} {
		_ = filepath.Walk(filepath.Join(root, dir), func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(root, filepath.Dir(p))
			if info.IsDir() {
				if skipDir[filepath.Base(p)] || skipDir[rel+"/"+info.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			af, err := parser.ParseFile(fset, p, nil, 0)
			if err != nil {
				t.Fatalf("%s: %v", p, err)
			}
			files = append(files, fileInfo{path: p, file: af, pkg: rel})
			return nil
		})
	}

	// 1. Package-level variables.
	vars := map[string]globalVar{} // "dir.name"
	for _, f := range files {
		for _, d := range f.file.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, sp := range gd.Specs {
				vs := sp.(*ast.ValueSpec)
				typ := exprString(vs.Type)
				for _, v := range vs.Values {
					typ += " " + exprString(v)
				}
				for _, n := range vs.Names {
					if n.Name == "_" {
						continue
					}
					vars[f.pkg+"."+n.Name] = globalVar{pkg: f.pkg, name: n.Name, pos: fset.Position(n.Pos()), typ: typ}
				}
			}
		}
	}
	// 2. Writes to those variables from functions other than init.
	writes := map[string]string{} // key -> function name
	for _, f := range files {
		for _, d := range f.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil || fd.Name.Name == "init" {
				continue
			}
			local := map[string]bool{} // names declared inside the function shadow the package variable
			for _, fl := range append(append([]*ast.Field{}, fd.Type.Params.List...), resultFields(fd)...) {
				for _, nm := range fl.Names {
					local[nm.Name] = true
				}
			}
			if fd.Recv != nil {
				for _, fl := range fd.Recv.List {
					for _, nm := range fl.Names {
						local[nm.Name] = true
					}
				}
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				switch s := n.(type) {
				case *ast.AssignStmt:
					if s.Tok == token.DEFINE {
						for _, l := range s.Lhs {
							if id, ok := l.(*ast.Ident); ok {
								local[id.Name] = true
							}
						}
					}
				case *ast.ValueSpec:
					for _, nm := range s.Names {
						local[nm.Name] = true
					}
				case *ast.RangeStmt:
					if s.Tok == token.DEFINE {
						for _, l := range []ast.Expr{s.Key, s.Value} {
							if id, ok := l.(*ast.Ident); ok {
								local[id.Name] = true
							}
						}
					}
				}
				return true
			})
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				var lhs []ast.Expr
				switch s := n.(type) {
				case *ast.AssignStmt:
					if s.Tok == token.DEFINE {
						return true
					}
					lhs = s.Lhs
				case *ast.IncDecStmt:
					lhs = []ast.Expr{s.X}
				case *ast.CallExpr:
					if id, ok := s.Fun.(*ast.Ident); ok && (id.Name == "delete" || id.Name == "clear") && len(s.Args) > 0 {
						lhs = []ast.Expr{s.Args[0]}
					}
				}
				for _, e := range lhs {
					if id := rootIdent(e); id != nil && !local[id.Name] {
						key := f.pkg + "." + id.Name
						if _, isVar := vars[key]; isVar {
							writes[key] = fd.Name.Name
						}
					}
				}
				return true
			})
		}
	}
	for key, g := range vars {
		short := filepath.Base(g.pkg) + "." + g.name
		if _, ok := stateAllow[short]; ok {
			continue
		}
		loc := fmt.Sprintf("%s:%d", strings.TrimPrefix(g.pos.Filename, "../../"), g.pos.Line)
		switch {
		case strings.Contains(g.typ, "sql.DB") || strings.Contains(g.typ, "sql.Tx") || strings.Contains(g.typ, "Queries") ||
			strings.Contains(g.typ, "store.Open") || strings.Contains(g.typ, "sql.Open"):
			rep.add("global %s (%s) is a database handle shared by every household", short, loc)
		case strings.Contains(g.typ, "sync.") || strings.Contains(g.typ, "atomic."):
			rep.add("global %s (%s) is a process-wide lock or atomic (%s): it guards state shared by every household", short, loc, strings.TrimSpace(g.typ))
		case writes[key] != "":
			rep.add("global %s (%s) is written at run time by %s: state shared by every household", short, loc, writes[key])
		}
	}

	// 3. The service graph is built by the tenant builder only; a constructor call elsewhere in production
	// code builds one instance for the whole process.
	builders := map[string]string{
		"receipts": "New", "products": "New", "autoprod": "New", "shopping": "New", "english": "New",
		"categories": "New", "shopsuggest": "NewService", "search": "New", "stats": "New", "match": "NewMatcher",
		"jobs": "New", "listplan": "New", "mcpserver": "New", "httpapi": "New", "logos": "New",
	}
	for _, f := range files {
		if !strings.HasPrefix(f.pkg, "cmd") {
			continue // the packages build their own per-Server parts; the process-level composition root is cmd/
		}
		own := filepath.Base(f.pkg)
		ast.Inspect(f.file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok || id.Name == own || builders[id.Name] != sel.Sel.Name {
				return true
			}
			pos := fset.Position(call.Pos())
			rep.add("%s:%d builds the %s service once per process (%s.%s); services must be built per household in the tenant builder",
				strings.TrimPrefix(pos.Filename, "../../"), pos.Line, id.Name, id.Name, sel.Sel.Name)
			return true
		})
	}

	// 4. Opening a database file belongs to the tenant provider (and the command line, which opens the
	// one database of single mode through it).
	for _, f := range files {
		if strings.HasPrefix(f.pkg, "internal/tenancy") || strings.HasPrefix(f.pkg, "internal/store") || strings.HasPrefix(f.pkg, "cmd") {
			continue
		}
		if strings.HasPrefix(f.pkg, "internal/hosted") {
			continue // the control database holds accounts and devices, not household data
		}
		if f.path == "../../internal/backup/backup.go" {
			continue // IntegrityCheck opens the snapshot file it is given, read-only
		}
		ast.Inspect(f.file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && ((id.Name == "store" && sel.Sel.Name == "Open") || (id.Name == "sql" && sel.Sel.Name == "Open")) {
					pos := fset.Position(call.Pos())
					rep.add("%s:%d opens a database outside the tenant provider", strings.TrimPrefix(pos.Filename, "../../"), pos.Line)
				}
			}
			return true
		})
	}
	// 5. The command line must not build the service graph itself either (serve.go / admin.go).
	rep.fail(t, "process-wide state")
}

func resultFields(fd *ast.FuncDecl) []*ast.Field {
	if fd.Type.Results == nil {
		return nil
	}
	return fd.Type.Results.List
}

func rootIdent(e ast.Expr) *ast.Ident {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			return x
		case *ast.IndexExpr:
			e = x.X
		case *ast.SelectorExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		default:
			return nil
		}
	}
}

func exprString(e ast.Expr) string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	ast.Inspect(e, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			b.WriteString(x.Name + " ")
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok {
				b.WriteString(id.Name + "." + x.Sel.Name + " ")
				return false
			}
		}
		return true
	})
	return b.String()
}
