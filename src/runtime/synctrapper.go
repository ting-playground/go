package runtime

import (
	"runtime/internal/atomic"
	"unsafe"
)

var SyncTraceEnable bool

func syncTraceEnabled() bool {
	s := gogetenv("SYNCTRAPPER_TRACE")
	if s == "0" {
		return false
	}
	if s == "1" {
		return true
	}

	return false
}

var StTrace = &stTrace{
	traces: make(map[int64][]StTraceEvent),
}

type SyncEventType int

const (
	LockEvent SyncEventType = iota
	UnlockEvent
	WLockEvent
	WUnlockEvent
	RLockEvent
	RUnlockEvent
	ChanSend1Event
	ChanRecv2Event
	ChanRecv1Envet
	ChanCloseEvent
	ChanReflectSendEvent
	ChanReflectRecvEvent
	ChanReflectCloseEvent
	SelectSendEvent
	SelectBufSendEvent
	SelectBufRecvEvent
	SelectCloseRecvEvent
	SelectNbSendEvent
	SelectRecvEvent
	SelectNbRecvEvent
	SelectDefaultEvent
	WGWaitEvent
	WGDoneEvent
	CondWaitEvent
	CondSignalEvent
	CondBroadcastEvent
	NewProcEvent
	MainFuncStartEvent
	ChanMakeEvent
	ChanMake64Event
	ChanReflectMakeEvent
)

type StTraceEvent struct {
	Goid int64
	Now  int64
	Type int
	Addr unsafe.Pointer
	File string
	Line int
}

func (s StTraceEvent) IsType(t SyncEventType) bool {
	return s.Type == int(t)
}

type stTrace struct {
	mu     mutex
	traces map[int64][]StTraceEvent
}

func (s *stTrace) lock() {
	lock(&s.mu)
	if raceenabled {
		raceacquire(unsafe.Pointer(&s.mu))
	}
}

func (s *stTrace) unlock() {
	if raceenabled {
		racerelease(unsafe.Pointer(&s.mu))
	}
	unlock(&s.mu)
}

func (s *stTrace) Reset() {
	s.lock()
	s.traces = make(map[int64][]StTraceEvent)
	s.unlock()
}

func (s *stTrace) Get() [][]StTraceEvent {
	var ret [][]StTraceEvent
	s.lock()
	for _, event := range s.traces {
		ret = append(ret, event)
	}
	s.unlock()

	return ret
}

func (s *stTrace) append(id int64, event StTraceEvent) {
	s.lock()
	s.traces[id] = append(s.traces[id], event)
	s.unlock()
}

//go:linkname MarkEvent sync.runtime_MarkEvent
func MarkEvent(addr unsafe.Pointer, goid int64, event int, skip int) {
	if !SyncTraceEnable {
		return
	}

	if goid == 0 {
		goid = getg().goid
	}

	var file string
	var line int

	if event != int(NewProcEvent) {
		_, file, line, _ = Caller(skip)

		filegoexit := "asm_amd64.s"
		szfile := len(file)
		szexit := len(filegoexit)
		if szfile > szexit && file[szfile-szexit:] == filegoexit {
			_, file, line, _ = Caller(skip + 1)
		}
	} else {
		rpc := make([]uintptr, 1)
		n := gcallers((*g)(addr), 2, rpc)
		if n >= 1 {
			frame, _ := CallersFrames(rpc).Next()
			file, line = frame.File, frame.Line
		}
	}

	StTrace.append(goid, StTraceEvent{
		Goid: goid,
		Now:  nanotime(),
		Type: event,
		Addr: addr,
		File: file,
		Line: line,
	})
}

type syncTrapperMap struct {
	lock mutex
	data map[*hchan]int64

	enable bool
	ch     chan SyncSignal
}

func (s *syncTrapperMap) Lock() {
	lock(&s.lock)
	if raceenabled {
		raceacquire(unsafe.Pointer(&s.lock))
	}
}

func (s *syncTrapperMap) Unlock() {
	if raceenabled {
		racerelease(unsafe.Pointer(&s.lock))
	}
	unlock(&s.lock)
}

func (s *syncTrapperMap) Store(c *hchan, id int64) {
	s.Lock()
	s.data[c] = id
	s.Unlock()
}

func (s *syncTrapperMap) Load(c *hchan) int64 {
	s.Lock()
	id, exists := s.data[c]
	s.Unlock()
	if exists {
		return id
	}
	return -1
}

//go:norace
func (s *syncTrapperMap) IsEnabled() bool {
	return s.enable
}

//go:norace
func (s *syncTrapperMap) Enable() {
	s.enable = true
}

//go:norace
func (s *syncTrapperMap) Disable() {
	s.enable = false
}

//go:norace
func (s *syncTrapperMap) Clear() {
	s.Lock()
	s.data = make(map[*hchan]int64)
	s.ch = make(chan SyncSignal)
	s.Unlock()
}

func (s *syncTrapperMap) Queued(id int64, isWakedUp *uint32) {
	s.ch <- SyncSignal{ID: id, IsWakedUp: isWakedUp}
}

func (s *syncTrapperMap) Notify() chan SyncSignal {
	return s.ch
}

type SyncSignal struct {
	ID        int64
	IsWakedUp *uint32
}

var SyncTrapperMap *syncTrapperMap = &syncTrapperMap{
	data: make(map[*hchan]int64),
	ch:   make(chan SyncSignal),
}

func waitSched(c *hchan) {
	id := SyncTrapperMap.Load(c)
	if id == -1 {
		return
	}

	isWakeUp := new(uint32)
	SyncTrapperMap.Queued(id, isWakeUp)
	for atomic.Load(isWakeUp) != 1 {
		timeSleep(1000000)
	}
}
