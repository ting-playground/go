package testing

import (
	"encoding/json"
	"os"
	"runtime"
	"sync"
)

type event struct {
	Type runtime.SyncEventType `json:"type"`
	Goid int64                 `json:"goid"`
	File string                `json:"file"`
	Line int                   `json:"line"`
	Now  int64                 `json:"now"`
	Hold int64                 `json:"hold"`
	Addr uintptr               `json:"addr"`
}

type sparsetrace struct {
	globalMtx sync.Mutex
	storage   map[string][][]event
	testCount int
}

func (s *sparsetrace) start() {
	runtime.StTrace.Reset()
	runtime.EnableSyncTracing()

	s.testCount++
}

func (s *sparsetrace) stop(name string) {
	runtime.DisableSyncTracing()
	st := runtime.StTrace.Get()

	var events []event

	for _, e := range st {
		events = append(events, event{
			Type: e.Type,
			Goid: e.Goid,
			File: e.File,
			Line: e.Line,
			Now:  e.Now,
			Hold: e.Hold,
			Addr: uintptr(e.Addr),
		})
	}

	s.storage[name] = append(s.storage[name], events)
}

func (s *sparsetrace) store() {
	if s.storage == nil {
		panic("storage: this shouldn't be nil!")
	}

	data, err := json.Marshal(&s.storage)

	if err != nil {
		panic(err)
	}

	if err = os.WriteFile("record.json", data, 0644); err != nil {
		panic(err)
	}
}
