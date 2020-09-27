package goone

import (
	"go/ast"
	"go/token"
	"go/types"
	"sync"

	"github.com/gostaticanalysis/analysisutil"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

const doc = "go_one finds N+1 query "

type SearchCache struct{
	sync.Mutex
	searchMemo map[token.Pos]bool
}


func NewSearchCache() *SearchCache  {
	return &SearchCache{
		searchMemo: make(map[token.Pos]bool),
	}
}

func (m *SearchCache) Set(key token.Pos, value bool) {
	m.Lock()
	m.searchMemo[key] = value
	m.Unlock()
}

func (m *SearchCache) Get(key token.Pos) bool {
	m.Lock()
	value := m.searchMemo[key]
	m.Unlock()
	return value
}


type FuncCache struct{
	sync.Mutex
	funcMemo map[token.Pos]bool
}


func NewFuncCache() *FuncCache  {
	return &FuncCache{
		funcMemo: make(map[token.Pos]bool),
	}
}

func (m *FuncCache) Set(key token.Pos, value bool) {
	m.Lock()
	m.funcMemo[key] = value
	m.Unlock()
}

func (m* FuncCache) Exists(key token.Pos) bool {
	m.Lock()
	_, exist := m.funcMemo[key]
	m.Unlock()
	return exist
}

func (m *FuncCache) Get(key token.Pos) bool {
	m.Lock()
	value := m.funcMemo[key]
	m.Unlock()
	return value
}

var searchCache *SearchCache
var funcCache *FuncCache


// Analyzer is ...
var Analyzer = &analysis.Analyzer{
	Name: "go_one",
	Doc:  doc,
	Run:  run,
	Requires: []*analysis.Analyzer{
		inspect.Analyzer,
	},
}
var sqlTypes []types.Type

func appendTypes(pass *analysis.Pass, pkg, name string) {
	if typ := analysisutil.TypeOf(pass, pkg, name); typ != nil {
		sqlTypes = append(sqlTypes, typ)
	}
}

func prepareTypes(pass *analysis.Pass) {

	appendTypes(pass, "database/sql", "*DB")
	appendTypes(pass, "gorm.io/gorm", "*DB")
	appendTypes(pass, "gopkg.in/gorp.v1", "*DbMap")
	appendTypes(pass, "gopkg.in/gorp.v2", "*DbMap")
	appendTypes(pass, "github.com/go-gorp/gorp/v3", "*DbMap")
	appendTypes(pass, "github.com/jmoiron/sqlx", "*DB")

}

func run(pass *analysis.Pass) (interface{}, error) {
	searchCache = NewSearchCache()
	funcCache = NewFuncCache()
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	prepareTypes(pass)

	if sqlTypes == nil {
		return nil, nil
	}
	forFilter := []ast.Node{
		(*ast.ForStmt)(nil),
		(*ast.RangeStmt)(nil),
	}

	inspect.Preorder(forFilter, func(n ast.Node) {
		switch n := n.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			findQuery(pass, n, nil)
		}

	})

	return nil, nil
}

func anotherFileSearch(pass *analysis.Pass, funcExpr *ast.Ident, parentNode ast.Node) bool {
	if anotherFileNode := pass.TypesInfo.ObjectOf(funcExpr); anotherFileNode != nil {
		file := analysisutil.File(pass, anotherFileNode.Pos())

		if file == nil {
			return false
		}
		inspect := inspector.New([]*ast.File{file})
		types := []ast.Node{new(ast.FuncDecl)}
		inspect.WithStack(types, func(n ast.Node, push bool, stack []ast.Node) bool {
			if !push {
				return false
			}

			findQuery(pass, n, parentNode)
			return true
		})

	}

	return false
}

func findQuery(pass *analysis.Pass, rootNode, parentNode ast.Node) {

	if funcCache.Exists(rootNode.Pos()) {
		if funcCache.Get(rootNode.Pos()) {
			pass.Reportf(parentNode.Pos(), "this query is called in a loop")
		}
		return
	}

	ast.Inspect(rootNode, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.Ident:

			if tv, ok := pass.TypesInfo.Types[node]; ok {
				reportNode := parentNode
				if reportNode == nil {
					reportNode = node
				}
				for _, typ := range sqlTypes {
					if types.Identical(tv.Type, typ) {
						pass.Reportf(reportNode.Pos(), "this query is called in a loop")
						funcCache.Set(rootNode.Pos(),true)
						return false
					}
				}

			}
		case *ast.CallExpr:
			switch funcExpr := node.Fun.(type) {
			case *ast.Ident:
				obj := funcExpr.Obj
				if obj == nil {
					return anotherFileSearch(pass, funcExpr, node)
				}
				switch decl := obj.Decl.(type) {
				case *ast.FuncDecl:
					if !searchCache.Get(decl.Pos()) {
						searchCache.Set(decl.Pos(),true)
						findQuery(pass, decl, node)
					} else {
						if funcCache.Get(decl.Pos()){
							pass.Reportf(node.Pos(), "this query is called in a loop")
						}
					}

				}

			}

		}
		return true

	})
}
