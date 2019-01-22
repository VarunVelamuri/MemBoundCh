package util

import "testing"
import "unsafe"
import "time"
import "fmt"

func TestMemBoundCh(t *testing.T) {
	// 1000 elements, 128 bytes
	memBoundCh := NewMemBoundCh(1000, 128)
	a := "12345678"
	var count int32
	num := 1000
	go func() {
		for {
			select {
				case  <-memBoundCh.ch:
					memBoundCh.DecrSize(int64(unsafe.Sizeof(a)))
					count++
			}
		}
	}()

	for i:=0; i< num; i++ {
		memBoundCh.Push(a)
	}
	

	time.Sleep(10 * time.Second)
	// Read the remaining elements from channel as channel is closed
	if count != int32(num) {
		fmt.Printf("Error, expected count: %v, actual count: %v\n", num, count)
		fmt.Printf("Error, expected count: %v, actual count: %v", memBoundCh.GetSize(), count)
		t.Error()
	}
}
