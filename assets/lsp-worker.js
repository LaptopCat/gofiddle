// messages are lsp json-rpc payloads without headers
importScripts("wasm_exec.js")

const queue = []

// replaced by the server once it's running
self.lspReceive = (msg) => queue.push(msg)
self.lspReady = () => {
    for (const msg of queue.splice(0)) {
        self.lspReceive(msg)
    }
}

self.onmessage = (e) => self.lspReceive(e.data)

const go = new Go()
WebAssembly.instantiateStreaming(fetch("../lsp.wasm"), go.importObject)
    .then((result) => go.run(result.instance))
    .catch((err) => {
        console.error("Failed to start the language server:", err)
        self.close()
    })
