package main

import "go/token"

func (s *server) definition(snap *snapshot, uri string, offset int) (any, error) {
	id := snap.identAt(offset)
	if id == nil {
		return nil, nil
	}
	obj := snap.objectOf(id)
	if obj == nil || !snap.inFile(obj.Pos()) {
		return nil, nil
	}
	r := snap.rangeOf(obj.Pos(), obj.Pos()+token.Pos(len(obj.Name())))
	return []Location{{URI: uri, Range: r}}, nil
}
