package main

import (
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

type ParsedFile struct {
	dbg      bool
	filename string
	code     []byte
	fileSet  *token.FileSet
	node     *ast.File
	states   map[string]*FnState
}

type FnState struct {
	Name string            // Name of function
	Recv *RecvPair         // Receiver
	Pars map[string]string // Parameters: k:name, v:type
	Rets []*Ret            // All returns
	Migr string            // Target from SetDefaultMigration or empty
	Prop []string
}

type RecvPair struct {
	Name string
	Type string
}

type Ret struct {
	Lvl           string
	Str           string
	Var           Variant
	Args          []Variant
	StepMigration string
}

type SortedRet []*Ret

func (a SortedRet) Len() int           { return len(a) }
func (a SortedRet) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a SortedRet) Less(i, j int) bool { return a[i].Var.Fun < a[j].Var.Fun }

type Variant struct {
	Obj string
	Fun string
	Str string // string representation
}

type SortedVariant []Variant

func (a SortedVariant) Len() int           { return len(a) }
func (a SortedVariant) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a SortedVariant) Less(i, j int) bool { return a[i].Fun < a[j].Fun }

func main() {
	path := flag.String("f", "", "Path to file")
	console := flag.Bool("c", false, "Print uml diagram to console")
	debug := flag.Bool("d", false, "Enable debug")
	flag.Parse()

	if *path == "" {
		fmt.Print("Error: Path to file is not specific\n")
		return
	}
	uml := analyse(*path, *debug)

	if *console {
		fmt.Printf("\n\n\n\n\n~~~~~~~~~~~~~~~~~\n%s", uml)
	}

	writeUml(*path, uml)
}

func isInSet(s1 []string, item string) bool {
	for i1 := range s1 {
		if s1[i1] == item {
			return true
		}
	}
	return false
}

// setDiff returns a new set with all the elements that exist in
// the first set and don't exist in the second set
func setDiff(s1 []string, s2 []string) []string {
	ret := []string{}
	for i1 := range s1 {
		if !isInSet(s2, s1[i1]) {
			ret = append(ret, s1[i1])
		}
	}
	return ret
}

func SetUniWoDup(s1 []string, s2 []string) []string {
	ret := s1
	for i2 := range s2 {
		if !isInSet(s1, s2[i2]) {
			ret = append(ret, s2[i2])
		}
	}
	return ret
}

func analyse(path string, debug bool) string {
	pf := ParseFile(path, debug)
	if nil == pf {
		panic("Cannot parse file " + path)
	}
	uml := "@startuml"
	pf.dbgmsg("\n\n:: ======= resource filename: %s", pf.filename)

	unvisited := []string{"Init"}
	visited := []string{}

	for len(unvisited) > 0 {
		// uml += fmt.Sprintf("\n-vis: %s", visited)
		// uml += fmt.Sprintf("\n-uns: %s", unvisited)

		top := unvisited[0]
		state, ok := pf.states[top]
		if !ok {
			if unvisited[0] != "Init" {
				panic(fmt.Sprintf("Failed to found step:%s", top))
			}
			unvisited = []string{"stepInit"}
			state = pf.states[top]
		}

		visited = append(visited, state.Name)
		// uml += fmt.Sprintf("\n:state.Migr:[%s]", state.Migr)

		pf.dbgmsg("\n\nfn: %s", state.Name) // Function name
		// pf.dbgmsg("\nrecv: %s | %s", state.Recv.Name, state.Recv.Type) // Receiver
		for parName, parType := range state.Pars { // Parameters
			pf.dbgmsg("\npar name: %s | type: %s", parName, parType)
		}

		// curr_migr propagate migration to outgoing arrows
		var possible_migrations []string = nil // Возможные миграции в этом состоянии
		// uml += fmt.Sprintf("\n:%s.Migr: [%s]", state.Name, state.Migr)
		if "NIL" == state.Migr { // Если миграции явно обнулены - никакие миграции не возможны
			possible_migrations = nil
			uml += fmt.Sprintf("\n%s : %s", state.Name, "NIL")
		} else if "" != state.Migr { // Если явно установлена миграция - возможна только она
			possible_migrations = []string{state.Migr}
		} else { // иначе берем унаследованные миграции
			uml += fmt.Sprintf("\n%s : %s", state.Name, "INHERITED")
			possible_migrations = state.Prop
		}

		for _, pos_migr := range possible_migrations {
			uml += fmt.Sprintf("\n%s : %s", state.Name, pos_migr)
			uml += fmt.Sprintf("\n%s -[#blue]-> %s", state.Name, pos_migr)
			if !isInSet(visited, pos_migr) {
				unvisited = append(unvisited, pos_migr)
			}
		}
		sort.Sort(SortedRet(state.Rets))

		for _, ret := range state.Rets {
			// uml += fmt.Sprintf("\n%s -[#green]-> %s", state.Name, ret)

			sort.Sort(SortedVariant(ret.Args))

			pf.dbgmsg("\n%s: ['%s']", ret.Lvl, ret.Str)
			pf.dbgmsg("\nfun: ['%s']\nobj: ['%s']", ret.Var.Fun, ret.Var.Obj)
			// dbg
			// uml += fmt.Sprintf("\n ! %s | %s", ret.StepMigration, ret.Var.Fun)
			switch ret.Var.Fun {
			case "Stop":
				uml += fmt.Sprintf("\n%s --> [*]", state.Name)
			case "Jump", "ThenJump":
				if ret.Args[0].Fun == "Stop" {
					uml += fmt.Sprintf("\n%s --> [*]", state.Name)
				} else {
					uml += fmt.Sprintf("\n%s --> %s : %s", state.Name, ret.Args[0].Fun, ret.Var.Fun)
					unvisited = append(unvisited, ret.Args[0].Fun)
					if nil != possible_migrations {
						pf.states[ret.Args[0].Fun].Prop = SetUniWoDup(pf.states[ret.Args[0].Fun].Prop, possible_migrations)
					}
				}
			case "JumpExt":
				if ret.Args[0].Fun == "Stop" {
					uml += fmt.Sprintf("\n%s --> [*]", state.Name)
				} else {
					uml += fmt.Sprintf("\n%s --> %s : %s", state.Name, ret.Args[0].Fun, ret.Var.Fun)
					unvisited = append(unvisited, ret.Args[0].Fun)
					if len(ret.StepMigration) > 0 {
						uml += fmt.Sprintf("\n%s -[#DarkGreen]-> %s : %s+(StepMigration)", state.Name, ret.StepMigration, ret.Var.Fun)
						unvisited = append(unvisited, ret.StepMigration)
					}
				}
			case "ThenRepeat":
				uml += fmt.Sprintf("\n%s --> %s : ThenRepeat", state.Name, state.Name)
			case "RepeatOrJumpElse":
				uml += fmt.Sprintf("\n%s -[#RoyalBlue]-> %s : RepeatOr(Jump)Else", state.Name, ret.Args[2].Fun)
				uml += fmt.Sprintf("\n%s -[#DarkGreen]-> %s : RepeatOrJump(Else)", state.Name, ret.Args[3].Fun)
			default:
				pf.dbgmsg("\n(=> (. %s %s)", ret.Var.Obj, ret.Var.Fun)
				for _, arg := range ret.Args {
					pf.dbgmsg("\n       %s", fmt.Sprintf("(. %s %s)", arg.Obj, arg.Fun))
				}
				pf.dbgmsg(")")
			}

			// -:- fn representation
			pf.dbgmsg(fmt.Sprintf("\n(-> (. %s %s)", ret.Var.Obj, ret.Var.Fun))

			for _, arg := range ret.Args {
				pf.dbgmsg("\n       %s", fmt.Sprintf("(. %s %s)", arg.Obj, arg.Fun))
			}
			pf.dbgmsg(")")
		}
		unvisited = setDiff(unvisited, visited)
	}

	state_keys := make([]string, 0, len(pf.states))
	for k := range pf.states {
		state_keys = append(state_keys, k)
	}

	uml += "\n@enduml\n"
	return uml
}

func writeUml(path string, uml string) {
	name := filepath.Base(path)
	name = strings.Replace(name, ".go", "", -1) + ".plantuml"
	umlPath := fmt.Sprintf("%s/%s", filepath.Dir(path), name)

	file, err := os.Create(umlPath)
	if err != nil {
		fmt.Printf("%s Failed to create file: %s \n", err.Error(), umlPath)
		return
	}

	defer file.Close()

	_, err = file.WriteString(uml)
	if err != nil {
		fmt.Printf("Failed to write file: %s\n", umlPath)
		return
	}

	err = file.Sync()
	if err != nil {
		fmt.Printf("Failed to sync file: %s\n", umlPath)
		return
	}

	fmt.Printf("Uml saved: %s\n", umlPath)
}

func ParseFile(path string, dbg ...bool) *ParsedFile {
	pf := &ParsedFile{
		filename: path,
		dbg:      dbg[0],
	}

	sourceCode, err := slurpFile(path)
	if err != nil {
		fmt.Printf("Failed to slurpFile file: %s %s\n", path, err.Error())
		return nil
	}
	pf.code = sourceCode

	pf.fileSet = token.NewFileSet()
	node, err := parser.ParseFile(pf.fileSet, pf.filename, pf.code, parser.ParseComments)
	if err != nil {
		fmt.Printf("Failed to parse file: %s\n", path)
		return nil
	}
	pf.node = node

	pf.states = make(map[string]*FnState)

	ast.Inspect(node, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if ok {
			pf.parseMethod(fn)
		}
		return true
	})

	return pf
}

func slurpFile(filename string) ([]byte, error) {
	file, err := os.OpenFile(filename, os.O_RDONLY, 0)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("Can't open file: [%s], %s\n", filename, err.Error()))
	}
	defer file.Close() //nolint: errcheck

	res, err := ioutil.ReadAll(file)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("Can't read file: [%s]\n", filename))
	}
	return res, nil
}

func (pf *ParsedFile) parseMethod(fn *ast.FuncDecl) {

	// I want to analise only method functions (if exists)
	if nil == fn.Recv {
		pf.dbgmsg("\n:parseMethod: skip %s - No receiver", fn.Name.Name)
	} else {
		for _, fld := range fn.Recv.List {
			if len(fld.Names) == 0 {
				continue
			}

			pf.parseRecv(fn, fld)
		}
	}
}

func (pf *ParsedFile) parseRecv(fn *ast.FuncDecl, fld *ast.Field) {
	// Receiver
	recv := &RecvPair{
		Name: fld.Names[0].Name,
		Type: fmt.Sprintf("%s", pf.code[fld.Type.Pos()-1:fld.Type.End()-1]),
	}

	// Parameters
	pars := make(map[string]string, 0)
	name := "unnamed-param"
	for _, par := range fn.Type.Params.List {
		if nil != par.Names {
			name = par.Names[0].Name
		}
		pars[name] = fmt.Sprintf("%s", pf.code[par.Type.Pos()-1:par.Type.End()-1])
	}

	// I want to analyse only methods, who takes context
	if !isMethodTakesCtx(pars) {
		pf.dbgmsg("\n:parseMethod: skip %s - Doesn`t take CTX", fn.Name.Name)
		return
	}

	// I want analyse only methods, which returned values
	if nil == fn.Type.Results {
		pf.dbgmsg("\n:parseMethod: skip %s - No return value", fn.Name.Name)
		return
	}

	// I want to analyze methods which have a `smashine.StateUpdate' result type
	res := fn.Type.Results.List[0].Type
	resSel, ok := res.(*ast.SelectorExpr)
	if !ok || "StateUpdate" != resSel.Sel.Name {
		if pf.dbg {
			fmt.Printf("\n:parseMethod: skip %s - No StateUpdate result type", fn.Name.Name)
		}
		return
	}
	resXstr := fmt.Sprintf("%s", pf.code[resSel.X.Pos()-1:resSel.X.End()-1])
	if "smachine" != resXstr {
		if pf.dbg {
			fmt.Printf("\n:parseMethod: skip %s - No smachine selector result type", fn.Name.Name)
		}
		return
	}

	// Show name (debug)
	pf.dbgmsg("\n:parseMethod: (sm-name) %s", fn.Name.Name)

	// Find all Return Statements and SetDefaultMigration calls
	var rets = make([]*Ret, 0)
	var migr = ""
	for _, smth := range fn.Body.List { // ∀ fn.Body.List ← (or RetStmt (Inspect ...))
		retStmt, ok := smth.(*ast.ReturnStmt)
		if ok {
			// return from top-level statements of function
			rets = append(rets, pf.collectRets(retStmt, "Top")...)
		} else {
			ast.Inspect(smth, func(in ast.Node) bool {
				// Find Return Statements
				retStmt, ok := in.(*ast.ReturnStmt) // ←
				if ok {
					// return from deep-level function statememt
					rets = append(rets, pf.collectRets(retStmt, "Deep")...)
				} else {
					migr = pf.findSetDefaultMigration(migr, in)
				}
				return true
			})
		}
	}

	pf.states[fn.Name.Name] = &FnState{
		Name: fn.Name.Name,
		Recv: recv,
		Pars: pars,
		Rets: rets,
		Migr: migr,
	}
}

func (pf *ParsedFile) findSetDefaultMigration(migr string, in ast.Node) string {
	// Find "ctx.SetDefaultMigration(some_target)"
	stmt, ok := in.(*ast.ExprStmt)
	if ok {
		callexpr, ok := stmt.X.(*ast.CallExpr)
		if ok {
			selexpr, ok := callexpr.Fun.(*ast.SelectorExpr)
			if ok {
				selexpr_x, ok := selexpr.X.(*ast.Ident)
				if ok {
					if ("ctx" == selexpr_x.Name) &&
						("SetDefaultMigration" == selexpr.Sel.Name) {
						for _, arg := range callexpr.Args {
							argsel, ok := arg.(*ast.SelectorExpr)
							if ok {
								pf.dbgmsg(fmt.Sprintf("\n>>>:[%s]", argsel.Sel.Name))
								migr = argsel.Sel.Name
							}
							argnil, ok := arg.(*ast.Ident)
							if ok {
								pf.dbgmsg(fmt.Sprintf("\n>>>:[%s]", argnil))
								migr = "NIL"
							}
						}
					}
				}
			}
		}
	}
	return migr
}

func (pf *ParsedFile) dbgmsg(msg string, par ...interface{}) {
	if pf.dbg {
		fmt.Printf(msg, par...)
	}
}

func isMethodTakesCtx(pars map[string]string) bool {
	for _, parType := range pars {
		if strings.Contains(parType, "Context") {
			return true
		}
	}
	return false
}

func (pf *ParsedFile) collectRets(retStmt *ast.ReturnStmt, level string) []*Ret {
	var acc []*Ret
	for _, ret := range retStmt.Results {
		item := &Ret{
			Lvl: level,
			Str: fmt.Sprintf("%s", pf.code[ret.Pos()-1:ret.End()-1]),
		}
		pf.dbgmsg("\n :collectRet: ~~~~~~ (item.Str) : %s", item.Str)

		for _, retNode := range retStmt.Results {
			switch retNode.(type) {
			case *ast.CallExpr:
				retCall := retNode.(*ast.CallExpr)
				switch retCall.Fun.(type) {
				case *ast.SelectorExpr:
					retSelector := retCall.Fun.(*ast.SelectorExpr)
					item.Var.Fun = retSelector.Sel.Name
					pf.dbgmsg("\n  :collectRet: (Selector) (%s.) =:[%s]:=", reflect.TypeOf(retSelector.X), retSelector.Sel.Name)
					switch retSelector.X.(type) { // Analyse started from [selector.*]
					case *ast.Ident:
						retX := retSelector.X.(*ast.Ident)
						item.Var.Obj = retX.Name
						pf.dbgmsg("\n   :collectRet: (ident) : %s _._", item.Var.Obj)
						switch item.Var.Fun {
						case "Jump", "Stop", "JumpExt":
						default:
							pf.dbgmsg("\n:collectRets: [WARN]: UNKNOWN RET SELECTOR '%s' in '%s.%s'",
								item.Var.Fun, item.Var.Obj, item.Var.Fun)
						}
					case *ast.CallExpr:
						subX := retSelector.X.(*ast.CallExpr)
						subXStr := fmt.Sprintf("%s", pf.code[subX.Pos()-1:subX.End()-1])
						item.Var.Obj = subXStr
						pf.dbgmsg("\n   :collectRet: (call to selector) : %s _._", item.Var.Obj)
						switch item.Var.Fun { // Check Fun (nb: not arg!)
						case "ThenRepeat", "ThenJump":
						default:
							fmt.Printf("\n:collectRets: [WARN]: UNKNOWN RET SUB SELECTOR '%s' in '%s'",
								item.Var.Fun, item.Var.Obj, item.Var.Fun)
						}
					default:
						pf.dbgmsg("\n:collectRets: [ERR]: UNKNOWN RETSELECTOR %s | ",
							reflect.TypeOf(retSelector.X),
							pf.code[retSelector.X.Pos()-1:retSelector.X.End()-1],
						)
					}

					// Args
					accArgs := make([]Variant, 0)
					for _, retarg := range retCall.Args {
						pf.dbgmsg("\n   -:collectRet: arg type [%s]", reflect.TypeOf(retarg))
						switch retarg.(type) {
						case *ast.SelectorExpr:
							sel := retarg.(*ast.SelectorExpr)
							selName := fmt.Sprintf("%s", pf.code[sel.X.Pos()-1:sel.X.End()-1])
							pf.dbgmsg("\n   -|[%s] %s .|. %s", reflect.TypeOf(sel), selName, sel.Sel.Name)
							arg := Variant{
								Obj: selName,
								Fun: sel.Sel.Name,
							}
							accArgs = append(accArgs, arg)
						case *ast.CompositeLit:
							cl := retarg.(*ast.CompositeLit)
							// We know only JumpExt composite literal
							arg := Variant{}
							if "JumpExt" == item.Var.Fun {
								ast.Inspect(cl, func(n ast.Node) bool {
									exp, ok := n.(*ast.KeyValueExpr)
									if ok {
										keystr := fmt.Sprintf("%s", exp.Key)
										switch keystr {
										case "Transition":
											sel := exp.Value.(*ast.SelectorExpr)
											selName := fmt.Sprintf("%s", pf.code[sel.X.Pos()-1:sel.X.End()-1])
											arg = Variant{
												Obj: selName,
												Fun: sel.Sel.Name,
											}
											pf.dbgmsg("\n   -| -transition: %s.%s", selName, sel.Sel.Name)
										case "Migration":
											sel := exp.Value.(*ast.SelectorExpr)
											selName := fmt.Sprintf("%s", pf.code[sel.X.Pos()-1:sel.X.End()-1])
											item.StepMigration = sel.Sel.Name
											// arg = Variant{
											//     Type: SelectorType,
											//     Obj:  selName,
											//     Fun:  sel.Sel.Name,
											// }
											pf.dbgmsg("\n   -| --migration: %s.%s", selName, sel.Sel.Name)
										default:
											pf.dbgmsg("\n:collectRets: [ERR]: UNKNOWN keystr [%s]", keystr)
										}
									}
									return true
								}) // end of Inspect
							} else {
								pf.dbgmsg("\n:collectRets: [ERR]: UNK JumpExt transition")
							}
							accArgs = append(accArgs, arg)
						case *ast.FuncLit:
							fl := retarg.(*ast.FuncLit)
							var r []*Ret
							for _, f := range fl.Body.List {
								switch f.(type) {
								case *ast.ReturnStmt:
									ret0 := f.(*ast.ReturnStmt)
									//TODO remove duplicates
									r = append(r, pf.collectRets(ret0, "Deep")...)
								default:
									pf.dbgmsg("\n:collectRets: [ERR]: UNK FuncLit return")
								}
							}

							for _, ret := range r {
								accArgs = append(accArgs, ret.Var)
							}
						default:
							pf.dbgmsg("\n:collectRets: [ERR]: UNKNOWN RETARGtype [%s] :OF: %s", reflect.TypeOf(retarg), retarg)
						}
					} // end of args
					item.Args = accArgs
				default:
					pf.dbgmsg("\n:collectRets: [ERR]: UNKNOWN RETSEL %s", fmt.Sprintf("%s", reflect.TypeOf(retCall.Fun)))
				}
			default:
				pf.dbgmsg("\n [ERR]: UNKNOWN TYPE OF RETNODE %s", fmt.Sprintf("%s", reflect.TypeOf(retNode)))
			} // end of switch retnode type
		}
		acc = append(acc, item)
	}
	return acc
}
