package main

import "encoding/json"

type message struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  any              `json:"result,omitempty"`
	Error   *responseError   `json:"error,omitempty"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	codeParseError     = -32700
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeRequestFailed  = -32803
)

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

type DiagnosticSeverity int

const (
	SeverityError   DiagnosticSeverity = 1
	SeverityWarning DiagnosticSeverity = 2
)

const TagUnnecessary = 1

type Diagnostic struct {
	Range    Range              `json:"range"`
	Severity DiagnosticSeverity `json:"severity"`
	Source   string             `json:"source"`
	Message  string             `json:"message"`
	Tags     []int              `json:"tags,omitempty"`
}

type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Version     int          `json:"version"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

type CompletionItemKind int

const (
	KindMethod    CompletionItemKind = 2
	KindFunction  CompletionItemKind = 3
	KindField     CompletionItemKind = 5
	KindVariable  CompletionItemKind = 6
	KindClass     CompletionItemKind = 7
	KindInterface CompletionItemKind = 8
	KindModule    CompletionItemKind = 9
	KindKeyword   CompletionItemKind = 14
	KindConstant  CompletionItemKind = 21
	KindStruct    CompletionItemKind = 22
)

const insertTextFormatSnippet = 2

type Command struct {
	Title   string `json:"title"`
	Command string `json:"command"`
}

type CompletionItem struct {
	Label            string             `json:"label"`
	Kind             CompletionItemKind `json:"kind"`
	Detail           string             `json:"detail,omitempty"`
	Documentation    *MarkupContent     `json:"documentation,omitempty"`
	SortText         string             `json:"sortText,omitempty"`
	InsertTextFormat int                `json:"insertTextFormat,omitempty"`
	TextEdit         *TextEdit          `json:"textEdit,omitempty"`
	Command          *Command           `json:"command,omitempty"`
}

type CompletionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
}

type ParameterInformation struct {
	Label [2]int `json:"label"`
}

type SignatureInformation struct {
	Label         string                 `json:"label"`
	Documentation *MarkupContent         `json:"documentation,omitempty"`
	Parameters    []ParameterInformation `json:"parameters"`
}

type SignatureHelp struct {
	Signatures      []SignatureInformation `json:"signatures"`
	ActiveSignature int                    `json:"activeSignature"`
	ActiveParameter int                    `json:"activeParameter"`
}
