# Go 核心高频强化版：并发、Channel、GC、Slice、Context

这篇是前面 Go 基础专题的压缩强化版，适合面试前集中复习。重点覆盖高级面试官常追问的细节：

- GMP 调度和 Goroutine 生命周期。
- Channel 底层 `hchan`、阻塞队列和 panic 规则。
- GC 三色标记、混合写屏障、逃逸分析和调优。
- Slice 底层结构、扩容和参数传递陷阱。
- Context 超时、取消和树形传播。

说明：本机 Go 版本为 `go1.26.1`。slice 扩容阈值已不是老面试资料里常说的 `1024`，当前 runtime 的 `nextslicecap` 使用 `256` 作为平滑过渡阈值，小 slice 倾向 2 倍增长，大 slice 逐步过渡到约 1.25 倍增长，最终容量还会受内存分配规格影响。所以面试时建议说“不要依赖精确扩容倍数”，同时补充传统说法和现代实现差异。

## 1. 并发编程与 GMP 调度模型

### 高频题

- Goroutine 与操作系统线程的区别？
- GMP 模型如何工作？
- 如何检测和定位 Goroutine 泄漏？
- 并发与并行的区别？
- Goroutine 发生阻塞系统调用时，Go runtime 如何处理？
- G 的生命周期如何流转？

## 30 秒回答

Goroutine 是 Go runtime 管理的轻量级执行单元，OS 线程由操作系统调度。Go 通过 GMP 模型把大量 Goroutine 调度到少量 OS 线程上执行。G 是 Goroutine，M 是 OS 线程，P 是执行 Go 代码需要的调度上下文，M 必须绑定 P 才能运行 G。

每个 P 有本地 runnable 队列，空闲 P 会从其他 P 窃取任务，也就是 work stealing。`GOMAXPROCS` 决定 P 的数量，也就是同一时刻最多有多少个 M 并行执行 Go 代码。Goroutine 进入阻塞 syscall 时，M 可能被阻塞，runtime 会把 P 从 M 上解绑并移交给其他 M，避免阻塞整个调度器。

并发是同时处理多个任务的能力，并行是同一时刻多个任务真正运行在多个 CPU 核上。Goroutine 泄漏通常通过 pprof goroutine、runtime metrics、日志和压测观察定位。

## Goroutine 与线程的区别

| 对比项 | Goroutine | OS 线程 |
| --- | --- | --- |
| 管理者 | Go runtime | 操作系统内核 |
| 调度位置 | 用户态调度为主 | 内核调度 |
| 初始栈 | 小，按需增长 | 通常较大 |
| 创建成本 | 低 | 高 |
| 切换成本 | 低 | 较高 |
| 数量级 | 可以大量创建，但不是无限免费 | 数量受系统资源限制明显 |
| 阻塞处理 | runtime 能感知多数阻塞并调度其他 G | 线程阻塞就是 OS 线程阻塞 |

## GMP 如何工作

```text
G: Goroutine
M: Machine，OS 线程
P: Processor，调度上下文

G -> P 的本地队列
M 绑定 P
M 从 P 取 G 执行
```

调度流程：

1. `go func()` 创建一个 G。
2. G 优先放入当前 P 的本地队列。
3. M 绑定 P，从 P 的本地队列取 G 执行。
4. 本地队列为空时，尝试从全局队列取。
5. 全局队列也为空时，从其他 P 的本地队列偷一部分 G。
6. 仍然没有任务时，检查 netpoller 是否有就绪网络事件。
7. 没任务时 M 休眠或短暂自旋。

## GOMAXPROCS 的影响

`GOMAXPROCS` 控制 P 的数量。

```go
runtime.GOMAXPROCS(4)
```

含义：

- 不是 Goroutine 数量上限。
- 不是 OS 线程数量上限。
- 是 Go 代码并行执行的最大宽度。

CPU 密集型任务通常设置接近 CPU 核数。I/O 密集型任务瓶颈往往在下游服务、连接池、网络、锁和队列，不是单纯调大 `GOMAXPROCS`。

## Work Stealing

每个 P 有本地队列。某个 P 空闲时，会从其他 P 的本地队列偷一部分 G。

作用：

- 避免某些 P 忙、某些 P 闲。
- 减少全局队列锁竞争。
- 提高 CPU 利用率。

## 系统调用时的 P Hand Off

当 G 发生阻塞系统调用：

```text
G 进入 syscall
M 阻塞在 syscall
P 从 M 上解绑
P 移交给其他 M
其他 G 继续运行
```

系统调用返回后：

- 如果有空闲 P，原 M 可能重新绑定 P 继续运行 G。
- 如果没有空闲 P，G 被放回可运行队列，M 可能休眠。

这样做的目的是：一个系统调用阻塞不能占住 P，不能影响其他 Goroutine 执行。

## sysmon 的作用

`sysmon` 是 runtime 的后台监控线程，不依赖 P 执行。

它会：

- 监控长时间运行的 G，触发抢占。
- 监控长时间阻塞的 syscall，协助 P hand off。
- 处理网络轮询器就绪事件。
- 处理定时器。
- 辅助 GC 调度。

面试表达：

> sysmon 负责观察 runtime 的调度状态，发现长时间运行、长时间 syscall、netpoller 事件和 timer 等情况，必要时触发抢占或唤醒调度，避免调度器被局部阻塞拖住。

## G 的生命周期

高级岗可能要求画状态流转。

简化状态图：

```text
_Gidle
  -> _Grunnable
  -> _Grunning
  -> _Gwaiting
  -> _Grunnable
  -> _Grunning
  -> _Gdead
```

关键状态：

- `_Gidle`：刚分配或未初始化。
- `_Grunnable`：可运行，等待被调度。
- `_Grunning`：正在 M 上执行。
- `_Gwaiting`：等待 channel、锁、timer、网络 I/O 等。
- `_Gsyscall`：系统调用中。
- `_Gdead`：执行结束，可被复用。

系统调用路径：

```text
_Grunning -> _Gsyscall -> _Grunnable -> _Grunning
```

channel 阻塞路径：

```text
_Grunning -> _Gwaiting -> _Grunnable -> _Grunning
```

## 并发与并行

并发：同时处理多个任务的能力，不一定同一时刻执行。

并行：多个任务在同一时刻真正运行，依赖多核 CPU。

例子：

- 单核 CPU 上多个 Goroutine 交替执行，是并发但不是并行。
- 多核 CPU 上多个 Goroutine 同时运行，是并发也是并行。

## 如何检测 Goroutine 泄漏

### 常见原因

- channel 没有人发送或接收。
- Goroutine 没有退出条件。
- context 没有传递或没有监听。
- ticker 没有停止。
- 下游 I/O 永久阻塞。
- worker 队列没人关闭。

### 排查工具

```bash
go tool pprof http://localhost:6060/debug/pprof/goroutine
```

也可以访问：

```text
/debug/pprof/goroutine?debug=2
```

看大量 Goroutine 是否卡在同一位置：

- `chan receive`
- `chan send`
- `select`
- `sync.Mutex.Lock`
- `net/http`
- `database/sql`

### 监控指标

- Goroutine 数量持续上涨。
- 内存持续上涨。
- 请求结束后 Goroutine 不下降。
- MQ 消费者或 worker 数异常。

## 面试回答模板：GMP

GMP 是 Go runtime 的调度模型。G 是 Goroutine，M 是 OS 线程，P 是执行 Go 代码的调度上下文。M 必须持有 P 才能运行 G，P 的数量由 `GOMAXPROCS` 决定。

G 通常先进入当前 P 的本地队列，M 从本地队列取 G 执行。本地队列为空时，会从全局队列取，或者从其他 P 偷任务，这就是 work stealing。G 阻塞在 channel、锁、网络 I/O 时，runtime 会把它挂起并调度其他 G。

如果 G 进入阻塞系统调用，M 可能阻塞，runtime 会把 P 从 M 上解绑并交给其他 M 继续运行其他 G。sysmon 会监控长时间 syscall、抢占、netpoller 和 timer。Goroutine 泄漏一般用 pprof goroutine profile、runtime metrics 和日志定位。

## 2. Channel 与并发通信

### 高频题

- 无缓冲 channel 和有缓冲 channel 的区别？
- select 在多个 channel 中如何工作？
- 关闭 channel 时什么情况会 panic？
- 如何用 channel 实现 worker pool？
- channel 底层结构是什么？

## 30 秒回答

无缓冲 channel 发送和接收必须同时就绪，天然有同步交接语义；有缓冲 channel 在缓冲区未满时发送不阻塞，在缓冲区非空时接收不阻塞，适合削峰和解耦生产消费速度差。

`select` 可以同时监听多个 channel 操作，如果多个 case 同时就绪，会伪随机选择一个；如果没有 case 就绪且没有 default，会阻塞；有 default 则执行 default。关闭 channel 要遵守规则：向已关闭 channel 发送会 panic，重复关闭会 panic，关闭 nil channel 会 panic；从已关闭 channel 接收会立即返回零值和 `ok=false`。

底层上 channel 是 runtime 的 `hchan`，包含环形缓冲区、sendq、recvq、closed 标记和锁。

## hchan 底层结构

Go runtime 里的 channel 核心结构可以简化理解为：

```go
type hchan struct {
    qcount   uint
    dataqsiz uint
    buf      unsafe.Pointer
    elemsize uint16
    closed   uint32
    elemtype *_type
    sendx    uint
    recvx    uint
    recvq    waitq
    sendq    waitq
    lock     mutex
}
```

字段含义：

- `qcount`：缓冲区当前元素数量。
- `dataqsiz`：缓冲区容量。
- `buf`：环形缓冲区。
- `closed`：是否关闭。
- `sendx`：发送写入位置。
- `recvx`：接收读取位置。
- `recvq`：等待接收的 Goroutine 队列。
- `sendq`：等待发送的 Goroutine 队列。
- `lock`：保护 channel 内部状态。

## 无缓冲 channel

```go
ch := make(chan int)
```

特点：

- 发送必须等接收。
- 接收必须等发送。
- 数据直接从发送方交给接收方。
- 适合同步交接、信号通知。

示例：

```go
done := make(chan struct{})

go func() {
    work()
    close(done)
}()

<-done
```

## 有缓冲 channel

```go
ch := make(chan int, 100)
```

特点：

- 缓冲区未满时，发送可以立即完成。
- 缓冲区非空时，接收可以立即完成。
- 缓冲区满时，发送阻塞。
- 缓冲区空时，接收阻塞。

适合：

- 生产者消费者。
- 有界任务队列。
- 短暂削峰。
- 限制并发。

## select 工作机制

```go
select {
case v := <-ch1:
    handle(v)
case ch2 <- value:
    sendOK()
case <-ctx.Done():
    return ctx.Err()
default:
    return ErrWouldBlock
}
```

规则：

- 多个 case 同时 ready，伪随机选择一个。
- 没有 ready case 且没有 default，阻塞。
- 没有 ready case 且有 default，立即执行 default。
- nil channel 永远不会 ready。

## channel panic 规则

| 操作 | 结果 |
| --- | --- |
| 向已关闭 channel 发送 | panic |
| 重复关闭 channel | panic |
| 关闭 nil channel | panic |
| 从已关闭 channel 接收 | 返回零值，`ok=false` |
| 向 nil channel 发送 | 永久阻塞 |
| 从 nil channel 接收 | 永久阻塞 |

示例：

```go
ch := make(chan int)
close(ch)
ch <- 1 // panic: send on closed channel
```

```go
close(ch)
close(ch) // panic: close of closed channel
```

## 谁来关闭 channel

原则：发送方关闭 channel，接收方不要关闭 channel。

如果多个发送方，要由协调者统一关闭：

```go
var wg sync.WaitGroup
jobs := make(chan Job)

for i := 0; i < producerNum; i++ {
    wg.Add(1)
    go func() {
        defer wg.Done()
        produce(jobs)
    }()
}

go func() {
    wg.Wait()
    close(jobs)
}()
```

## 用 channel 实现 worker pool

```go
func RunWorkerPool(ctx context.Context, jobs <-chan Job, workerNum int) {
    var wg sync.WaitGroup

    for i := 0; i < workerNum; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for {
                select {
                case <-ctx.Done():
                    return
                case job, ok := <-jobs:
                    if !ok {
                        return
                    }
                    handle(job)
                }
            }
        }()
    }

    wg.Wait()
}
```

关键点：

- worker 数固定，控制并发。
- jobs channel 有界，控制内存。
- ctx 取消，支持优雅退出。
- channel 关闭，worker 自然退出。

## 面试回答模板：Channel

无缓冲 channel 强调同步，发送和接收必须同时准备好；有缓冲 channel 可以解耦生产者和消费者的短暂速度差，但缓冲不是无限队列，满了仍会阻塞。

channel 底层是 `hchan`，有环形缓冲区、sendq、recvq、closed 标记和锁。发送时如果有等待接收者，可以直接交付；否则有缓冲就写入缓冲区，缓冲满则发送方进入 sendq。接收时逻辑类似，如果没有数据且没有发送者，就进入 recvq。

关闭 channel 要注意，向已关闭 channel 发送会 panic，重复 close 会 panic，关闭 nil channel 会 panic；接收已关闭 channel 会返回零值和 `ok=false`。多发送方场景要用 WaitGroup 等协调者统一关闭。

## 3. 内存管理与 GC

### 高频题

- Go 的垃圾回收机制是怎样的？
- 三色标记和写屏障如何工作？
- 什么情况下变量会逃逸到堆上？
- 如何排查逃逸？
- GC 频繁怎么定位和优化？

## 30 秒回答

Go GC 是并发标记清扫，核心是三色标记。白色表示未标记对象，灰色表示已发现但引用还没扫描完，黑色表示对象已扫描完成。因为 GC 标记和用户 Goroutine 并发执行，指针关系会变化，所以需要写屏障保证不会漏标对象。Go 的混合写屏障结合插入和删除写屏障思想，减少 STW 并保证并发标记正确。

变量逃逸到堆上通常是因为生命周期超出函数栈帧、返回局部变量指针、被 interface 装箱、闭包捕获、对象过大或编译器无法证明安全。可以用 `go build -gcflags="-m"` 分析。GC 频繁时先用 pprof、trace、benchmem 定位分配热点，再通过减少分配、预分配、复用对象、`sync.Pool`、降低指针数量、控制缓存和队列上限优化。

## 三色标记

```text
白色：未标记，可能被回收
灰色：已发现，但引用还没扫描完
黑色：已扫描完成，确认存活
```

流程：

1. 从 GC roots 出发，包括栈、全局变量、寄存器等。
2. 根对象标记为灰色。
3. 从灰色队列取对象，扫描它引用的对象。
4. 被引用的白色对象变灰。
5. 当前对象扫描完成后变黑。
6. 灰色队列为空，标记结束。
7. 剩余白色对象可回收。

## 为什么需要写屏障

GC 标记和用户程序并发执行时，用户程序可能修改指针。

例如：

```go
a.child = b
```

如果 `a` 已经被标成黑色，`b` 还是白色，GC 如果不知道这个新引用，就可能误回收 `b`。

写屏障的作用是在指针写入时通知 GC，维护三色不变性，避免漏标。

## 混合写屏障怎么回答

面试不用背源码细节，可以这样说：

> Go 的混合写屏障结合了插入写屏障和删除写屏障的思想。并发标记期间，用户 Goroutine 修改指针时，runtime 会通过写屏障记录或标记相关对象，保证可达对象不会因为引用变化被漏标。它的目标是在保持并发标记正确性的同时减少 STW 和栈重扫成本。

## 逃逸分析

查看逃逸：

```bash
go build -gcflags="-m" ./...
```

常见逃逸原因：

### 1. 返回局部变量指针

```go
func f() *User {
    u := User{}
    return &u
}
```

`u` 需要在函数返回后继续存在，所以逃逸到堆。

### 2. interface 装箱

```go
func logValue(v any) {
    fmt.Println(v)
}
```

热点路径中大量使用 `any`、`fmt`、反射，可能增加分配。

### 3. 闭包捕获

```go
func f() func() int {
    x := 1
    return func() int {
        return x
    }
}
```

`x` 被闭包捕获，生命周期延长。

### 4. 对象过大或编译器无法证明安全

大对象可能放堆上，某些复杂引用关系也可能逃逸。

## GC 频繁如何定位

### pprof heap

```bash
go tool pprof http://localhost:6060/debug/pprof/heap
```

看当前存活对象：

- `inuse_space`
- `inuse_objects`

### pprof allocs

```bash
go tool pprof http://localhost:6060/debug/pprof/allocs
```

看累计分配热点：

- `alloc_space`
- `alloc_objects`

### CPU profile

```bash
go tool pprof http://localhost:6060/debug/pprof/profile
```

如果 runtime GC 相关函数占比高，说明 GC 消耗明显。

### go test benchmem

```bash
go test -bench=. -benchmem
```

关注：

- `B/op`
- `allocs/op`

### trace

```bash
go tool trace trace.out
```

可以看 GC、调度、阻塞、系统调用。

## GC 优化手段

### 1. 减少分配

```go
items := make([]Item, 0, expectedSize)
```

### 2. 复用对象

```go
var bufPool = sync.Pool{
    New: func() any {
        return new(bytes.Buffer)
    },
}
```

使用时：

```go
buf := bufPool.Get().(*bytes.Buffer)
buf.Reset()
defer bufPool.Put(buf)
```

注意：

- 放回前 Reset。
- 不要把仍被外部引用的对象放回。
- `sync.Pool` 可能被 GC 清理，不适合做可靠缓存。

### 3. 减少指针数量

指针越多，GC 扫描越重。热点路径中可以评估 `[]T` 和 `[]*T` 的权衡。

### 4. 控制缓存和队列

无上限 map、slice、channel 会让堆增长，必须有容量、TTL 和淘汰。

### 5. 调整 GOGC / GOMEMLIMIT

`GOGC` 调整 GC 触发频率，`GOMEMLIMIT` 或 `debug.SetMemoryLimit` 控制软内存目标。

调参前先用 pprof 找热点，不要上来就调参数。

## 面试回答模板：GC

Go GC 是并发标记清扫，使用三色标记。白色是未标记对象，灰色是已发现但引用未扫描完，黑色是扫描完成的存活对象。并发标记期间，业务代码还会修改指针，所以 Go 使用混合写屏障保证不会漏标，同时尽量减少 STW。

变量逃逸到堆上通常是因为生命周期超出函数栈，比如返回局部变量指针、闭包捕获、interface 装箱、对象过大或编译器无法证明安全。可以用 `go build -gcflags="-m"` 看逃逸。

GC 频繁时我会先用 pprof heap/allocs、CPU profile、trace、benchmem 定位分配热点。优化上会减少临时对象、预分配 slice、复用 buffer、使用 `sync.Pool`、降低指针数量、控制缓存和队列上限。最后再结合压测调 `GOGC` 或 `GOMEMLIMIT`。

## 4. Slice 底层机制与扩容陷阱

### 高频题

- slice 的底层数据结构是什么？
- slice 扩容规则是什么？
- slice 作为函数参数传递时对原切片有什么影响？
- slice 和 array 的区别？

## 30 秒回答

slice 是对底层数组的描述符，包含三个字段：指向底层数组的指针、长度 len、容量 cap。array 是固定长度值类型，slice 是动态视图。slice 作为函数参数传递时，传递的是 slice header 的副本，但底层数组共享，所以修改已有元素会影响外部；如果 append 触发扩容，新 slice 会指向新数组，外部 slice 不会自动改变。

扩容规则不要死背精确倍数。传统说法是小于 1024 大致翻倍，大于等于 1024 大致增长 25%。但现代 Go runtime 已调整为小 slice 近似 2 倍，大 slice 平滑过渡到约 1.25 倍，当前 Go 1.26.1 的 `nextslicecap` 阈值是 256，最终 cap 还受元素大小和内存分配规格影响。

## slice 底层结构

可以简化理解为：

```go
type slice struct {
    array unsafe.Pointer
    len   int
    cap   int
}
```

含义：

- `array`：指向底层数组。
- `len`：当前元素数量。
- `cap`：从起始位置到底层数组末尾的容量。

## array 与 slice 区别

| 对比项 | array | slice |
| --- | --- | --- |
| 长度 | 固定，是类型的一部分 | 动态 |
| 类型示例 | `[3]int` | `[]int` |
| 传参 | 拷贝整个数组 | 拷贝 slice header |
| 扩容 | 不支持 | append 可能扩容 |
| 底层 | 自己持有数据 | 引用底层数组 |

数组传参：

```go
func f(a [3]int) {
    a[0] = 100
}
```

不会影响外部数组，因为数组值被拷贝。

slice 传参：

```go
func f(s []int) {
    s[0] = 100
}
```

会影响外部底层数组。

## append 不扩容

```go
func f(s []int) {
    s = append(s, 3)
    s[0] = 100
}

func main() {
    s := make([]int, 2, 4)
    s[0], s[1] = 1, 2
    f(s)
    fmt.Println(s) // [100 2]
}
```

容量足够，append 复用底层数组，所以修改元素影响外部。但外部的 len 仍然是 2，看不到新 append 的第 3 个元素。

## append 触发扩容

```go
func f(s []int) {
    s = append(s, 3)
    s[0] = 100
}

func main() {
    s := []int{1, 2}
    f(s)
    fmt.Println(s) // [1 2]
}
```

容量不够，append 分配新数组，函数内 `s` 指向新数组，外部不受影响。

## 扩容规则怎么回答

推荐回答：

> slice append 时，如果容量足够，会复用原底层数组；如果容量不足，会调用 runtime growslice 分配新数组并拷贝旧数据。扩容不是语言规范保证的固定倍数，是 runtime 实现细节。传统面试常说小容量 2 倍、大容量 1.25 倍；现代 Go 中小 slice 仍倾向 2 倍，大 slice 平滑过渡到约 1.25 倍，具体容量还受元素大小和内存分配规格影响，所以不能在业务代码里依赖精确 cap。

当前 Go 1.26.1 runtime 的 `nextslicecap` 逻辑要点：

- 如果需要的新长度超过旧容量 2 倍，直接使用需要的新长度。
- 旧容量小于阈值时倾向 2 倍。
- 大容量时用公式平滑过渡到约 1.25 倍。
- 当前阈值是 256，不是旧资料常说的 1024。

## 常见陷阱

### 1. 子切片引用大数组

```go
func head() []byte {
    data := make([]byte, 100<<20)
    return data[:10]
}
```

返回 10 字节，但底层 100 MB 数组无法释放。

解决：

```go
return append([]byte(nil), data[:10]...)
```

### 2. 多个 slice 共享底层数组

```go
s1 := []int{1, 2, 3, 4}
s2 := s1[1:3]
s2[0] = 99
fmt.Println(s1) // [1 99 3 4]
```

### 3. append 覆盖原数组

```go
s1 := []int{1, 2, 3, 4}
s2 := s1[:2]
s3 := append(s2, 99)
fmt.Println(s1) // [1 2 99 4]
_ = s3
```

因为 `s2` 容量足够，append 写回原数组。

可用 full slice expression 限制容量：

```go
s2 := s1[:2:2]
```

这样 append 会触发扩容，避免覆盖 `s1` 后续元素。

## 面试回答模板：Slice

slice 底层是三字段结构：指向底层数组的指针、len、cap。array 是固定长度值类型，slice 是对数组的动态视图。slice 传参时拷贝的是 slice header，底层数组共享，所以修改已有元素会影响外部。

append 时如果容量足够，会复用底层数组；容量不足会分配新数组并复制数据。函数里 append 如果触发扩容，函数内 slice 会指向新数组，外部 slice 不会自动改变。扩容倍数不要死背，传统说法是小容量 2 倍、大容量约 1.25 倍，但现代 Go runtime 是平滑过渡，具体 cap 还受内存分配规格影响。

实际开发要注意子切片引用大数组导致内存滞留、多个 slice 共享底层数组互相影响、append 可能覆盖原数组。必要时用 copy 或 full slice expression 控制容量。

## 5. Context 使用

### 高频题

- context 的使用场景有哪些？
- 如何优雅传递超时和取消信号？
- `Done()` 方法底层原理是什么？
- context 如何树形传播？

## 30 秒回答

`context.Context` 用于跨 API 边界传递取消信号、超时截止时间和少量请求级元数据。常见场景是 HTTP/gRPC 请求超时、用户取消、服务关闭、下游调用取消、worker 优雅退出。

Context 是树形结构，父 context 取消后会取消所有子 context。`WithCancel`、`WithTimeout`、`WithDeadline` 会创建可取消子节点。底层上，`cancelCtx` 维护一个懒创建的 `done` channel、children 集合、err 和 cause。调用 cancel 时会关闭 done channel，并递归取消子 context。监听 `<-ctx.Done()` 的 Goroutine 会被唤醒。

## 使用场景

### 1. 请求超时

```go
ctx, cancel := context.WithTimeout(r.Context(), 200*time.Millisecond)
defer cancel()

resp, err := service.Do(ctx, req)
```

### 2. 请求取消

客户端断开连接时，`r.Context()` 会被取消。

### 3. 下游调用

```go
req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
```

数据库：

```go
row := db.QueryRowContext(ctx, query, id)
```

### 4. worker 优雅退出

```go
for {
    select {
    case <-ctx.Done():
        return ctx.Err()
    case job := <-jobs:
        handle(ctx, job)
    }
}
```

### 5. 请求级元数据

适合放：

- trace_id。
- request_id。
- tenant_id。
- auth summary。

不适合放：

- 大对象。
- 可选参数。
- 数据库连接。
- 业务核心参数。

## Context 树形传播

```text
Background
  -> request ctx
    -> timeout ctx
      -> db ctx
      -> rpc ctx
```

父节点取消时，子节点全部取消。

```go
parent, cancel := context.WithCancel(context.Background())
child, _ := context.WithTimeout(parent, time.Second)

cancel()
<-child.Done() // child 也会被取消
```

## Done 底层原理

以 `cancelCtx` 为核心理解：

```go
type cancelCtx struct {
    Context
    mu       sync.Mutex
    done     atomic.Value
    children map[canceler]struct{}
    err      atomic.Value
    cause    error
}
```

关键点：

- `Done()` 返回一个只读 channel。
- done channel 是懒创建的。
- 第一次 cancel 会关闭 done channel。
- 关闭 channel 会唤醒所有等待 `<-ctx.Done()` 的 Goroutine。
- 父 context 取消会递归取消 children。
- `Err()` 返回 `context.Canceled` 或 `context.DeadlineExceeded`。

## 为什么要 defer cancel

```go
ctx, cancel := context.WithTimeout(parent, time.Second)
defer cancel()
```

原因：

- 释放 timer 等资源。
- 让子 context 尽早取消。
- 避免资源等到超时自然发生才释放。

即使正常返回，也应该调用 cancel。

## 常见错误

### 1. 传 nil context

不要传 nil。没有合适 context 时使用：

```go
context.Background()
```

或：

```go
context.TODO()
```

### 2. context 存进 struct

通常不建议长期存储 context，应该作为函数第一个参数传递。

### 3. 只传 ctx，但下游不检查

context 不会自动终止代码。必须：

- 调用支持 context 的 API。
- 或在循环中监听 `ctx.Done()`。

### 4. 用 context.Value 传业务参数

这会让函数依赖隐式化。业务参数应该显式传参。

## 面试回答模板：Context

context 主要用于超时、取消和请求级元数据传递。比如 HTTP 请求进入后，我会把 `r.Context()` 一直传到 service、repository、RPC 和 DB 调用。下游使用 `QueryContext`、`NewRequestWithContext` 或 gRPC ctx，这样请求取消或超时时，下游能及时停止。

context 是树形传播的。`WithCancel`、`WithTimeout`、`WithDeadline` 会基于父 context 创建子 context。父 context 取消时，子 context 也会取消。底层可以理解为 `cancelCtx` 维护 done channel 和 children，cancel 时关闭 done channel 并递归取消子节点。

使用上要注意 `defer cancel()`，避免 timer 资源滞留。`context.Value` 只放 trace_id、request_id、tenant_id 这类请求级元数据，不放大对象和业务参数。

## 高频题总复习口诀

- GMP：G 是任务，M 是线程，P 是运行 Go 代码的上下文；M 必须拿 P；P 数量看 `GOMAXPROCS`。
- syscall：G 进 syscall，M 阻塞，P hand off 给其他 M。
- 泄漏：Goroutine 数持续涨，用 pprof goroutine 看阻塞点。
- channel：无缓冲同步交接，有缓冲削峰解耦；已关闭发送 panic，重复 close panic。
- hchan：buf、sendq、recvq、closed、lock。
- select：多 ready 伪随机，无 ready 阻塞，有 default 非阻塞。
- GC：三色标记 + 混合写屏障；先用 pprof 找分配热点，再优化。
- 逃逸：`go build -gcflags="-m"`。
- slice：ptr、len、cap；传参拷贝 header，共享底层数组。
- append：容量够复用，容量不够扩容换数组。
- context：超时、取消、元数据；树形传播；cancel 关闭 Done channel。

