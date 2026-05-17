# 内存泄漏和内存优化

## 30 秒回答

Go 有 GC，但仍然会发生内存泄漏。Go 里的泄漏通常不是“内存没人释放”，而是对象仍然被引用，GC 无法回收。常见原因包括 Goroutine 泄漏、channel 阻塞、无上限 map/cache、slice 小片引用大数组、timer/ticker 未停止、context 未取消、全局变量长期持有对象。

排查时我会先看内存曲线和 Goroutine 数量，再用 pprof heap、goroutine、allocs 分析对象来源和引用路径。优化方向是控制生命周期、减少分配、复用对象、预分配、限制缓存、及时释放引用。

## Go 中为什么还会内存泄漏

GC 只能回收不可达对象。如果对象仍然被某个变量、全局 map、Goroutine 栈、channel、闭包、timer 引用，GC 就认为它还活着。

所以 Go 内存泄漏的本质通常是：

```text
对象已经没有业务意义，但仍然可达
```

## 常见内存泄漏类型

### 1. Goroutine 泄漏

```go
func wait(ch <-chan int) {
    go func() {
        v := <-ch
        fmt.Println(v)
    }()
}
```

如果没有人发送数据，Goroutine 会永久阻塞。Goroutine 栈和它引用的对象都无法释放。

修复：

```go
func wait(ctx context.Context, ch <-chan int) {
    go func() {
        select {
        case v := <-ch:
            fmt.Println(v)
        case <-ctx.Done():
            return
        }
    }()
}
```

### 2. channel 发送阻塞

```go
func query(ctx context.Context) <-chan Result {
    ch := make(chan Result)
    go func() {
        ch <- slowQuery()
    }()
    return ch
}
```

如果调用方因为超时不再接收，发送方会阻塞。

修复：

```go
func query(ctx context.Context) <-chan Result {
    ch := make(chan Result, 1)
    go func() {
        result := slowQuery()
        select {
        case ch <- result:
        case <-ctx.Done():
        }
    }()
    return ch
}
```

### 3. ticker 未停止

```go
func start() {
    ticker := time.NewTicker(time.Second)
    go func() {
        for range ticker.C {
            work()
        }
    }()
}
```

没有停止条件，ticker 和 Goroutine 都会一直存在。

修复：

```go
func start(ctx context.Context) {
    ticker := time.NewTicker(time.Second)
    go func() {
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                work()
            case <-ctx.Done():
                return
            }
        }
    }()
}
```

### 4. context cancel 未调用

```go
ctx, _ := context.WithTimeout(parent, time.Second)
```

应该：

```go
ctx, cancel := context.WithTimeout(parent, time.Second)
defer cancel()
```

这样能及时释放 timer 等资源。

### 5. 无上限 map/cache

```go
var cache = map[string]Value{}

func Get(key string) Value {
    v := load(key)
    cache[key] = v
    return v
}
```

如果 key 持续增长，cache 会无限变大。

解决：

- 设置容量上限。
- TTL 过期。
- LRU/LFU 淘汰。
- 分片。
- 定期清理。
- 监控 key 数量和内存。

### 6. slice 小片引用大数组

```go
func head() []byte {
    data := make([]byte, 100<<20)
    return data[:10]
}
```

返回的小 slice 引用了整个大数组，导致 100 MB 无法释放。

修复：

```go
func head() []byte {
    data := make([]byte, 100<<20)
    return append([]byte(nil), data[:10]...)
}
```

### 7. 闭包持有大对象

```go
func register(data []byte) {
    http.HandleFunc("/debug", func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprintln(w, len(data))
    })
}
```

闭包一直被 handler 引用，`data` 无法释放。

### 8. 全局变量长期持有对象

全局 map、全局 slice、单例缓存会延长对象生命周期。要明确释放策略。

## 内存优化方向

### 1. 减少分配

```go
result := make([]Item, 0, len(items))
```

预分配可以减少扩容和复制。

### 2. 避免无意义转换

减少 `string` 和 `[]byte` 的频繁互转。

```go
s := string(b)
b2 := []byte(s)
```

在热点路径中要特别关注。

### 3. 复用对象

```go
var pool = sync.Pool{
    New: func() any {
        return make([]byte, 0, 4096)
    },
}
```

适合临时 buffer、编码缓冲等。

### 4. 选择合适的数据结构

例如：

- 小对象连续存储，减少指针。
- 大对象避免频繁拷贝，可以用指针。
- 读多写少场景考虑 copy-on-write。
- 高并发 map 考虑分片。

### 5. 及时释放引用

如果大对象不再使用，可以让引用变为 nil，尤其是长生命周期对象中的字段。

```go
obj.BigBuffer = nil
```

注意：局部变量一般不需要手动置 nil，除非它处于长生命周期作用域或影响峰值内存。

### 6. 控制 Goroutine 生命周期

所有长期运行的 Goroutine 都应该有：

- context。
- done channel。
- channel 关闭策略。
- 超时。
- 明确退出路径。

## 排查方法

### 1. 看整体趋势

先看：

- RSS 是否持续增长。
- HeapAlloc 是否持续增长。
- Goroutine 数量是否持续增长。
- GC 后内存是否下降。

如果 GC 后堆仍持续上升，说明可能有对象长期存活。

### 2. pprof heap

```bash
go tool pprof http://localhost:6060/debug/pprof/heap
```

关注：

- `inuse_space`：当前仍在使用的内存。
- `alloc_space`：累计分配。

泄漏更关注 `inuse_space`。

### 3. pprof goroutine

```bash
go tool pprof http://localhost:6060/debug/pprof/goroutine
```

或访问：

```text
/debug/pprof/goroutine?debug=2
```

查看大量 Goroutine 是否卡在同一位置，例如 channel receive、send、select、锁等待。

### 4. 对比 heap profile

在不同时间点抓两份 heap，看哪些对象持续增长。

```bash
go tool pprof -base old.pb.gz new.pb.gz
```

### 5. benchmark

```bash
go test -bench=. -benchmem
```

关注：

- `B/op`
- `allocs/op`

## 面试回答模板

Go 有 GC，但仍然会内存泄漏。因为只要对象仍然被引用，GC 就不会回收。常见泄漏包括 Goroutine 阻塞不退出、channel 没有接收方、ticker 没停止、context cancel 没调用、map 或缓存无上限增长、slice 小片引用大数组。

排查时我会先看内存和 Goroutine 数量曲线，再用 pprof 的 heap、allocs、goroutine 分析。heap 里重点看 `inuse_space`，goroutine profile 里看是否大量阻塞在同一位置。优化上会控制对象生命周期，给 Goroutine 加 context 和退出条件，缓存设置上限和淘汰策略，热点路径减少分配、预分配 slice、复用 buffer。

## 常见追问

### 1. Go 内存泄漏和 C/C++ 内存泄漏有什么不同？

C/C++ 常见泄漏是分配后忘记释放。Go 中更多是对象仍然被引用，GC 无法回收。表现都是内存持续增长，但原因不同。

### 2. 怎么判断是泄漏还是正常缓存？

看是否有上限、是否符合业务预期、GC 后是否稳定、流量下降后是否回落。如果内存只涨不降，并且对象数量持续增加，就要怀疑泄漏。

### 3. Goroutine 泄漏为什么会导致内存泄漏？

Goroutine 本身有栈和 runtime 结构，而且它的栈上可能引用其他对象。只要 Goroutine 不退出，这些对象就可能一直存活。

### 4. 删除 map key 后内存为什么不一定下降？

map 的 bucket 可能仍然保留，内存不一定马上归还。大量删除后如果需要释放内存，可以重建 map。

