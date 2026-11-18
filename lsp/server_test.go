package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// request opens src, whose cursor is marked with "|", sends method at the cursor
// and returns the published diagnostics and the decoded result
func request[T any](t *testing.T, src, method string) ([]Diagnostic, T) {
	t.Helper()
	var outbox [][]byte
	s, err := newServer(func(msg []byte) { outbox = append(outbox, msg) })
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(src, "|", "", 1)
	send := func(m map[string]any) {
		m["jsonrpc"] = "2.0"
		data, _ := json.Marshal(m)
		s.handle(data)
	}
	doc := map[string]any{"uri": "file:///main.go", "version": 1, "text": text}
	send(map[string]any{"method": "textDocument/didOpen", "params": map[string]any{"textDocument": doc}})
	if method != "" {
		pos := newMapper(text).position(strings.Index(src, "|"))
		send(map[string]any{"id": 1, "method": method, "params": map[string]any{"textDocument": doc, "position": pos}})
	}

	var diags []Diagnostic
	var result T
	for _, raw := range outbox {
		var msg struct {
			Method string
			Params PublishDiagnosticsParams
			Result json.RawMessage
			Error  *responseError
		}
		json.Unmarshal(raw, &msg)
		switch {
		case msg.Method != "":
			diags = msg.Params.Diagnostics
		case msg.Error != nil:
			t.Fatalf("%s: %s", method, msg.Error.Message)
		default:
			json.Unmarshal(msg.Result, &result)
		}
	}
	return diags, result
}

func messages(diags []Diagnostic) string {
	var msgs []string
	for _, d := range diags {
		msgs = append(msgs, d.Message)
	}
	return strings.Join(msgs, "\n")
}

func TestDiagnostics(t *testing.T) {
	diags, _ := request[any](t, `package main

import (
	"iter"
	"os"
	"strings"
)

func main() {
	x := 1
	_ = strings.Lines
	_ = undefinedThing
	for range 3 {
	}
}
`, "")
	got := messages(diags)
	for _, want := range []string{
		"package iter is not available in GoFiddle",
		`"os" imported and not used`,
		"declared and not used: x",
		"strings.Lines is not available in the GoFiddle interpreter",
		"undefined: undefinedThing",
		"doesn't support ranging over an integer",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestScriptMode(t *testing.T) {
	if diags, _ := request[any](t, "x := 40\nx + 2", ""); len(diags) != 0 {
		t.Errorf("unexpected diagnostics: %s", messages(diags))
	}
	diags, _ := request[any](t, "y := 1\nz := undefinedName", "")
	if len(diags) != 1 || diags[0].Range.Start != (Position{Line: 1, Character: 5}) {
		t.Errorf("wrong diagnostics: %+v", diags)
	}
}

func TestHover(t *testing.T) {
	_, h := request[Hover](t, `package main

import "fmt"

func main() {
	fmt.Print|ln()
}
`, "textDocument/hover")
	for _, want := range []string{"func fmt.Println(a ...any) (n int, err error)", "Println formats", "https://pkg.go.dev/fmt#Println"} {
		if !strings.Contains(h.Contents.Value, want) {
			t.Errorf("hover missing %q:\n%s", want, h.Contents.Value)
		}
	}
}

func TestCompletion(t *testing.T) {
	labels := func(list CompletionList) string {
		var l []string
		for _, it := range list.Items {
			l = append(l, it.Label)
		}
		return " " + strings.Join(l, " ") + " "
	}

	_, list := request[CompletionList](t, "package main\n\nimport \"strings\"\n\nfunc main() {\n\tstrings.|\n}\n", "textDocument/completion")
	if got := labels(list); !strings.Contains(got, " ToUpper ") || strings.Contains(got, " Lines ") {
		t.Errorf("bad package members:%s", got)
	}

	_, list = request[CompletionList](t, `package main

type inner struct{ Depth int }

type point struct {
	inner
	X int
}

func (p *point) Move() {}

func main() {
	var p point
	p.|
}
`, "textDocument/completion")
	if got := labels(list); !strings.Contains(got, " X ") || !strings.Contains(got, " Move ") || !strings.Contains(got, " Depth ") {
		t.Errorf("bad fields and methods:%s", got)
	}

	_, list = request[CompletionList](t, "package main\n\nfunc main() {\n\tcounter := 1\n\t_ = co|\n}\n", "textDocument/completion")
	if got := labels(list); !strings.Contains(got, " counter ") || !strings.Contains(got, " copy ") {
		t.Errorf("bad scope completion:%s", got)
	}
}

func TestSignatureHelp(t *testing.T) {
	_, help := request[SignatureHelp](t, "package main\n\nimport \"strings\"\n\nfunc main() {\n\tstrings.Replace(\"abc\", |\n}\n", "textDocument/signatureHelp")
	if len(help.Signatures) != 1 || help.Signatures[0].Label != "Replace(s string, old string, new string, n int) string" || help.ActiveParameter != 1 {
		t.Errorf("bad signature help: %+v", help)
	}

	_, help = request[SignatureHelp](t, "package main\n\nfunc main() {\n\t_ = append([]int{}, 1, |)\n}\n", "textDocument/signatureHelp")
	if len(help.Signatures) != 1 || help.Signatures[0].Label != "append(slice []Type, elems ...Type) []Type" || help.ActiveParameter != 1 {
		t.Errorf("bad builtin signature help: %+v", help)
	}
}

func TestDefinition(t *testing.T) {
	_, locs := request[[]Location](t, "package main\n\nfunc main() {\n\tvalue := 1\n\t_ = val|ue\n}\n", "textDocument/definition")
	if len(locs) != 1 || locs[0].Range.Start != (Position{Line: 3, Character: 1}) {
		t.Errorf("bad definition: %+v", locs)
	}
}
