# Goroutine 与线程的区别

## 30 秒回答

Goroutine 是 Go 运行时管理的轻量级并发执行单元，线程是操作系统管理的调度单元。Goroutine 的创建、销毁和切换成本都比线程低很多，初始栈也更小，并且可以按需增长。Go 通过 GMP 调度模型把大量 Goroutine 映射到少量 OS 线程上执行，所以非常适合高并发 I/O 场景。

但 Goroutine 不是没有成本。大量 Goroutine 如果阻塞在 channel、锁、网络请求或无法退出的循环里，也会造成内存占用、调度压力和泄漏。所以实际开发中要配合 context、超时、限流、worker pool 等手段控制生命周期。

## 核心区别

| 对比项 | Goroutine | OS 线程 |
| --- | --- | --- |
| 管理者 | Go runtime | 操作系统内核 |
| 调度方式 | 用户态调度为主，由 Go scheduler 负责 | 内核态调度 |
| 创建成本 | 低 | 高 |
| 初始栈 | 很小，通常按需增长 | 较大，通常固定或增长成本更高 |
| 切换成本 | 相对低，不一定进入内核 | 相对高，涉及内核调度 |
| 数量级 | 可以轻松创建成千上万甚至更多 | 通常不能无限创建 |
| 阻塞处理 | runtime 会尽量避免阻塞整个调度器 | 线程阻塞就是该 OS 线程阻塞 |
| 编程模型 | `go func()`，偏 CSP 风格 | 需要直接使用线程 API 或线程池 |

## Goroutine 为什么轻量

1. 栈小且可增长  
   Goroutine 初始栈很小，运行过程中如果栈空间不够，runtime 会进行栈扩容。线程的栈通常更大，因此大量线程会带来明显内存压力。

2. 调度在用户态完成  
   Go runtime 自己维护 Goroutine 队列，很多调度决策不需要频繁进入内核态，因此切换成本较低。

3. M:N 调度  
   Go 不是一个 Goroutine 对应一个线程，而是把多个 Goroutine 调度到多个 OS 线程上。大量并发任务可以复用少量线程。

4. 与网络轮询器结合  
   对网络 I/O，Go runtime 可以把等待 I/O 的 Goroutine 挂起，等事件就绪后再恢复执行，避免 OS 线程长时间空等。

## Goroutine 和线程的关系

Go 程序最终还是运行在 OS 线程上。Goroutine 本身不能脱离线程执行，它只是由 runtime 调度到某个线程上运行。

可以理解为：

```text
多个 Goroutine -> Go runtime 调度 -> 多个 OS 线程 -> CPU
```

其中：

- G 表示 Goroutine。
- M 表示 OS 线程。
- P 表示处理器上下文，负责连接 G 和 M。

## Goroutine 的优势场景

### 1. 高并发 I/O

例如 HTTP 服务、RPC 服务、消息消费、数据库访问、缓存访问等。

```go
go func() {
    resp, err := http.Get(url)
    if err != nil {
        return
    }
    defer resp.Body.Close()
}()
```

每个请求或任务可以用一个 Goroutine 表示，代码同步写法，执行上并发运行。

### 2. 后台异步任务

例如异步写日志、异步刷新缓存、后台统计、定时任务。

```go
go func() {
    ticker := time.NewTicker(time.Minute)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            flushMetrics()
        case <-ctx.Done():
            return
        }
    }
}()
```

重点是要有退出条件，不能只启动不回收。

### 3. 并发流水线

多个 Goroutine 通过 channel 组织成生产者、消费者、聚合器。

## 常见误区

### 误区 1：Goroutine 越多越好

不是。Goroutine 虽轻量，但仍然占用栈、调度结构、channel 等资源。过多 Goroutine 会带来：

- 内存占用升高。
- 调度开销增加。
- GC 扫描压力增加。
- 排查问题难度增加。

### 误区 2：Goroutine 一定比线程快

不能这么绝对。Goroutine 创建和切换成本更低，但如果任务是 CPU 密集型，性能主要受 CPU 核数、缓存命中、锁竞争和算法影响。无限增加 Goroutine 不会让 CPU 密集任务更快。

### 误区 3：只要用了 Goroutine 就是异步安全

Goroutine 之间共享变量时仍然有数据竞争，需要用锁、channel、atomic 或其他同步手段保护。

## 面试回答模板

Goroutine 和线程最大的区别在于调度层级和成本。线程由操作系统调度，创建和上下文切换成本较高；Goroutine 由 Go runtime 管理，初始栈小、可增长，切换通常在用户态完成，所以可以支撑大量并发任务。

Go runtime 通过 GMP 模型把多个 Goroutine 映射到多个 OS 线程上执行。对网络 I/O，runtime 还会配合 netpoller，在等待 I/O 时挂起 Goroutine，而不是让线程一直阻塞。

不过 Goroutine 不是无限免费的。如果 Goroutine 没有退出条件，或者阻塞在 channel、锁、I/O 上，就会造成泄漏和调度压力。实际项目里我会通过 context、超时、限流、worker pool 和 pprof 来控制和排查。

## 常见追问

### 1. Goroutine 泄漏是什么？

Goroutine 泄漏是指 Goroutine 启动后因为没有退出条件、channel 永远收不到数据、锁一直拿不到、I/O 长时间阻塞等原因无法结束，导致数量持续增加。

典型例子：

```go
func leak(ch <-chan int) {
    go func() {
        v := <-ch
        fmt.Println(v)
    }()
}
```

如果没有人向 `ch` 发送数据，这个 Goroutine 会一直阻塞。

### 2. CPU 密集型任务适合开很多 Goroutine 吗？

不适合盲目开很多。CPU 密集任务最终受 CPU 核数限制，过多 Goroutine 只会增加调度和缓存开销。通常会控制并发度，例如设置 worker 数量接近 `runtime.GOMAXPROCS(0)` 或 CPU 核数。

### 3. Goroutine 阻塞会不会阻塞线程？

要看阻塞类型。普通 channel、锁、定时器、网络 I/O 等，runtime 通常能感知并调度其他 Goroutine。某些系统调用或 cgo 调用可能会阻塞 OS 线程，此时 runtime 可能创建或唤醒其他线程来维持调度能力。

