window.executing = false
function Defer(func) {
    if (document.readyState === "interactive") {
        func()
    } else {
        document.addEventListener("DOMContentLoaded", func)
    }
}

require.config({
    paths: {
        vs: "https://cdnjs.cloudflare.com/ajax/libs/monaco-editor/0.35.0/min/vs",
    },
})

require(["vs/editor/editor.main"], () => {
    window.editor = monaco.editor.create(document.getElementById("editor"), {
        fontFamily: "JetBrains Mono",
        language: "go",
        theme: "vs-dark",
        automaticLayout: true,
        cursorBlinking: "smooth",
        smoothScrolling: true,
        fontSize: 13,
        minimap: {
            enabled: false,
        },
    })

    loaded.monaco = true
    monacoAndWasmLoaded()
})

function monacoAndWasmLoaded() {
    if (!loaded.monaco || !loaded.wasm) {
        return
    }

    if (location.hash != "") {
        let comp = base64ToBytes(padBase64(location.hash.slice(1)))
        let result = Decompress(comp)
        if (result[0] === "error") {
            editor.setValue(new TextDecoder().decode(comp))
        } else {
            editor.setValue(result[1])
        }
    } else {
        editor.setValue(`package main

import "fmt"

func main() {
    fmt.Println("Hello, GoFiddle!")
}`)
    }
}


window.term = new Terminal({
    theme: {
        foreground: "#eff0eb",
        background: "#282a36",
        selection: "#97979b33",
        black: "#282a36",
        brightBlack: "#686868",
        red: "#ff5c57",
        brightRed: "#ff5c57",
        green: "#5af78e",
        brightGreen: "#5af78e",
        yellow: "#f3f99d",
        brightYellow: "#f3f99d",
        blue: "#57c7ff",
        brightBlue: "#57c7ff",
        magenta: "#ff6ac1",
        brightMagenta: "#ff6ac1",
        cyan: "#9aedfe",
        brightCyan: "#9aedfe",
        white: "#f1f1f0",
        brightWhite: "#eff0eb",
    },
    fontFamily: "JetBrains Mono",
})
Defer(() => {
    term.open(document.getElementById("terminal"))

    const fitAddon = new FitAddon.FitAddon()
    term.loadAddon(fitAddon)

    fitAddon.fit()

    term.writeln("\x1b[1mLoading interpretor...\x1b[0m\r\n")
})

function exec() {
    if (window.executing) return
    
    window.executing = true

    term.clear()
    term.writeln("\x1b[1mExecuting...\x1b[0m\r\n")

    start = performance.now()
    res = ExecPure(editor.getValue())
    end = performance.now()

    term.writeln("")
    switch (res[0]) {
        case "noresult":
            term.writeln("\x1b[36m<No Result>\x1b[0m")
            break
        case "error":
            term.writeln("\x1b[1mAn error occured!")
            term.writeln("\x1b[31;1m" + res[1] + "\x1b[0m")
            break
        case "result":
            term.writeln("\x1b[1mDone!")
            term.writeln(`Result: ${res[1]} (of type ${res[2]})\x1b[0m`)
            break
    }

    term.writeln(`\x1b[36mTime taken: ${end - start}ms\x1b[0m`)

    window.executing = false
}

function fmt() {
    term.clear()

    start = performance.now()
    res = FormatCode(editor.getValue())
    end = performance.now()
    switch (res[0]) {
        case "error":
            term.writeln("\x1b[1mAn error occured during formatting!")
            term.writeln("\x1b[31;1m" + res[1] + "\x1b[0m")
            break
        case "result":
            term.writeln("\x1b[1mFormatting done!")
            editor.setValue(res[1])
            break
    }

    term.writeln(`\x1b[36mTime taken: ${end - start}ms\x1b[0m`)
}

function padBase64(input) {
    let diff = input.length % 4
    if (!diff) {
        return input
    }

    let padLength = 4 - diff

    while (padLength--) {
        input += "="
    }
    return input
}

function share() {
    term.clear()

    term.writeln("\x1b[1mTrying to compress...\x1b[0m")
    let content = editor.getValue().trim()
    let enc = new TextEncoder().encode(content)
    
    let compLen = Compress(enc)
    let encoded
    if (compLen >= content.length) {
        term.writeln("\x1b[1mCompression ineffective! Falling back to raw content...\x1b[0m")
        encoded = btoa(content)
    } else {
        term.writeln(`\x1b[1mContent succesfully compressed!\x1b[0m`)
        encoded = bytesToBase64(enc.slice(0, compLen))
    }

    let url = new URL(location.href)
    url.hash = encoded.replaceAll("=", "")

    navigator.clipboard.writeText(url.href)
    term.writeln("Copied link to clipboard!")
    term.writeln(url.href)
}

// term.onKey(key => {
//     if (!window.executing) return

//     switch (key.key) {
//         case "\n":
//         case "\r":
//             key.key = "\r\n"
        
//         default:
//             term.write(key.key)
//             InputKey(key.key)
//             break
//     }
// })