package util

import (
	"sync"
	atomic "sync/atomic"
	"reflect"
	"errors"
)

var ErrorClosed = errors.New("MemBoundCh is closed")
var ErrorSize = errors.New("element size is more than max memory quota")
var ErrorInvSize = errors.New("Max memory quota should always be greater than 0")

type MemBoundCh struct {
	ch	chan interface{}
	size	int64
	maxCount int64
	maxSize	int64
	closed  bool
	mu sync.Mutex
	notfull *sync.Cond
}	

func NewMemBoundCh(count int64, size int64) *MemBoundCh {
	memBoundCh := &MemBoundCh {
		ch : make(chan interface{}, count),
		maxCount: count,
		maxSize: size,
		size : 0,
	}
	memBoundCh.notfull = sync.NewCond(&memBoundCh.mu)
	return memBoundCh
}

func (memBoundCh *MemBoundCh) GetSize() int64 {
	return memBoundCh.size
}

func (memBoundCh *MemBoundCh) GetChannel() chan interface{} {
	return memBoundCh.ch
}

func (memBoundCh *MemBoundCh) IncrSize(size int64) error { 
	if size < 0 {
		return ErrorInvSize//Error
	}
	memBoundCh.maxSize = size
	return nil	
}

// Any read from channel should immediately be followed by DecrSize method
// Else, it will result in a hang 
func (memBoundCh *MemBoundCh) DecrSize(size int64) error {
	for {
		currSize := memBoundCh.GetSize()
		if currSize == 0 {
			return nil
		} else {
			atomic.AddInt64(&memBoundCh.size, -1 * size)
			if memBoundCh.size < 0 {
				return ErrorInvSize
			}
			// New size is less than maxSize and oldSize is greater than maxSize
			if memBoundCh.size < memBoundCh.maxSize && currSize >= memBoundCh.maxSize {
				memBoundCh.notfull.Signal()
			}
			return nil
		}
	}
}

func (memBoundCh *MemBoundCh) Push(elem interface{}) error {
	// Return error if the channel is closed
	if memBoundCh.closed {
		return ErrorClosed
	}

	elemsz := int64(reflect.TypeOf(elem).Size())
	if elemsz > memBoundCh.maxSize {
		return ErrorSize
	}
	for {
		currSize := memBoundCh.GetSize()
		newSize := currSize + elemsz
		if (newSize > memBoundCh.maxSize) {
			// Wait for a not-full notification and retry the loop after the condition is satisfied
			memBoundCh.mu.Lock()
			memBoundCh.notfull.Wait()
			memBoundCh.mu.Unlock()
			continue
		} else {
			// We may exceed the maxSize limit due to AddInt64 but this is only by a margin
			atomic.AddInt64(&memBoundCh.size, elemsz)
			memBoundCh.ch <- elem
			return nil
		}
	}
}

func (memBoundCh *MemBoundCh) close() {
	if !memBoundCh.closed {
		memBoundCh.closed = true
		close(memBoundCh.ch)
	}
}
