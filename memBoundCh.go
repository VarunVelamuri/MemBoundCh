//*********************************************
// MemBoundCh is a wrapper around golang channels
//
// MemBoundCh blocks when the total size of all elements (in bytes) in the
// underlying channel exceeds the configured size (or) when the number
// of elements in the underlying channel exceeds the configured count
//
// MemBoundCh is currently optimized for
//	a. Single producer, single consumer scenarios
//	b. Single producer, multiple consumer scenarios
//
// In multiple producer, single consumer scenarios, it can happen that
// consumer signals a producer but producer falls back to wait if the size
// of element it needs to push is greater than available size (Because of using
// signal() instead of broadcast()). This might induce temporary blocking on the
// producer's side as they keep waiting even when some space is available.
// This is a temporary phenomemon as the data in underlying channel gets consumed
// eventually and signal() does not wake-up the same producer always (i.e. giving
// chance for other producers to do a Push() on the channel)
//
// It is the responsibility of the caller to handle
//	a. ErrorChClosed - The channel is closed. The caller should either refrain from
//			   pushing elements into the channel (or) create a new MemBoundCh,
//			   consume all the elements remaining in old channel, push them to
//			   new MemBoundCh and then continue to push new elements. The 
//			   creation of new MemBoundCh has to be lock protected as the caller
//			   can simultanesouly handle "ErrorInvSize" simultaneously in a 
//			   separate thread
//	b. ErrorSize - The size of the element that is being pushed is greater than
//		       the maximum size the channel can hold. Increase the MemBoundCh
//		       maxSize to handle this error
//	c. ErrorInvMaxSize - The total size of all the elements in MemBoundCh should not
//			     be configured less than "0". Increase the MemBoundCh maxSize
//			     to handle this error
//	d. ErrorInvSize - This can happens when the DecrSize() method is called more than once
//			  for the same element (If this situation arises, then it means that the
//			  code has a bug). As a recovery, the caller can close the existing 
//			  MemBoundCh, create a new MemBoundCh, consume all the elements remaining
//			  in old channel, push them to new MemBoundCh. The creation of new
//			  MemBoundCh has to be lock protected as the caller can simultaneously 
//			  handle "ErrorChClosed" in a separate thread
//
//*********************************************
package common

import (
	"errors"
	"sync"
	atomic "sync/atomic"
)

var ErrorChClosed = errors.New("MemBoundCh is closed")
var ErrorSize = errors.New("Element size is more than max allowed channel size")
var ErrorInvMaxSize = errors.New("Max allowed channel size should always be greater than 0")
var ErrorInvSize = errors.New("Total size of all the elements can not go below zero")

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
		return ErrorInvMaxSize //Error
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
		if memBoundCh.GetSize() < 0 {
			return ErrorInvSize
		}
		// New size is less than maxSize. Signal all threads that are waiting
		// Due to AddInt64 while doing Push, the memBoundCh.size can go beyond 
		// memBoundCh.maxSize. Hence, signal only if the size comes down below 
		// memBoundCh.maxSize
		if atomic.LoadInt64(&memBoundCh.waitFull) > 0 && atomic.LoadInt64(&memBoundCh.size) < memBoundCh.GetMaxSize() {
			// Using Signal() instead of Broadcast() here might be unfair in some cases
			// E.g., Signal() wakes up only one go-routine which might require more memory than
			// freed and therefore sleeps again. Other go-routines keep waiting even though
			// there is space in the memBoundCh. Using Broadcast() will solve this problem but
			// Broadcast is costly operation. This producer blocking is a temporary phenomenon
			// as the data in underlying channel gets consumed eventually and signal() does not
			// wake-up the same producer always (i.e. giving chance for other producers to do a 
			// Push() on the channel)
			memBoundCh.notfull.Signal()
			atomic.AddInt64(&memBoundCh.waitFull, -1)
		}
		return nil
	}
}

func (memBoundCh *MemBoundCh) Push(elem interface{}, elemsz int64) error {
	for {
		// Return error is the element size is greater than the max configured size
		if elemsz > memBoundCh.GetMaxSize() {
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
	// Only one thread should succeed in closing the channels
	if atomic.CompareAndSwapInt64(&memBoundCh.closed, 0, 1) {
		// Signal all waiting threads that the channels is closed
		memBoundCh.notfull.Broadcast()
		atomic.StoreInt64(&memBoundCh.waitFull, 0)
		close(memBoundCh.ch)
	}
}
