package common

import (
	"errors"
	"sync"
	atomic "sync/atomic"
)

var ErrorChClosed = errors.New("MemBoundCh is closed")
var ErrorSize = errors.New("Element size is more than max allowed channel size")
var ErrorInvSize = errors.New("Max allowed channel size should always be greater than 0")

type MemBoundCh struct {
	ch       chan interface{}
	size     int64
	maxSize  int64
	closed   int64
	mu       sync.Mutex
	notfull  *sync.Cond
	waitFull int64
}

func NewMemBoundCh(count int64, size int64) *MemBoundCh {
	memBoundCh := &MemBoundCh{
		ch:       make(chan interface{}, count),
		maxSize:  size,
		size:     0,
		waitFull: 0,
		closed:   0,
	}
	memBoundCh.notfull = sync.NewCond(&memBoundCh.mu)
	return memBoundCh
}

func (memBoundCh *MemBoundCh) GetSize() int64 {
	return atomic.LoadInt64(&memBoundCh.size)
}

func (memBoundCh *MemBoundCh) GetChannel() chan interface{} {
	return memBoundCh.ch
}

func (memBoundCh *MemBoundCh) SetMaxSize(size int64) error {
	if size < 0 {
		return ErrorInvSize //Error
	}
	atomic.StoreInt64(&memBoundCh.maxSize, size)
	return nil
}

func (memBoundCh *MemBoundCh) GetMaxSize() int64 {
	return atomic.LoadInt64(&memBoundCh.maxSize)
}

// Any read from channel should immediately be followed by DecrSize method
// Else, it will result in a hang
func (memBoundCh *MemBoundCh) DecrSize(size int64) error {
	currSize := memBoundCh.GetSize()
	if currSize == 0 {
		return nil
	} else {
		atomic.AddInt64(&memBoundCh.size, 0-size)
		if atomic.LoadInt64(&memBoundCh.size) < 0 {
			return ErrorInvSize
		}
		// New size is less than maxSize. Signal all threads that are waiting
		if atomic.LoadInt64(&memBoundCh.waitFull) > 0 && atomic.LoadInt64(&memBoundCh.size) < atomic.LoadInt64(&memBoundCh.maxSize) {
			// Using Signal() instead of Broadcast() here might be unfair in some cases
			// E.g., Signal() wakes up only one go-routine which might require more memory than
			// freed and therefore sleeps again. Other go-routines keep waiting even though
			// there is space in the memBoundCh. Using Broadcast() will solve this problem but
			// Broadcast is costly operation. Hence, going with Signal()
			memBoundCh.notfull.Signal()
			atomic.AddInt64(&memBoundCh.waitFull, -1)
		}
		return nil
	}
}

func (memBoundCh *MemBoundCh) Push(elem interface{}, elemsz int64) error {
	for {
		// Return error is the element size is greater than the max configured size
		if elemsz > memBoundCh.GetMaxSize()  {
			return ErrorSize
		}

		// Return error if the channel is closed
		if atomic.LoadInt64(&memBoundCh.closed) != 0 {
			return ErrorChClosed
		}

		currSize := memBoundCh.GetSize()
		newSize := currSize + elemsz
		if newSize > memBoundCh.GetMaxSize() {
			// Wait for a not-full notification and retry the loop after the condition is satisfied
			memBoundCh.mu.Lock()
			atomic.AddInt64(&memBoundCh.waitFull, 1)
			memBoundCh.notfull.Wait()
			memBoundCh.mu.Unlock()
			continue
		} else {
			// atomic.AddInt64 is used instead of CAS as CAS is a costly operation.
			// Because of this, we may exceed the maxSize limit but this is only by a margin
			// The margin depends on the number of outstanding requests by all the threads and
			// the corresponding element sizes at the point in time where we might cross the
			// memory limit
			atomic.AddInt64(&memBoundCh.size, elemsz)
			memBoundCh.ch <- elem
			return nil
		}
	}
}

func (memBoundCh *MemBoundCh) Close() {
	if atomic.LoadInt64(&memBoundCh.closed) == 0 {
		atomic.StoreInt64(&memBoundCh.closed, 1)
		// Signal all waiting threads that the channels is closed
		memBoundCh.notfull.Broadcast()
		atomic.StoreInt64(&memBoundCh.waitFull, 0)
		close(memBoundCh.ch)
	}
}
