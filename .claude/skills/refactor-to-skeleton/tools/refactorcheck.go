//go:build ignore

// refactorcheck proves that a refactoring accounted for everything the old
// code contained. It is part of the refactor-to-skeleton skill.
//
//	go run refactorcheck.go snapshot   record the code as it is before the refactoring
//	go run refactorcheck.go check      compare the working tree with the snapshot and the ledger
//	go run refactorcheck.go diff       show old against new for the declarations that moved
//	go run refactorcheck.go report     list what was replaced or dropped, for sign-off
//
// The snapshot records every top-level declaration, every test, every string
// and number literal and every non-Go file of the old code. check passes only
// when each of them is still there unchanged, or has a line in the ledger
// saying where it went.
//
// This file uses only the standard library and no recent language features,
// so it runs inside any module regardless of the Go version in its go.mod.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const usage = `usage: go run refactorcheck.go <command> [flags]

  snapshot   record declarations, tests, literals and files of the code under -root
  check      fail unless everything in the snapshot is unchanged or in the ledger
  diff       print old and new bodies of moved declarations (args filter by name)
  report     print what the ledger marks as replaced, changed or dropped, and rewritten literals

flags (all commands):
  -root dir        the project (default ".")
  -snapshot file   default <root>/.refactor/snapshot.json
  -ledger file     default <root>/.refactor/ledger.tsv
check only:
  -append          add a TODO line to the ledger for everything unaccounted for
diff only:
  -stat            one line per declaration instead of the full diff

Ledger lines are tab-separated: kind, key, disposition, targets, note.

  kind         decl | test | lit | file
  key          as printed by check
  disposition  moved     the logic now lives in the targets (for a split, list them all)
               replaced  the targets do the same job but were not derived from the old code
               changed   (file only) edited in place
               dropped   it is gone
  targets      comma-separated: a declaration such as internal/feature/user.Service.Create,
               file:<path>, or ext:<import path>
  note         required for replaced, changed and dropped, and for every lit line
`

type decl struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // decl or test
	File string `json:"file"`
	Line int    `json:"line"`
	Hash string `json:"hash"`
	Body string `json:"body"`
}

type literal struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	File  string `json:"file"`
	Line  int    `json:"line"`
}

type fileEntry struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

type snapshot struct {
	Commit   string      `json:"commit"`
	Decls    []decl      `json:"decls"`
	Literals []literal   `json:"literals"`
	Files    []fileEntry `json:"files"`
}

// tree is what a scan of a directory finds.
type tree struct {
	decls    map[string]decl // key: kind + " " + id
	order    []string
	literals map[string]literal
	litOrder []string
	files    map[string]string // non-Go files: path -> hash
	imports  map[string]bool
	goText   string   // every literal value, joined, for substring search
	texts    []string // contents of the non-Go text files
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	root := fs.String("root", ".", "")
	snapPath := fs.String("snapshot", "", "")
	ledgerPath := fs.String("ledger", "", "")
	appendTodo := fs.Bool("append", false, "")
	stat := fs.Bool("stat", false, "")
	fs.Parse(os.Args[2:])
	if *snapPath == "" {
		*snapPath = filepath.Join(*root, ".refactor", "snapshot.json")
	}
	if *ledgerPath == "" {
		*ledgerPath = filepath.Join(*root, ".refactor", "ledger.tsv")
	}

	var err error
	code := 0
	switch cmd {
	case "snapshot":
		err = runSnapshot(*root, *snapPath, *ledgerPath)
	case "check":
		code, err = runCheck(*root, *snapPath, *ledgerPath, *appendTodo)
	case "diff":
		err = runDiff(*root, *snapPath, *ledgerPath, *stat, fs.Args())
	case "report":
		err = runReport(*snapPath, *ledgerPath)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "refactorcheck:", err)
		os.Exit(2)
	}
	os.Exit(code)
}

// ---------------------------------------------------------------- scanning

var skipDirs = map[string]bool{
	".git": true, ".refactor": true, ".claude": true, ".idea": true, ".vscode": true,
	"vendor": true, "node_modules": true,
}

// listFiles returns the files of the project, relative to root. In a git
// repository these are the tracked files plus the untracked ones that are not
// ignored, so build output and downloaded dependencies stay out.
func listFiles(root string) ([]string, error) {
	var paths []string
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err == nil {
		for _, p := range strings.Split(string(out), "\x00") {
			if p != "" {
				paths = append(paths, p)
			}
		}
	} else {
		err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			paths = append(paths, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	var kept []string
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] || skipped(p) {
			continue
		}
		seen[p] = true
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil || !info.Mode().IsRegular() {
			continue // deleted in the working tree, a symlink or a submodule
		}
		kept = append(kept, p)
	}
	sort.Strings(kept)
	return kept, nil
}

func skipped(path string) bool {
	parts := strings.Split(path, "/")
	for _, dir := range parts[:len(parts)-1] {
		if skipDirs[dir] {
			return true
		}
	}
	return false
}

func inTestdata(path string) bool {
	return strings.HasPrefix(path, "testdata/") || strings.Contains(path, "/testdata/")
}

// scan reads the project under root. With generated set, generated Go files
// count like hand-written ones; the snapshot leaves them out because they are
// derived from files that are tracked themselves.
func scan(root string, generated bool) (*tree, error) {
	paths, err := listFiles(root)
	if err != nil {
		return nil, err
	}
	t := &tree{
		decls:    map[string]decl{},
		literals: map[string]literal{},
		files:    map[string]string{},
		imports:  map[string]bool{},
	}
	fset := token.NewFileSet()
	var values []string
	for _, path := range paths {
		src, err := ioutil.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return nil, err
		}
		if !strings.HasSuffix(path, ".go") || inTestdata(path) {
			t.files[path] = hashOf(src)
			if len(src) <= 2<<20 && bytes.IndexByte(src, 0) < 0 {
				t.texts = append(t.texts, normalize(string(src)))
			}
			continue
		}
		file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %v", path, err)
		}
		if !generated && isGenerated(file) {
			continue
		}
		for _, imp := range file.Imports {
			if p, err := strconv.Unquote(imp.Path.Value); err == nil {
				t.imports[p] = true
			}
		}
		if strings.HasSuffix(path, "_test.go") {
			t.addTests(fset, path, file)
			continue
		}
		t.addDecls(fset, path, file)
		for _, l := range literalsOf(fset, path, file) {
			if _, dup := t.literals[l.Key]; !dup {
				t.literals[l.Key] = l
				t.litOrder = append(t.litOrder, l.Key)
				values = append(values, l.Value)
			}
		}
	}
	t.goText = strings.Join(values, "\x00")
	return t, nil
}

func isGenerated(file *ast.File) bool {
	for _, group := range file.Comments {
		if group.Pos() > file.Package {
			break
		}
		for _, c := range group.List {
			if strings.HasPrefix(c.Text, "// Code generated ") && strings.HasSuffix(c.Text, " DO NOT EDIT.") {
				return true
			}
		}
	}
	return false
}

func dirOf(path string) string {
	i := strings.LastIndex(path, "/")
	if i < 0 {
		return "."
	}
	return path[:i]
}

func (t *tree) add(kind, id, path string, line int, body string) {
	key := kind + " " + id
	if _, dup := t.decls[key]; dup {
		// The same name twice in one directory: build-tagged variants, or
		// a package and its external test package.
		id += "@" + path[strings.LastIndex(path, "/")+1:]
		key = kind + " " + id
		for n := 2; ; n++ {
			if _, dup := t.decls[key]; !dup {
				break
			}
			key = kind + " " + id + "#" + strconv.Itoa(n)
		}
		id = strings.TrimPrefix(key, kind+" ")
	}
	t.decls[key] = decl{ID: id, Kind: kind, File: path, Line: line, Hash: hashOf([]byte(body)), Body: body}
	t.order = append(t.order, key)
}

func (t *tree) addDecls(fset *token.FileSet, path string, file *ast.File) {
	dir := dirOf(path)
	for _, d := range file.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			name := d.Name.Name
			if d.Recv != nil && len(d.Recv.List) > 0 {
				name = receiverName(d.Recv.List[0].Type) + "." + name
			}
			if name == "init" {
				name = "init@" + path[strings.LastIndex(path, "/")+1:]
			}
			fn := *d
			fn.Doc = nil
			t.add("decl", dir+"."+name, path, fset.Position(d.Pos()).Line, render(fset, &fn))
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				continue
			}
			// The value of a constant in a block can depend on its position
			// (iota, repeated expressions), so the block is part of its body.
			block := ""
			if d.Tok == token.CONST && d.Lparen.IsValid() && len(d.Specs) > 1 {
				whole := *d
				whole.Doc = nil
				block = render(fset, &whole)
			}
			for _, spec := range d.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					ts := *spec
					ts.Doc, ts.Comment = nil, nil
					t.add("decl", dir+"."+spec.Name.Name, path, fset.Position(spec.Pos()).Line, "type "+render(fset, &ts))
				case *ast.ValueSpec:
					vs := *spec
					vs.Doc, vs.Comment = nil, nil
					body := strings.ToLower(d.Tok.String()) + " " + render(fset, &vs)
					for _, name := range spec.Names {
						if name.Name == "_" {
							continue
						}
						b := body
						if block != "" {
							b = name.Name + " in " + block
						}
						t.add("decl", dir+"."+name.Name, path, fset.Position(name.Pos()).Line, b)
					}
				}
			}
		}
	}
}

func (t *tree) addTests(fset *token.FileSet, path string, file *ast.File) {
	dir := dirOf(path)
	for _, d := range file.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		for _, prefix := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
			if strings.HasPrefix(fn.Name.Name, prefix) {
				f := *fn
				f.Doc = nil
				t.add("test", dir+"."+fn.Name.Name, path, fset.Position(fn.Pos()).Line, render(fset, &f))
				break
			}
		}
	}
}

func receiverName(expr ast.Expr) string {
	for {
		switch e := expr.(type) {
		case *ast.StarExpr:
			expr = e.X
		case *ast.ParenExpr:
			expr = e.X
		case *ast.IndexExpr:
			expr = e.X
		case *ast.IndexListExpr:
			expr = e.X
		case *ast.Ident:
			return e.Name
		default:
			return "?"
		}
	}
}

// render prints a node without its comments, so that only code counts when
// two bodies are compared.
func render(fset *token.FileSet, node interface{}) string {
	var buf bytes.Buffer
	cfg := printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 8}
	if err := cfg.Fprint(&buf, fset, node); err != nil {
		return fmt.Sprintf("<unprintable: %v>", err)
	}
	return buf.String()
}

func literalsOf(fset *token.FileSet, path string, file *ast.File) []literal {
	var out []literal
	skip := map[*ast.BasicLit]bool{}
	for _, imp := range file.Imports {
		skip[imp.Path] = true
	}
	emit := func(prefix, value string, pos token.Pos) {
		if value == "" {
			return
		}
		key := prefix + ":" + value
		if prefix == "s" || prefix == "tag" {
			key = prefix + ":" + hashOf([]byte(value))[:10]
		}
		out = append(out, literal{Key: key, Value: value, File: path, Line: fset.Position(pos).Line})
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Field:
			if n.Tag != nil {
				skip[n.Tag] = true
				if raw, err := strconv.Unquote(n.Tag.Value); err == nil {
					for _, pair := range tagPairs(raw) {
						emit("tag", pair, n.Tag.Pos())
					}
				}
			}
		case *ast.BasicLit:
			if skip[n] {
				return true
			}
			switch n.Kind {
			case token.STRING:
				if s, err := strconv.Unquote(n.Value); err == nil {
					emit("s", normalize(s), n.Pos())
				}
			case token.INT, token.FLOAT:
				if v := number(n.Value); v != "0" && v != "1" {
					emit("n", v, n.Pos())
				}
			}
		}
		return true
	})
	return out
}

// tagPairs splits a struct tag into its key:"value" pairs. Each pair is a
// literal of its own, because a refactoring typically moves the db tag and
// the json tag of one field to different structs.
func tagPairs(tag string) []string {
	var pairs []string
	for tag != "" {
		tag = strings.TrimLeft(tag, " ")
		colon := strings.Index(tag, ":\"")
		if colon <= 0 {
			break
		}
		end := colon + 2
		for end < len(tag) && tag[end] != '"' {
			if tag[end] == '\\' {
				end++
			}
			end++
		}
		if end >= len(tag) {
			break
		}
		pairs = append(pairs, tag[:end+1])
		tag = tag[end+1:]
	}
	return pairs
}

// normalize collapses whitespace, so reformatting a query is not a change.
func normalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func number(raw string) string {
	clean := strings.Replace(raw, "_", "", -1)
	if i, err := strconv.ParseInt(clean, 0, 64); err == nil {
		return strconv.FormatInt(i, 10)
	}
	if f, err := strconv.ParseFloat(clean, 64); err == nil {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	return raw
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16]
}

// has reports whether the literal still occurs in the tree: as a literal of
// its own, inside a longer string literal, or in a non-Go text file (a route
// that moved into the OpenAPI spec, a topic name that moved into the config).
func (t *tree) has(l literal) bool {
	if _, ok := t.literals[l.Key]; ok {
		return true
	}
	if strings.HasPrefix(l.Key, "n:") {
		for _, text := range t.texts {
			if containsNumber(text, l.Value) {
				return true
			}
		}
		return false
	}
	if strings.HasPrefix(l.Key, "s:") && len(l.Value) >= 4 && strings.Contains(t.goText, l.Value) {
		return true
	}
	if len(l.Value) >= 3 {
		for _, text := range t.texts {
			if strings.Contains(text, l.Value) {
				return true
			}
		}
	}
	return false
}

func containsNumber(text, num string) bool {
	for from := 0; ; {
		i := strings.Index(text[from:], num)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(num)
		if !(start > 0 && numberByte(text[start-1])) && !(end < len(text) && numberByte(text[end])) {
			return true
		}
		from = start + 1
	}
}

func numberByte(b byte) bool { return b >= '0' && b <= '9' || b == '.' }

// ---------------------------------------------------------------- snapshot

const ledgerHeader = `# Refactoring ledger: where everything from the old code went.
# Tab-separated: kind, key, disposition, targets, note. Lines starting with # are comments.
# "go run refactorcheck.go" without arguments explains the columns;
# "check -append" adds a TODO line for everything that is not accounted for yet.
`

func runSnapshot(root, snapPath, ledgerPath string) error {
	if _, err := os.Stat(snapPath); err == nil {
		return fmt.Errorf("%s already exists; a snapshot is taken once, before the refactoring starts. Delete it only if no code has been changed yet", snapPath)
	}
	t, err := scan(root, false)
	if err != nil {
		return err
	}
	snap := snapshot{}
	if out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output(); err == nil {
		snap.Commit = strings.TrimSpace(string(out))
	}
	for _, key := range t.order {
		snap.Decls = append(snap.Decls, t.decls[key])
	}
	for _, key := range t.litOrder {
		snap.Literals = append(snap.Literals, t.literals[key])
	}
	var paths []string
	for p := range t.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		snap.Files = append(snap.Files, fileEntry{Path: p, Hash: t.files[p]})
	}

	if err := os.MkdirAll(filepath.Dir(snapPath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snap, "", " ")
	if err != nil {
		return err
	}
	if err := ioutil.WriteFile(snapPath, data, 0644); err != nil {
		return err
	}
	if _, err := os.Stat(ledgerPath); os.IsNotExist(err) {
		if err := ioutil.WriteFile(ledgerPath, []byte(ledgerHeader), 0644); err != nil {
			return err
		}
	}
	tests := 0
	for _, d := range snap.Decls {
		if d.Kind == "test" {
			tests++
		}
	}
	fmt.Printf("snapshot of %s written to %s\n", orDefault(snap.Commit, "the working tree"), snapPath)
	fmt.Printf("  %d declarations, %d tests, %d literals, %d non-Go files\n",
		len(snap.Decls)-tests, tests, len(snap.Literals), len(snap.Files))
	return nil
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func loadSnapshot(path string) (*snapshot, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%v (run the snapshot command before changing any code)", err)
	}
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	return &snap, nil
}

// ------------------------------------------------------------------ ledger

type row struct {
	line        int
	kind, key   string
	disposition string
	targets     []string
	note        string
}

func loadLedger(path string) (map[string]*row, []string, error) {
	data, err := ioutil.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]*row{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	rows := map[string]*row{}
	var problems []string
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		cols := strings.Split(line, "\t")
		for j := range cols {
			cols[j] = strings.TrimSpace(cols[j])
		}
		if len(cols) < 3 {
			problems = append(problems, fmt.Sprintf("ledger line %d: want tab-separated kind, key, disposition, targets, note", i+1))
			continue
		}
		r := &row{line: i + 1, kind: cols[0], key: cols[1], disposition: cols[2]}
		if len(cols) > 3 {
			for _, target := range strings.Split(cols[3], ",") {
				if target = strings.TrimSpace(target); target != "" {
					r.targets = append(r.targets, target)
				}
			}
		}
		if len(cols) > 4 {
			r.note = strings.TrimSpace(strings.Join(cols[4:], " "))
		}
		id := r.kind + " " + r.key
		if prev, dup := rows[id]; dup {
			problems = append(problems, fmt.Sprintf("ledger line %d: %s %s is already on line %d", i+1, r.kind, r.key, prev.line))
			continue
		}
		rows[id] = r
	}
	return rows, problems, nil
}

// validate returns what is wrong with a ledger line, or "".
func (r *row) validate(t *tree, root string) string {
	allowed := map[string][]string{
		"decl": {"moved", "replaced", "dropped"},
		"test": {"moved", "replaced", "dropped"},
		"lit":  {"moved", "dropped"},
		"file": {"moved", "replaced", "changed", "dropped"},
	}[r.kind]
	if r.disposition == "TODO" {
		return "still TODO"
	}
	ok := false
	for _, d := range allowed {
		ok = ok || d == r.disposition
	}
	if !ok {
		return fmt.Sprintf("disposition %q, want one of %s", r.disposition, strings.Join(allowed, ", "))
	}
	needsTargets := r.disposition == "moved" || r.disposition == "replaced"
	if needsTargets && len(r.targets) == 0 {
		return r.disposition + " needs at least one target"
	}
	if !needsTargets && len(r.targets) > 0 {
		return r.disposition + " takes no targets; put the explanation in the note column"
	}
	for _, target := range r.targets {
		if msg := checkTarget(r, target, t, root); msg != "" {
			return msg
		}
	}
	needsNote := r.disposition != "moved" || r.kind == "lit"
	if needsNote && (r.note == "" || r.note == "TODO") {
		return "a note is required: say why, and what shows it is safe"
	}
	return ""
}

func checkTarget(r *row, target string, t *tree, root string) string {
	switch {
	case strings.HasPrefix(target, "file:"):
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(target, "file:")))); err != nil {
			return "target " + target + " does not exist"
		}
	case strings.HasPrefix(target, "ext:"):
		pkg := strings.TrimPrefix(target, "ext:")
		for imp := range t.imports {
			if imp == pkg || strings.HasPrefix(imp, pkg+"/") {
				return ""
			}
		}
		return "target " + target + ": no file imports that package"
	case r.kind == "file":
		return "target " + target + ": a file line takes file:<path> targets"
	default:
		_, isDecl := t.decls["decl "+target]
		_, isTest := t.decls["test "+target]
		switch {
		case r.kind == "test" && r.disposition == "moved" && !isTest:
			return "target " + target + " is not a test in the working tree (a test moves to a test)"
		case r.kind == "decl" && r.disposition == "moved" && !isDecl:
			return "target " + target + " is not a declaration in the working tree"
		case !isDecl && !isTest:
			return "target " + target + " is not a declaration in the working tree"
		}
	}
	return ""
}

// ------------------------------------------------------------------- check

type finding struct {
	kind, key string
	where     string // old location
	problem   string
	comment   string // context written above an appended TODO line
	hasRow    bool
}

func runCheck(root, snapPath, ledgerPath string, appendTodo bool) (int, error) {
	snap, err := loadSnapshot(snapPath)
	if err != nil {
		return 0, err
	}
	rows, problems, err := loadLedger(ledgerPath)
	if err != nil {
		return 0, err
	}
	t, err := scan(root, true)
	if err != nil {
		return 0, err
	}

	var findings []finding
	used := map[string]bool{}
	counts := map[string]int{}

	judge := func(kind, key, where, comment string, unchanged bool, missing string) {
		id := kind + " " + key
		r, ok := rows[id]
		if !ok {
			if unchanged {
				counts["unchanged"]++
				return
			}
			findings = append(findings, finding{kind: kind, key: key, where: where, problem: missing, comment: comment})
			return
		}
		used[id] = true
		if msg := r.validate(t, root); msg != "" {
			findings = append(findings, finding{kind: kind, key: key, where: where, hasRow: true,
				problem: fmt.Sprintf("ledger line %d: %s", r.line, msg)})
			return
		}
		if r.disposition == "dropped" && unchanged {
			findings = append(findings, finding{kind: kind, key: key, where: where, hasRow: true,
				problem: fmt.Sprintf("ledger line %d says dropped, but it is still there", r.line)})
			return
		}
		counts[r.disposition]++
	}

	for _, d := range snap.Decls {
		now, exists := t.decls[d.Kind+" "+d.ID]
		missing := "no longer exists"
		if exists {
			missing = "changed in place (use: moved, with itself as the target)"
		}
		where := fmt.Sprintf("%s:%d", d.File, d.Line)
		judge(d.Kind, d.ID, where, where, exists && now.Hash == d.Hash, missing)
	}
	for _, l := range snap.Literals {
		comment := fmt.Sprintf("%s  (%s:%d)", quote(l.Value), l.File, l.Line)
		judge("lit", l.Key, fmt.Sprintf("%s:%d", l.File, l.Line), comment, t.has(l),
			"literal "+quote(l.Value)+" is nowhere in the working tree")
	}
	for _, f := range snap.Files {
		hash, exists := t.files[f.Path]
		missing := "file no longer exists"
		if exists {
			missing = "file changed (use: changed, with a note)"
		}
		judge("file", f.Path, f.Path, "", exists && hash == f.Hash, missing)
	}

	var ids []string
	for id := range rows {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if !used[id] {
			r := rows[id]
			problems = append(problems, fmt.Sprintf("ledger line %d: %s %s is not in the snapshot (a typo in the key?)", r.line, r.kind, r.key))
		}
	}

	if len(findings) == 0 && len(problems) == 0 {
		fmt.Printf("OK: everything in the snapshot is accounted for (%s)\n", summary(counts))
		return 0, nil
	}

	for _, p := range problems {
		fmt.Println(p)
	}
	var todo []finding
	for _, kind := range []string{"decl", "test", "lit", "file"} {
		for _, f := range findings {
			if f.kind != kind {
				continue
			}
			fmt.Printf("%s %s (%s): %s\n", f.kind, f.key, f.where, f.problem)
			if !f.hasRow {
				todo = append(todo, f)
			}
		}
	}
	fmt.Printf("\nFAIL: %d not accounted for, %d ledger lines to fix (%s)\n",
		len(todo), len(findings)-len(todo)+len(problems), summary(counts))

	if len(todo) > 0 && appendTodo {
		var buf bytes.Buffer
		for _, f := range todo {
			if f.comment != "" {
				fmt.Fprintf(&buf, "# %s\n", f.comment)
			}
			fmt.Fprintf(&buf, "%s\t%s\tTODO\t\t\n", f.kind, f.key)
		}
		if err := os.MkdirAll(filepath.Dir(ledgerPath), 0755); err != nil {
			return 0, err
		}
		file, err := os.OpenFile(ledgerPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return 0, err
		}
		defer file.Close()
		if _, err := file.Write(buf.Bytes()); err != nil {
			return 0, err
		}
		fmt.Printf("appended %d TODO lines to %s\n", len(todo), ledgerPath)
	} else if len(todo) > 0 {
		fmt.Println("run check -append to add a TODO line for each to the ledger")
	}
	return 1, nil
}

func summary(counts map[string]int) string {
	var parts []string
	for _, k := range []string{"unchanged", "moved", "changed", "replaced", "dropped"} {
		parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
	}
	return strings.Join(parts, ", ")
}

func quote(s string) string {
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return strconv.Quote(s)
}

// -------------------------------------------------------------------- diff

func runDiff(root, snapPath, ledgerPath string, stat bool, filters []string) error {
	snap, err := loadSnapshot(snapPath)
	if err != nil {
		return err
	}
	rows, _, err := loadLedger(ledgerPath)
	if err != nil {
		return err
	}
	t, err := scan(root, true)
	if err != nil {
		return err
	}
	shown := 0
	for _, d := range snap.Decls {
		r, ok := rows[d.Kind+" "+d.ID]
		if !ok || r.disposition != "moved" || !matches(d.ID, filters) {
			continue
		}
		var bodies, missing []string
		for _, target := range r.targets {
			now, ok := t.decls["decl "+target]
			if !ok {
				now, ok = t.decls["test "+target]
			}
			if !ok {
				missing = append(missing, target)
				continue
			}
			bodies = append(bodies, now.Body)
		}
		shown++
		newBody := strings.Join(bodies, "\n\n")
		header := fmt.Sprintf("%s -> %s", d.ID, strings.Join(r.targets, ", "))
		switch {
		case len(missing) > 0:
			fmt.Printf("%s: target not found: %s\n", header, strings.Join(missing, ", "))
		case newBody == d.Body:
			fmt.Printf("%s: identical\n", header)
		case stat:
			added, removed := lineDelta(d.Body, newBody)
			fmt.Printf("%s: changed (+%d -%d lines)\n", header, added, removed)
		default:
			fmt.Printf("%s: changed\n%s\n", header, unifiedDiff(d.Body, newBody))
		}
	}
	if shown == 0 {
		fmt.Println("no moved declarations match")
	}
	return nil
}

func matches(id string, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, f := range filters {
		if strings.Contains(id, f) {
			return true
		}
	}
	return false
}

// lineDelta counts the lines only in b and the lines only in a.
func lineDelta(a, b string) (added, removed int) {
	count := map[string]int{}
	for _, l := range strings.Split(a, "\n") {
		count[strings.TrimSpace(l)]++
	}
	for _, l := range strings.Split(b, "\n") {
		l = strings.TrimSpace(l)
		if count[l] > 0 {
			count[l]--
		} else {
			added++
		}
	}
	for _, n := range count {
		removed += n
	}
	return added, removed
}

func unifiedDiff(a, b string) string {
	dir, err := ioutil.TempDir("", "refactorcheck")
	if err != nil {
		return "--- old\n" + a + "\n+++ new\n" + b
	}
	defer os.RemoveAll(dir)
	oldFile, newFile := filepath.Join(dir, "old"), filepath.Join(dir, "new")
	ioutil.WriteFile(oldFile, []byte(a+"\n"), 0644)
	ioutil.WriteFile(newFile, []byte(b+"\n"), 0644)
	out, err := exec.Command("diff", "-u", "--label", "old", "--label", "new", oldFile, newFile).Output()
	if len(out) == 0 && err != nil {
		return "--- old\n" + a + "\n+++ new\n" + b // no diff program
	}
	return string(out)
}

// ------------------------------------------------------------------ report

func runReport(snapPath, ledgerPath string) error {
	snap, err := loadSnapshot(snapPath)
	if err != nil {
		return err
	}
	rows, _, err := loadLedger(ledgerPath)
	if err != nil {
		return err
	}
	values := map[string]string{}
	for _, l := range snap.Literals {
		values["lit "+l.Key] = quote(l.Value)
	}
	var ids []string
	for id := range rows {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, disposition := range []string{"dropped", "replaced", "changed", "moved"} {
		title := disposition
		if disposition == "moved" {
			// A moved declaration is the normal case; a literal that only
			// survives in another form is a rewritten query, key or message.
			title = "literals that now exist in another form"
		}
		fmt.Printf("## %s\n\n", title)
		n := 0
		for _, id := range ids {
			r := rows[id]
			if r.disposition != disposition || (disposition == "moved" && r.kind != "lit") {
				continue
			}
			n++
			name := r.key
			if v, ok := values[id]; ok {
				name = v
			}
			line := fmt.Sprintf("- %s `%s`", r.kind, name)
			if len(r.targets) > 0 {
				line += " -> " + strings.Join(r.targets, ", ")
			}
			fmt.Printf("%s: %s\n", line, r.note)
		}
		if n == 0 {
			fmt.Println("(none)")
		}
		fmt.Println()
	}
	return nil
}
