function startLanguageServer(editor) {
    const worker = new Worker("assets/lsp-worker.js")
    const model = editor.getModel()
    const textDocument = { uri: model.uri.toString() }

    let nextId = 1
    const pending = new Map()
    const send = (msg) => worker.postMessage(JSON.stringify({ jsonrpc: "2.0", ...msg }))

    worker.onmessage = (e) => {
        const msg = JSON.parse(e.data)
        if (msg.method === "textDocument/publishDiagnostics") {
            showDiagnostics(msg.params)
        } else if (pending.has(msg.id)) {
            pending.get(msg.id)(msg.result)
            pending.delete(msg.id)
        }
    }

    const toRange = (r) =>
        new monaco.Range(r.start.line + 1, r.start.character + 1, r.end.line + 1, r.end.character + 1)
    const request = (method, position) =>
        new Promise((resolve) => {
            const id = nextId++
            pending.set(id, resolve)
            send({ id, method, params: { textDocument, position: { line: position.lineNumber - 1, character: position.column - 1 } } })
        })

    function showDiagnostics({ version, diagnostics }) {
        if (version !== model.getVersionId()) {
            return // stale, newer diagnostics are on their way
        }
        const markers = diagnostics.map((d) => ({
            ...toRange(d.range),
            severity: d.severity === 1 ? monaco.MarkerSeverity.Error : monaco.MarkerSeverity.Warning,
            message: d.message,
            source: d.source,
            tags: d.tags,
        }))
        monaco.editor.setModelMarkers(model, "gofiddle-lsp", markers)
    }

    const sync = (method) =>
        send({ method, params: { textDocument: { ...textDocument, version: model.getVersionId(), text: model.getValue() }, contentChanges: [{ text: model.getValue() }] } })
    sync("textDocument/didOpen")
    model.onDidChangeContent(() => sync("textDocument/didChange"))

    const K = monaco.languages.CompletionItemKind
    const kinds = { 2: K.Method, 3: K.Function, 5: K.Field, 6: K.Variable, 7: K.Class, 8: K.Interface, 9: K.Module, 14: K.Keyword, 21: K.Constant, 22: K.Struct }

    monaco.languages.registerCompletionItemProvider("go", {
        triggerCharacters: ["."],
        async provideCompletionItems(m, position) {
            const list = await request("textDocument/completion", position)
            return {
                suggestions: (list?.items || []).map((item) => ({
                    label: item.label,
                    kind: kinds[item.kind] ?? K.Text,
                    detail: item.detail,
                    documentation: item.documentation && { value: item.documentation.value },
                    sortText: item.sortText,
                    insertText: item.textEdit.newText,
                    insertTextRules: item.insertTextFormat === 2 ? monaco.languages.CompletionItemInsertTextRule.InsertAsSnippet : undefined,
                    range: toRange(item.textEdit.range),
                    command: item.command && { id: item.command.command, title: item.command.title },
                })),
            }
        },
    })

    monaco.languages.registerHoverProvider("go", {
        async provideHover(m, position) {
            const hover = await request("textDocument/hover", position)
            return hover && { contents: [{ value: hover.contents.value }], range: toRange(hover.range) }
        },
    })

    monaco.languages.registerSignatureHelpProvider("go", {
        signatureHelpTriggerCharacters: ["(", ","],
        async provideSignatureHelp(m, position) {
            const help = await request("textDocument/signatureHelp", position)
            if (!help?.signatures.length) {
                return null
            }
            const signatures = help.signatures.map((s) => ({ ...s, documentation: s.documentation && { value: s.documentation.value } }))
            return { value: { ...help, signatures }, dispose() {} }
        },
    })

    monaco.languages.registerDefinitionProvider("go", {
        async provideDefinition(m, position) {
            const locations = await request("textDocument/definition", position)
            return (locations || []).map((l) => ({ uri: model.uri, range: toRange(l.range) }))
        },
    })
}
