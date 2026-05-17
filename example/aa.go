package main

import (
	"fmt"
	"sync"
	"time"
)

type Counter struct {
	mu    sync.Mutex   // 普通互斥锁
	rwm   sync.RWMutex // 读写锁
	value int
}

// 使用 Mutex
func (c *Counter) ReadMutex() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

func (c *Counter) WriteMutex(v int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value = v
}

// 使用 RWMutex
func (c *Counter) ReadRWMutex() int {
	c.rwm.RLock()
	defer c.rwm.RUnlock()
	return c.value
}

func (c *Counter) WriteRWMutex(v int) {
	c.rwm.Lock()
	defer c.rwm.Unlock()
	c.value = v
}

func main() {
	c := &Counter{}

	readCount := 1000
	writeCount := 10

	// 压测 RWMutex
	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(readCount + writeCount)

	// 多读
	for i := 0; i < readCount; i++ {
		go func() {
			defer wg.Done()
			c.ReadRWMutex()
		}()
	}

	// 少写
	for i := 0; i < writeCount; i++ {
		go func(v int) {
			defer wg.Done()
			c.WriteRWMutex(v)
		}(i)
	}

	wg.Wait()
	fmt.Println("RWMutex 耗时:", time.Since(start))

	// 压测 Mutex
	start = time.Now()
	wg.Add(readCount + writeCount)

	for i := 0; i < readCount; i++ {
		go func() {
			defer wg.Done()
			c.ReadMutex()
		}()
	}

	for i := 0; i < writeCount; i++ {
		go func(v int) {
			defer wg.Done()
			c.WriteMutex(v)
		}(i)
	}

	wg.Wait()
	fmt.Println("Mutex 耗时:", time.Since(start))
}
