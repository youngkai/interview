# Channel、select 的使用场景

## 30 秒回答

Channel 是 Go 中 Goroutine 之间通信和同步的机制，强调“不要通过共享内存来通信，而要通过通信来共享内存”。它适合任务分发、结果收集、生产者消费者、信号通知、并发控制等场景。

`select` 用来同时监听多个 channel 操作，常见于超时控制、取消信号、多路复用、非阻塞收发和优雅退出。实际项目中，channel 适合表达所有权转移和事件通知，但不应该滥用。简单共享状态、高频计数、读多写少缓存，可能用锁或 atomic 更合适。

## Channel 基础

### 无缓冲 channel

```go
ch := make(chan int)
```

无缓冲 channel 的发送和接收必须同时准备好，天然具有同步效果。

```go
go func() {
    ch <- 1 // 等待接收者
}()

v := <-ch
fmt.Println(v)
```

特点：

- 发送阻塞直到有人接收。
- 接收阻塞直到有人发送。
- 适合强同步、交接任务、信号确认。

### 有缓冲 channel

```go
ch := make(chan int, 10)
```

有缓冲 channel 可以在缓冲区未满时发送成功，在缓冲区非空时接收成功。

特点：

- 能削峰填谷。
- 能解耦生产者和消费者的短暂速度差。
- 缓冲区满了发送仍会阻塞。
- 缓冲区空了接收仍会阻塞。

## Channel 的常见使用场景

### 1. 任务队列

```go
jobs := make(chan Job, 100)

for i := 0; i < workerNum; i++ {
    go func() {
        for job := range jobs {
            handle(job)
        }
    }()
}

for _, job := range input {
    jobs <- job
}
close(jobs)
```

适合固定 worker 池，避免每个任务都创建无限 Goroutine。

### 2. 结果收集

```go
results := make(chan Result, len(tasks))

for _, task := range tasks {
    task := task
    go func() {
        results <- doTask(task)
    }()
}

for range tasks {
    result := <-results
    fmt.Println(result)
}
```

注意这里 `results` 设置缓冲为任务数量，可以避免某些情况下发送方因为接收方未及时接收而阻塞。

### 3. 并发限制

```go
sem := make(chan struct{}, 10)

for _, item := range items {
    item := item
    sem <- struct{}{}
    go func() {
        defer func() { <-sem }()
        process(item)
    }()
}
```

用带缓冲 channel 作为信号量，最多允许 10 个任务同时执行。

### 4. 退出通知

```go
done := make(chan struct{})

go func() {
    defer close(done)
    work()
}()

<-done
```

如果涉及跨 API 传递取消信号，更推荐使用 `context.Context`。

### 5. 广播通知

关闭 channel 可以广播给所有接收者。

```go
done := make(chan struct{})

for i := 0; i < 3; i++ {
    go func() {
        <-done
        fmt.Println("exit")
    }()
}

close(done)
```

所有等待 `<-done` 的 Goroutine 都会被唤醒。

## Channel 关闭规则

### 谁负责关闭 channel

一般原则：发送方关闭 channel，接收方不要关闭 channel。

原因是关闭后再发送会 panic：

```go
close(ch)
ch <- 1 // panic: send on closed channel
```

多个发送方时，关闭要非常谨慎，通常需要额外协调，例如 `sync.WaitGroup`。

### 从关闭的 channel 接收

```go
v, ok := <-ch
if !ok {
    // channel 已关闭且数据已读完
}
```

如果 channel 已关闭且缓冲区为空，接收会立即返回零值和 `ok=false`。

## select 基础

`select` 可以同时等待多个 channel 操作。

```go
select {
case v := <-ch1:
    fmt.Println(v)
case ch2 <- 10:
    fmt.Println("sent")
case <-time.After(time.Second):
    fmt.Println("timeout")
}
```

规则：

- 如果多个 case 同时可执行，会伪随机选择一个。
- 如果没有 case 可执行且没有 default，会阻塞。
- 如果有 default 且其他 case 都不可执行，会执行 default。
- `nil` channel 对应的 case 永远不会就绪。

## select 的常见使用场景

### 1. 超时控制

```go
select {
case result := <-resultCh:
    return result, nil
case <-time.After(2 * time.Second):
    return Result{}, errors.New("timeout")
}
```

注意：在循环中频繁使用 `time.After` 可能产生额外定时器对象，建议复用 `time.Timer`。

### 2. 取消控制

```go
select {
case <-ctx.Done():
    return ctx.Err()
case job := <-jobs:
    return handle(job)
}
```

长耗时任务应该定期检查 `ctx.Done()`。

### 3. 非阻塞发送

```go
select {
case ch <- value:
    return true
default:
    return false
}
```

适合指标上报、日志采样、尽力而为的异步通知。

### 4. 非阻塞接收

```go
select {
case v := <-ch:
    return v, true
default:
    return zero, false
}
```

适合尝试读取，不希望当前 Goroutine 被阻塞。

### 5. 多路复用

```go
for {
    select {
    case msg := <-messageCh:
        handleMessage(msg)
    case event := <-eventCh:
        handleEvent(event)
    case <-ctx.Done():
        return
    }
}
```

适合一个 Goroutine 管理多类事件。

## Channel 和锁如何选择

适合 channel：

- 任务传递。
- 数据所有权转移。
- 生命周期通知。
- 多阶段流水线。
- 需要组合取消、超时、多路等待。

适合锁：

- 保护共享 map、缓存、计数器等状态。
- 临界区很小。
- 读写状态比传递任务更自然。

适合 atomic：

- 简单计数。
- 状态标记。
- 高频无锁读写。

一句话：channel 更适合通信和编排，锁更适合保护共享状态。

## 常见坑

### 1. 向 nil channel 收发会永久阻塞

```go
var ch chan int
ch <- 1 // 永久阻塞
```

### 2. 重复 close 会 panic

```go
close(ch)
close(ch) // panic
```

### 3. 向已关闭 channel 发送会 panic

```go
close(ch)
ch <- 1 // panic
```

### 4. 只发送不接收导致泄漏

```go
func query() <-chan Result {
    ch := make(chan Result)
    go func() {
        ch <- slowQuery()
    }()
    return ch
}
```

如果调用方超时返回，不再接收，发送方可能永久阻塞。解决方式包括：

- 使用缓冲 channel。
- 发送时监听 `ctx.Done()`。
- 保证接收方消费。

## 面试回答模板

Channel 本质上是 Goroutine 之间通信和同步的工具。无缓冲 channel 强调同步交接，有缓冲 channel 可以解耦生产和消费速度差。常见场景包括任务队列、结果收集、并发限制、退出通知和流水线。

`select` 用来同时等待多个 channel 操作，常用于超时、取消、非阻塞收发和多路复用。比如请求处理里，我会把业务结果 channel 和 `ctx.Done()` 放到同一个 select 里，这样请求取消或超时时能及时退出。

但 channel 不是所有并发问题的答案。如果只是保护共享 map 或缓存，用 `sync.Mutex` 可能更简单；如果是高频计数，用 `atomic` 更合适。我的判断标准是：数据流动和生命周期编排用 channel，共享状态保护用锁。

## 常见追问

### 1. 有缓冲 channel 大小怎么设置？

要看业务含义。缓冲不是越大越好，太大会掩盖下游消费能力不足，导致内存堆积和延迟变高。通常根据吞吐、峰值、消费者能力和可接受延迟设置，并配合监控观察队列长度。

### 2. select 多个 case 同时 ready 时执行哪个？

Go 会伪随机选择一个 ready case，避免固定顺序导致饥饿。

### 3. close channel 的作用是什么？

关闭 channel 表示不会再发送新数据。接收方可以通过 `for range ch` 读完剩余数据并退出，也可以通过 `v, ok := <-ch` 判断是否关闭。关闭 channel 还可以作为广播通知。

### 4. channel 会不会有锁？

会。channel 底层为了保护队列、等待发送者、等待接收者等状态，需要同步机制。channel 是高级并发原语，不代表完全无锁。

