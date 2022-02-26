package testing

import (
	"sort"
	"sync"
	"sync/atomic"
)

var WhiteList = &whitelist{
	data: make(map[string]bool),
}

type whitelist struct {
	sync.Mutex
	data map[string]bool
}

func InitWhiteList(names []string) {
	for _, i := range names {
		WhiteList.data[i] = true
	}
}

func (w *whitelist) Lookup(name string) bool {
	w.Lock()
	defer w.Unlock()

	return w.data[name]
}

type testWithIndex struct {
	index int
	test  InternalTest
}

type indexedTests []testWithIndex

func (p indexedTests) Len() int {
	return len(p)
}

func (p indexedTests) Less(i, j int) bool {
	return p[i].index < p[j].index
}

func (p indexedTests) Swap(i, j int) {
	p[i], p[j] = p[j], p[i]
}

func (p indexedTests) List() (result []InternalTest) {
	for _, i := range p {
		result = append(result, i.test)
	}
	return
}

type suspectTests struct {
	smu sync.Mutex

	allTests map[string]testWithIndex
	susTests map[string]testWithIndex

	maxRetry int32
}

func (t *suspectTests) SetMaxRetry(n int32) {
	atomic.StoreInt32(&t.maxRetry, n)
}

func (t *suspectTests) AddSuspect(name string) {
	t.smu.Lock()
	defer t.smu.Unlock()

	t.susTests[name] = t.allTests[name]
}

func (t *suspectTests) AddAllTests(tests []InternalTest) {
	t.smu.Lock()
	defer t.smu.Unlock()

	for i, test := range tests {
		if _, ok := t.allTests[test.Name]; ok {
			panic("repeat test name")
		}

		t.allTests[test.Name] = testWithIndex{
			index: i,
			test:  test,
		}
	}
}

func (t *suspectTests) RemoveSuspect(name string) {
	t.smu.Lock()
	defer t.smu.Unlock()

	delete(t.susTests, name)
}

func (t *suspectTests) NumSuspects() int {
	t.smu.Lock()
	defer t.smu.Unlock()

	return len(t.susTests)
}

func (t *suspectTests) ListSuspects() []InternalTest {
	t.smu.Lock()
	defer t.smu.Unlock()

	var result indexedTests
	for _, test := range t.susTests {
		result = append(result, test)
	}

	sort.Sort(result)

	return result.List()
}

func (t *suspectTests) ListAllTests() []InternalTest {
	t.smu.Lock()
	defer t.smu.Unlock()

	var result indexedTests
	for _, test := range t.allTests {
		result = append(result, test)
	}

	sort.Sort(result)

	return result.List()
}
