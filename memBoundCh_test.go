package common

import (
	"fmt"
	"testing"
	"time"
	"unsafe"
)

//This test should be improved further
func TestMemBoundCh(t *testing.T) {
	// 1000 elements, 128 bytes
	memBoundCh := NewMemBoundCh(1000, 128)
	a := "12345678"
	var count int
	num := 1000

	go func() {
		for {
			// Receiving from memBoundCh is same as receiving from normal channels
			// After receiving, we need to call the DecrSize method
			select {
			case <-memBoundCh.ch:
				memBoundCh.DecrSize(int64(unsafe.Sizeof(a)))
				count++
			}
		}
	}()

	go func() {
		for i := 0; i < num; i++ {
			memBoundCh.Push(a, int64(unsafe.Sizeof(a)))
		}

	}()

	time.Sleep(10 * time.Second)
	memBoundCh.Close()
	// Read the remaining elements from channel as channel is closed
	for _ = range memBoundCh.ch {
		count++
	}

	if count != num {
		fmt.Printf("Error, expected count: %v, actual count: %v\n", num, count)
		t.Error()
	}
}
