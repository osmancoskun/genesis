// Command verify runs static analyzers in-process (no os/exec / subprocesses):
// vet-equivalent passes, Staticcheck, and Uber NilAway.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/nilaway"
	nilawayconfig "go.uber.org/nilaway/config"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/analysis/passes/appends"
	"golang.org/x/tools/go/analysis/passes/asmdecl"
	"golang.org/x/tools/go/analysis/passes/assign"
	"golang.org/x/tools/go/analysis/passes/atomic"
	"golang.org/x/tools/go/analysis/passes/bools"
	"golang.org/x/tools/go/analysis/passes/buildtag"
	"golang.org/x/tools/go/analysis/passes/cgocall"
	"golang.org/x/tools/go/analysis/passes/composite"
	"golang.org/x/tools/go/analysis/passes/copylock"
	"golang.org/x/tools/go/analysis/passes/defers"
	"golang.org/x/tools/go/analysis/passes/directive"
	"golang.org/x/tools/go/analysis/passes/errorsas"
	"golang.org/x/tools/go/analysis/passes/framepointer"
	"golang.org/x/tools/go/analysis/passes/httpresponse"
	"golang.org/x/tools/go/analysis/passes/ifaceassert"
	"golang.org/x/tools/go/analysis/passes/loopclosure"
	"golang.org/x/tools/go/analysis/passes/lostcancel"
	"golang.org/x/tools/go/analysis/passes/nilfunc"
	"golang.org/x/tools/go/analysis/passes/printf"
	"golang.org/x/tools/go/analysis/passes/shift"
	"golang.org/x/tools/go/analysis/passes/sigchanyzer"
	"golang.org/x/tools/go/analysis/passes/stdmethods"
	"golang.org/x/tools/go/analysis/passes/stringintconv"
	"golang.org/x/tools/go/analysis/passes/structtag"
	"golang.org/x/tools/go/analysis/passes/testinggoroutine"
	"golang.org/x/tools/go/analysis/passes/tests"
	"golang.org/x/tools/go/analysis/passes/timeformat"
	"golang.org/x/tools/go/analysis/passes/unmarshal"
	"golang.org/x/tools/go/analysis/passes/unreachable"
	"golang.org/x/tools/go/analysis/passes/unsafeptr"
	"golang.org/x/tools/go/analysis/passes/unusedresult"
	"golang.org/x/tools/go/packages"
	"honnef.co/go/tools/staticcheck"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	patterns := splitPackages(strings.Join(args, " "))
	modDir := moduleDir()

	// Limit NilAway to this module's import path (avoids reporting stdlib via tool deps).
	_ = nilawayconfig.Analyzer.Flags.Set(nilawayconfig.IncludePkgsFlag, "dnsredirector")

	checks := []struct {
		name         string
		analyzers    []*analysis.Analyzer
		moduleDiagsOnly bool
	}{
		{"go vet", vetAnalyzers(), false},
		{"staticcheck", staticcheckAnalyzers(), false},
		{"nilaway", []*analysis.Analyzer{nilaway.Analyzer}, true},
	}

	var failed bool
	for _, c := range checks {
		fmt.Printf("==> %s\n", c.name)
		if err := runAnalyzers(c.analyzers, patterns, modDir, c.moduleDiagsOnly); err != nil {
			fmt.Fprintf(os.Stderr, "%s failed: %v\n", c.name, err)
			failed = true
			continue
		}
		fmt.Printf("ok  %s\n", c.name)
	}

	if failed {
		fmt.Fprintln(os.Stderr, "verify: FAILED")
		return 1
	}
	fmt.Println("verify: OK")
	return 0
}

func runAnalyzers(analyzers []*analysis.Analyzer, patterns []string, modDir string, moduleDiagsOnly bool) error {
	if len(analyzers) == 0 {
		return fmt.Errorf("no analyzers")
	}
	cfg := &packages.Config{
		Mode:  packages.LoadAllSyntax,
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return err
	}
	if packages.PrintErrors(pkgs) > 0 {
		return fmt.Errorf("package load errors")
	}
	if len(pkgs) == 0 {
		return fmt.Errorf("no packages matched %v", patterns)
	}

	graph, err := checker.Analyze(analyzers, pkgs, nil)
	if err != nil {
		return err
	}

	var nfail int
	for act := range graph.All() {
		if act.Err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", act, act.Err)
			nfail++
			continue
		}
		if !act.IsRoot {
			continue
		}
		for _, diag := range act.Diagnostics {
			pos := act.Package.Fset.Position(diag.Pos)
			if moduleDiagsOnly && modDir != "" && !fileInDir(pos.Filename, modDir) {
				continue
			}
			fmt.Fprintf(os.Stderr, "%s: %s\n", pos, diag.Message)
			nfail++
		}
	}
	if nfail > 0 {
		return fmt.Errorf("analyzer diagnostics reported")
	}
	return nil
}

func moduleDir() string {
	cfg := &packages.Config{Mode: packages.NeedModule}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil || len(pkgs) == 0 || pkgs[0].Module == nil {
		return ""
	}
	return pkgs[0].Module.Dir
}

func fileInDir(file, dir string) bool {
	file = filepath.Clean(file)
	dir = filepath.Clean(dir)
	return file == dir || strings.HasPrefix(file, dir+string(os.PathSeparator))
}

func vetAnalyzers() []*analysis.Analyzer {
	return []*analysis.Analyzer{
		appends.Analyzer,
		asmdecl.Analyzer,
		assign.Analyzer,
		atomic.Analyzer,
		bools.Analyzer,
		buildtag.Analyzer,
		cgocall.Analyzer,
		composite.Analyzer,
		copylock.Analyzer,
		defers.Analyzer,
		directive.Analyzer,
		errorsas.Analyzer,
		framepointer.Analyzer,
		httpresponse.Analyzer,
		ifaceassert.Analyzer,
		loopclosure.Analyzer,
		lostcancel.Analyzer,
		nilfunc.Analyzer,
		printf.Analyzer,
		shift.Analyzer,
		sigchanyzer.Analyzer,
		stdmethods.Analyzer,
		stringintconv.Analyzer,
		structtag.Analyzer,
		testinggoroutine.Analyzer,
		tests.Analyzer,
		timeformat.Analyzer,
		unmarshal.Analyzer,
		unreachable.Analyzer,
		unsafeptr.Analyzer,
		unusedresult.Analyzer,
	}
}

func staticcheckAnalyzers() []*analysis.Analyzer {
	out := make([]*analysis.Analyzer, 0, len(staticcheck.Analyzers))
	for _, a := range staticcheck.Analyzers {
		out = append(out, a.Analyzer)
	}
	return out
}

func splitPackages(packages string) []string {
	fields := strings.Fields(packages)
	if len(fields) == 0 {
		return []string{"./..."}
	}
	return fields
}
