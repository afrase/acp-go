package acp_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The README's Go programs build: each block that declares something is
// built as a program of its own, with the imports it uses, against this
// module.
func TestTheREADMEsCodeBuilds(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if os.IsNotExist(err) {
		t.Skip("no README here (this is a copy of the test in another package)")
	}
	if err != nil {
		t.Fatal(err)
	}
	if testing.Short() {
		t.Skip("runs the go command")
	}
	goCmd, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command")
	}
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	gomod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	module := regexp.MustCompile(`(?m)^module (\S+)`).FindSubmatch(gomod)
	if module == nil {
		t.Fatal("no module line in go.mod")
	}
	imports := map[string]string{"acp": string(module[1]), "context": "context", "exec": "os/exec", "fmt": "fmt", "os": "os", "time": "time"}
	built := 0
	for _, block := range regexp.MustCompile("(?s)```go\n(.*?)```").FindAllStringSubmatch(string(readme), -1) {
		code := block[1]
		if !strings.HasPrefix(code, "type ") && !strings.HasPrefix(code, "func ") {
			continue // a fragment
		}
		var src strings.Builder
		src.WriteString("package main\n\nimport (\n")
		for name, path := range imports {
			if regexp.MustCompile(`\b` + name + `\.`).MatchString(code) {
				src.WriteString("\t" + name + " \"" + path + "\"\n")
			}
		}
		src.WriteString(")\n\n" + code)
		if !strings.Contains(code, "func main()") {
			src.WriteString("\nfunc main() {}\n")
		}
		dir := t.TempDir()
		mod := "module readme\n\ngo 1.25\n\nrequire " + imports["acp"] + " v0.0.0\n\nreplace " + imports["acp"] + " => " + filepath.ToSlash(root) + "\n"
		for name, content := range map[string]string{"go.mod": mod, "main.go": src.String()} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.CommandContext(t.Context(), goCmd, "build", "-o", os.DevNull, ".")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOFLAGS=-mod=mod")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("the README's block\n%s\ndoesn't build: %v\n%s", code, err, out)
		}
		built++
	}
	if built < 2 {
		t.Errorf("built %d of the README's blocks, want the client and the agent", built)
	}
}
