package main

import (
	"sort"
	"unicode/utf16"
	"unicode/utf8"
)

// lsp counts characters in utf-16 code units
type mapper struct {
	text  string
	lines []int
}

func newMapper(text string) *mapper {
	m := &mapper{text: text, lines: []int{0}}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			m.lines = append(m.lines, i+1)
		}
	}
	return m
}

func (m *mapper) position(offset int) Position {
	offset = max(0, min(offset, len(m.text)))
	line := sort.Search(len(m.lines), func(i int) bool { return m.lines[i] > offset }) - 1
	col := 0
	for _, r := range m.text[m.lines[line]:offset] {
		col += utf16.RuneLen(r)
	}
	return Position{Line: line, Character: col}
}

func (m *mapper) offset(p Position) int {
	if p.Line < 0 {
		return 0
	}
	if p.Line >= len(m.lines) {
		return len(m.text)
	}
	offset := m.lines[p.Line]
	for col := 0; col < p.Character && offset < len(m.text); {
		r, size := utf8.DecodeRuneInString(m.text[offset:])
		if r == '\n' {
			break
		}
		col += utf16.RuneLen(r)
		offset += size
	}
	return offset
}

func (m *mapper) rangeOf(start, end int) Range {
	return Range{Start: m.position(start), End: m.position(end)}
}
