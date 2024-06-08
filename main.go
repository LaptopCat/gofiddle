package main

import (
	"errors"
	"fmt"
	"go/format"
	"reflect"
	"runtime"
	"strings"
	"syscall/js"

	"github.com/klauspost/compress/zstd"
	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

// var i *interp.Interpreter
var window js.Value
var null = js.Null()
var term js.Value
var stdout writer
var encoder, _ = zstd.NewWriter(nil)
var decoder, _ = zstd.NewReader(nil)

// var stdin reader

type writer struct{}

func (writer) Write(p []byte) (n int, err error) {
	term.Call("write", strings.ReplaceAll(string(p), "\n", "\r\n"))
	n = len(p)
	return
}

// broken dont uncomment yet
// type reader struct {
// 	Queue chan byte
// 	Mutex *sync.RWMutex
// }

// func (r reader) Read(p []byte) (n int, err error) {
// 	fmt.Println("called new!", p, len(p))

// 	for i := 0; i < len(p); i++ {
// 		p[i] = <-r.Queue
// 		n++
// 	}

// 	if n == 0 {
// 		err = io.EOF
// 	}

// 	return
// }

func CopyBytesToGo(val js.Value) ([]byte, int) {
	dst := make([]byte, val.Length())
	ln := js.CopyBytesToGo(dst, val)
	return dst, ln
}

func main() {
	window = js.Global()
	term = window.Get("term")
	stdout = writer{}
	// stdin = reader{Mutex: &sync.RWMutex{}, Queue: make(chan byte)}

	// i = NewInterpreter()

	window.Set("ExecPure", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) < 1 {
			return []any{null, null, null}
		}

		val, err := ExecPure(args[0].String())
		if err != nil {
			return []any{"error", fmt.Sprint(err), "err"}
		}

		if !val.IsValid() {
			return []any{"noresult", null, "nil"}
		}

		vl := val.Interface()

		return []any{"result", fmt.Sprintf("%#+v", vl), fmt.Sprintf("%T", vl)}
	}))

	window.Set("FormatCode", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) < 1 {
			return []any{null, null}
		}

		data, err := format.Source([]byte(args[0].String()))
		if err != nil {
			return []any{"error", fmt.Sprint(err)}
		}

		return []any{"result", string(data)}
	}))

	window.Set("GetVersion", js.FuncOf(func(_ js.Value, _ []js.Value) any {
		return runtime.Version()
	}))

	window.Set("Compress", js.FuncOf(func(_ js.Value, args []js.Value) any {
		data, ln := CopyBytesToGo(args[0])
		return js.CopyBytesToJS(args[0], encoder.EncodeAll(data, make([]byte, 0, ln)))
	}))

	// window.Set("Compress2", js.FuncOf(func(_ js.Value, args []js.Value) any {
	// 	data, ln := CopyBytesToGo(args[0])
	// 	return js.CopyBytesToJS(args[1], encoder.EncodeAll(data, make([]byte, 0, ln)))
	// }))

	window.Set("Decompress", js.FuncOf(func(_ js.Value, args []js.Value) any {
		ar, ln := CopyBytesToGo(args[0])
		data, err := decoder.DecodeAll(ar, make([]byte, 0, ln))
		if err != nil {
			return []any{"error", fmt.Sprint(err)}
		}

		return []any{"result", string(data)}
	}))

	// window.Set("InputKey", js.FuncOf(func(_ js.Value, args []js.Value) any {
	// 	if len(args) < 1 {
	// 		return null
	// 	}

	// 	data := []byte(args[0].String())
	// 	for _, b := range data {
	// 		stdin.Queue <- b
	// 	}

	// 	return true
	// }))

	// Never exit
	<-make(chan struct{})
}

func NewInterpreter() *interp.Interpreter {
	i := interp.New(interp.Options{Stdout: stdout, Stderr: stdout})
	i.Use(stdlib.Symbols)
	return i
}

// I plan to use this later (REPL mode)
// func Exec(code string) (reflect.Value, error) {
//  	return i.Eval(code)
// }

func ExecPure(code string) (v reflect.Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			switch x := r.(type) {
			case string:
				err = errors.New(x)
			case error:
				err = x
			default:
				err = errors.New("unknown error (panic during code exection)")
			}
		}
	}()
	v, err = NewInterpreter().Eval(code)
	return
}
