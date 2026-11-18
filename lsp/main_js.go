//go:build js && wasm

package main

import "syscall/js"

func main() {
	global := js.Global()
	srv, err := newServer(func(msg []byte) {
		global.Call("postMessage", string(msg))
	})
	if err != nil {
		global.Get("console").Call("error", err.Error())
		return
	}

	global.Set("lspReceive", js.FuncOf(func(_ js.Value, args []js.Value) any {
		srv.handle([]byte(args[0].String()))
		return nil
	}))
	global.Call("lspReady")

	select {}
}
