package main

import (
	"encoding/json"
	"fmt"
)

type document struct {
	version int
	text    string
	snap    *snapshot // nil until analyzed
}

type server struct {
	std  *stdlib
	docs map[string]*document
	send func(msg []byte)
}

func newServer(send func([]byte)) (*server, error) {
	std, err := loadStdlib()
	if err != nil {
		return nil, fmt.Errorf("loading standard library data: %w", err)
	}
	return &server{std: std, docs: map[string]*document{}, send: send}, nil
}

var requests = map[string]func(s *server, snap *snapshot, uri string, offset int) (any, error){
	"textDocument/hover":         (*server).hover,
	"textDocument/completion":    (*server).completion,
	"textDocument/signatureHelp": (*server).signatureHelp,
	"textDocument/definition":    (*server).definition,
}

var capabilities = map[string]any{
	"textDocumentSync":      1,
	"hoverProvider":         true,
	"completionProvider":    map[string]any{"triggerCharacters": []string{"."}},
	"signatureHelpProvider": map[string]any{"triggerCharacters": []string{"(", ","}},
	"definitionProvider":    true,
}

func (s *server) handle(data []byte) {
	var msg message
	if err := json.Unmarshal(data, &msg); err != nil {
		s.reply(nil, nil, &responseError{Code: codeParseError, Message: err.Error()})
		return
	}
	defer func() {
		if r := recover(); r != nil && msg.ID != nil {
			s.reply(msg.ID, nil, &responseError{Code: codeRequestFailed, Message: fmt.Sprint("internal error: ", r)})
		}
	}()

	var p struct {
		TextDocument struct {
			URI     string `json:"uri"`
			Version int    `json:"version"`
			Text    string `json:"text"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Text string `json:"text"`
		} `json:"contentChanges"`
		Position Position `json:"position"`
	}
	if err := json.Unmarshal(msg.Params, &p); err != nil && msg.Params != nil {
		s.reply(msg.ID, nil, &responseError{Code: codeInvalidParams, Message: err.Error()})
		return
	}
	uri := p.TextDocument.URI

	switch msg.Method {
	case "initialize":
		s.reply(msg.ID, map[string]any{"capabilities": capabilities}, nil)
	case "textDocument/didOpen":
		s.docs[uri] = &document{version: p.TextDocument.Version, text: p.TextDocument.Text}
		s.publishDiagnostics(uri)
	case "textDocument/didChange":
		if doc, ok := s.docs[uri]; ok && len(p.ContentChanges) > 0 {
			*doc = document{version: p.TextDocument.Version, text: p.ContentChanges[len(p.ContentChanges)-1].Text}
			s.publishDiagnostics(uri)
		}
	case "textDocument/didClose":
		delete(s.docs, uri)
	default:
		fn, ok := requests[msg.Method]
		if msg.ID == nil {
			return
		}
		doc, known := s.docs[uri]
		if !ok || !known {
			s.reply(msg.ID, nil, &responseError{Code: codeMethodNotFound, Message: "unsupported request " + msg.Method})
			return
		}
		snap := s.snapshot(doc)
		result, err := fn(s, snap, uri, snap.m.offset(p.Position))
		if err != nil {
			s.reply(msg.ID, nil, &responseError{Code: codeRequestFailed, Message: err.Error()})
			return
		}
		s.reply(msg.ID, result, nil)
	}
}

func (s *server) reply(id *json.RawMessage, result any, rerr *responseError) {
	msg := message{JSONRPC: "2.0", ID: id, Error: rerr}
	if rerr == nil {
		// a null result must still be sent explicitly
		raw, _ := json.Marshal(result)
		msg.Result = json.RawMessage(raw)
	}
	data, _ := json.Marshal(msg)
	s.send(data)
}

func (s *server) snapshot(doc *document) *snapshot {
	if doc.snap == nil {
		doc.snap = s.analyze(doc.text)
	}
	return doc.snap
}

func (s *server) publishDiagnostics(uri string) {
	doc := s.docs[uri]
	params, _ := json.Marshal(PublishDiagnosticsParams{URI: uri, Version: doc.version, Diagnostics: s.diagnostics(s.snapshot(doc))})
	data, _ := json.Marshal(message{JSONRPC: "2.0", Method: "textDocument/publishDiagnostics", Params: params})
	s.send(data)
}
