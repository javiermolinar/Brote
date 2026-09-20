package broker

import "fmt"

type dapHandle struct{ backend, generation int }

func (p *dapPeer) translateArguments(args obj, generation int) error {
	for _, key := range []string{"frameId", "variablesReference"} {
		if id := num(args[key]); id > 0 {
			h, ok := p.handles[id]
			if !ok || h.generation != generation {
				return fmt.Errorf("stale %s; refresh the paused stack", key)
			}
			args[key] = h.backend
		}
	}
	return nil
}
func (p *dapPeer) exportHandles(body obj, command string, generation int) {
	if p.handles == nil {
		p.handles = map[int]dapHandle{}
	}
	add := func(o obj, key string) {
		if id := num(o[key]); id > 0 {
			p.nextHandle++
			p.handles[p.nextHandle] = dapHandle{id, generation}
			o[key] = p.nextHandle
		}
	}
	if command == "stackTrace" {
		for _, f := range asList(body["stackFrames"]) {
			add(asObj(f), "id")
		}
	}
	add(body, "variablesReference")
	for _, key := range []string{"scopes", "variables"} {
		for _, v := range asList(body[key]) {
			add(asObj(v), "variablesReference")
		}
	}
}
