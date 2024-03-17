package main

import (
	"fmt"
	"reflect"
	"strings"
	"syscall/js"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

var i *interp.Interpreter
var window js.Value
var null = js.Null()
var term js.Value
var stdout writer

// var stdin reader

type writer struct{}

func (writer) Write(p []byte) (n int, err error) {
	term.Call("write", strings.ReplaceAll(string(p), "\n", "\r\n"))
	fmt.Println(p, len(p), string(p))
	n = len(p)
	return
}

// type queue struct {
// 	Data []byte
// }

// func (q *queue) Read() (b byte) {
// 	b = q.Data[0]
// 	q.Data = q.Data[1:]
// 	return
// }

// func (q *queue) Push(a []byte) {
// 	q.Data = append(q.Data, a...)
// }

// type reader struct {
// 	Queue *queue
// 	Mutex *sync.RWMutex
// }

// func (r reader) Read(p []byte) (n int, err error) {
// 	r.Mutex.RLock()
// 	defer r.Mutex.RUnlock()

// 	fmt.Println("called new!", p, len(p), r.Queue, len(r.Queue.Data))

// 	if len(r.Queue.Data) == 0 {
// 		return 0, io.EOF
// 	}

// 	for i := 0; i < len(p); i++ {
// 		p[i] = r.Queue.Read()
// 		n++
// 	}

// 	if n == 0 {
// 		err = io.EOF
// 	}
// 	return
// }

// func (r *reader) Push(a []byte) {
// 	r.Mutex.Lock()
// 	defer r.Mutex.Unlock()

// 	r.Queue.Push(a)
// }

func main() {
	window = js.Global()
	term = window.Get("term")
	stdout = writer{}
	//stdin = reader{Mutex: &sync.RWMutex{}, Queue: &queue{}}

	i = NewInterpreter()

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

	// window.Set("InputKey", js.FuncOf(func(_ js.Value, args []js.Value) any {
	// 	if len(args) < 1 {
	// 		return null
	// 	}

	// 	stdin.Push([]byte(args[0].String()))

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

func Exec(code string) (reflect.Value, error) {
	return i.Eval(code)
}

func ExecPure(code string) (reflect.Value, error) {
	return NewInterpreter().Eval(code)
}
