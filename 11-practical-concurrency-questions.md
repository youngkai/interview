# Go 并发实战例题

这篇整理偏“现场写代码 + 解释设计”的面试题。重点不是只写出能跑的代码，而是能说明为什么这样设计、如何退出、如何限流、如何避免 Goroutine 泄漏和内存膨胀。

## 题目 1：写一个生产者-消费者模型，要求用 goroutine 和 channel 实现

### 30 秒回答

生产者-消费者模型可以用一个 jobs channel 作为任务队列，多个 producer 往 channel 写任务，多个 consumer 从 channel 读任务并处理。为了优雅退出，一般使用 `sync.WaitGroup` 等待生产者结束，然后由统一位置关闭 jobs channel；消费者通过 `for job := range jobs` 消费，channel 关闭且任务读完后自动退出。

关键点是：channel 应该由发送方关闭；多个生产者时不能让每个生产者都 close channel，而是等待所有生产者结束后由协调者统一 close。消费者处理完后用 WaitGroup 等待，保证主流程不会提前退出。

### 基础版本代码

```go
package main

import (
    "fmt"
    "sync"
)

type Job struct {
    ID int
}

func producer(id int, jobs chan<- Job, count int, wg *sync.WaitGroup) {
    defer wg.Done()

    for i := 0; i < count; i++ {
        job := Job{ID: id*1000 + i}
        jobs <- job
        fmt.Printf("producer %d produced job %d\n", id, job.ID)
    }
}

func consumer(id int, jobs <-chan Job, wg *sync.WaitGroup) {
    defer wg.Done()

    for job := range jobs {
        fmt.Printf("consumer %d handled job %d\n", id, job.ID)
    }
}

func main() {
    jobs := make(chan Job, 100)

    producerCount := 3
    consumerCount := 5
    jobsPerProducer := 10

    var producerWG sync.WaitGroup
    var consumerWG sync.WaitGroup

    for i := 0; i < consumerCount; i++ {
        consumerWG.Add(1)
        go consumer(i, jobs, &consumerWG)
    }

    for i := 0; i < producerCount; i++ {
        producerWG.Add(1)
        go producer(i, jobs, jobsPerProducer, &producerWG)
    }

    producerWG.Wait()
    close(jobs)

    consumerWG.Wait()
}
```

### 代码解释

这个实现里有三层核心逻辑：

1. `jobs := make(chan Job, 100)` 是任务队列，缓冲区可以削峰，但不会无限增长。
2. 生产者只负责发送任务，不负责关闭 channel。
3. 主 Goroutine 等所有生产者完成后统一 `close(jobs)`，消费者通过 `for range jobs` 自动退出。

为什么不能让消费者关闭 channel？

因为消费者只知道自己不想再接收了，但不知道是否还有其他生产者正在发送。如果消费者关闭 channel，而生产者继续发送，会触发 `panic: send on closed channel`。

为什么多个生产者不能各自 close？

因为 channel 只能关闭一次。多个生产者并发 close 会 panic，而且一个生产者结束不代表其他生产者也结束。

### 加上 context 的优雅退出版本

面试中如果追问“如何支持取消、超时、服务关闭”，可以写这个版本。

```go
package main

import (
    "context"
    "fmt"
    "sync"
    "time"
)

type Job struct {
    ID int
}

func producer(ctx context.Context, id int, jobs chan<- Job, wg *sync.WaitGroup) {
    defer wg.Done()

    for i := 0; ; i++ {
        job := Job{ID: id*1000 + i}

        select {
        case jobs <- job:
            fmt.Printf("producer %d produced job %d\n", id, job.ID)
        case <-ctx.Done():
            fmt.Printf("producer %d exit: %v\n", id, ctx.Err())
            return
        }

        time.Sleep(100 * time.Millisecond)
    }
}

func consumer(ctx context.Context, id int, jobs <-chan Job, wg *sync.WaitGroup) {
    defer wg.Done()

    for {
        select {
        case job, ok := <-jobs:
            if !ok {
                return
            }
            fmt.Printf("consumer %d handled job %d\n", id, job.ID)
        case <-ctx.Done():
            fmt.Printf("consumer %d exit: %v\n", id, ctx.Err())
            return
        }
    }
}

func main() {
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()

    jobs := make(chan Job, 100)

    var producerWG sync.WaitGroup
    var consumerWG sync.WaitGroup

    for i := 0; i < 5; i++ {
        consumerWG.Add(1)
        go consumer(ctx, i, jobs, &consumerWG)
    }

    for i := 0; i < 3; i++ {
        producerWG.Add(1)
        go producer(ctx, i, jobs, &producerWG)
    }

    producerWG.Wait()
    close(jobs)
    consumerWG.Wait()
}
```

### 这个版本的注意点

生产者发送任务时用：

```go
select {
case jobs <- job:
case <-ctx.Done():
    return
}
```

这样如果队列满了，同时 context 已取消，生产者不会永久阻塞在发送上。

消费者接收任务时用：

```go
select {
case job, ok := <-jobs:
case <-ctx.Done():
    return
}
```

这样消费者既能在 channel 关闭时退出，也能在服务取消时退出。

### 面试回答模板

我会用 channel 作为任务队列，生产者往 channel 里写任务，消费者从 channel 里读任务。消费者数量可以固定，这样能控制并发度，避免每个任务都新建 Goroutine。

关闭流程上，我不会让多个生产者各自关闭 channel，而是用 WaitGroup 等所有生产者结束后，由主 Goroutine 统一关闭 jobs channel。消费者使用 `for range jobs` 或 `job, ok := <-jobs`，当 channel 关闭且任务消费完后自然退出。

如果题目要求优雅退出，我会加 `context.Context`。生产者发送时 select 监听 `ctx.Done()`，消费者接收时也监听 `ctx.Done()`，这样队列满、请求取消或服务关闭时都不会造成 Goroutine 泄漏。

### 常见追问

#### 1. channel 缓冲区设置多大？

看业务吞吐和消费者处理能力。缓冲区可以削峰，但不能当作无限队列。过大可能导致内存堆积和延迟升高，过小可能导致生产者频繁阻塞。实际项目需要配合指标观察队列长度、消费耗时和丢弃策略。

#### 2. 消费者处理失败怎么办？

要看任务语义。常见方案有：

- 返回错误并记录日志。
- 重试有限次数。
- 投递到失败队列。
- 使用死信队列。
- 对不可重试错误直接丢弃或告警。

不能无限重试，否则可能阻塞整个消费链路。

#### 3. 如何保证任务不丢？

单机内存 channel 不能保证进程崩溃后任务不丢。如果要强可靠，需要使用消息队列、数据库状态表、WAL 或其他持久化机制。Go channel 更适合进程内并发协作。

#### 4. 如何避免消费者太慢导致内存爆炸？

不要使用无限队列。可以使用固定大小 channel，生产者在队列满时阻塞、超时、降级或丢弃。也可以增加消费者、做批处理、限流上游，或者把任务交给外部消息队列。

## 题目 2：如何优雅处理大并发请求并保证内存不爆炸

### 30 秒回答

处理大并发请求时，核心是限制并发、限制队列、限制超时和控制对象生命周期。不能每来一个请求就无限制创建 Goroutine，也不能把请求无限堆在内存队列里。

我会从入口限流、worker pool、带缓冲但有上限的队列、context 超时取消、连接池限制、响应流式处理、对象复用、监控和降级几个方面设计。目标是系统在压力超过承载能力时能排队、拒绝或降级，而不是无限占用内存直到 OOM。

### 设计原则

#### 1. 限制入口并发

使用信号量、限流器、连接数限制或服务网关限制并发。

```go
var sem = make(chan struct{}, 1000)

func handler(w http.ResponseWriter, r *http.Request) {
    select {
    case sem <- struct{}{}:
        defer func() { <-sem }()
    default:
        http.Error(w, "too many requests", http.StatusTooManyRequests)
        return
    }

    handle(w, r)
}
```

这段代码表示最多允许 1000 个请求同时进入核心处理逻辑。超过后直接返回 429，避免无限排队。

#### 2. 所有请求设置超时

```go
func handler(w http.ResponseWriter, r *http.Request) {
    ctx, cancel := context.WithTimeout(r.Context(), 200*time.Millisecond)
    defer cancel()

    result, err := service.Do(ctx)
    if err != nil {
        http.Error(w, err.Error(), http.StatusGatewayTimeout)
        return
    }

    _ = json.NewEncoder(w).Encode(result)
}
```

超时的作用：

- 防止慢请求长期占用 Goroutine。
- 防止下游卡住导致资源耗尽。
- 给系统一个明确的失败边界。

#### 3. 下游调用也要支持 context

只在入口设置 timeout 不够，下游也要使用支持 context 的 API。

```go
req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
if err != nil {
    return nil, err
}

resp, err := http.DefaultClient.Do(req)
```

数据库：

```go
row := db.QueryRowContext(ctx, query, id)
```

否则 context 超时了，下游调用仍可能继续阻塞。

#### 4. 使用 worker pool 控制后台任务并发

如果请求会投递异步任务，不要每个任务都无限开 Goroutine。

```go
type Pool struct {
    jobs chan Job
}

func NewPool(workerNum int, queueSize int) *Pool {
    p := &Pool{
        jobs: make(chan Job, queueSize),
    }

    for i := 0; i < workerNum; i++ {
        go p.worker()
    }

    return p
}

func (p *Pool) Submit(ctx context.Context, job Job) error {
    select {
    case p.jobs <- job:
        return nil
    case <-ctx.Done():
        return ctx.Err()
    default:
        return ErrQueueFull
    }
}

func (p *Pool) worker() {
    for job := range p.jobs {
        process(job)
    }
}
```

这个结构用 `workerNum` 控制执行并发，用 `queueSize` 控制内存队列上限。

### 上面 Submit 写法的细节

这段代码有一个需要注意的点：

```go
select {
case p.jobs <- job:
    return nil
case <-ctx.Done():
    return ctx.Err()
default:
    return ErrQueueFull
}
```

有 `default` 时，如果队列满，会立即返回 `ErrQueueFull`，不会等待。如果希望等待一小段时间，可以改成：

```go
func (p *Pool) Submit(ctx context.Context, job Job) error {
    select {
    case p.jobs <- job:
        return nil
    case <-ctx.Done():
        return ctx.Err()
    }
}
```

然后由调用方给 ctx 设置较短 timeout。

### 完整示例：带并发限制和队列上限的 HTTP 处理

```go
package main

import (
    "context"
    "encoding/json"
    "errors"
    "net/http"
    "sync"
    "time"
)

var ErrQueueFull = errors.New("queue full")

type Job struct {
    ID int64
}

type Result struct {
    OK bool `json:"ok"`
}

type Pool struct {
    jobs chan Job
    wg   sync.WaitGroup
}

func NewPool(workerNum int, queueSize int) *Pool {
    p := &Pool{
        jobs: make(chan Job, queueSize),
    }

    for i := 0; i < workerNum; i++ {
        p.wg.Add(1)
        go p.worker()
    }

    return p
}

func (p *Pool) Submit(ctx context.Context, job Job) error {
    select {
    case p.jobs <- job:
        return nil
    case <-ctx.Done():
        return ctx.Err()
    default:
        return ErrQueueFull
    }
}

func (p *Pool) Close() {
    close(p.jobs)
    p.wg.Wait()
}

func (p *Pool) worker() {
    defer p.wg.Done()

    for job := range p.jobs {
        process(job)
    }
}

func process(job Job) {
    time.Sleep(20 * time.Millisecond)
}

type Server struct {
    pool *Pool
    sem  chan struct{}
}

func NewServer() *Server {
    return &Server{
        pool: NewPool(100, 1000),
        sem:  make(chan struct{}, 500),
    }
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    select {
    case s.sem <- struct{}{}:
        defer func() { <-s.sem }()
    default:
        http.Error(w, "too many requests", http.StatusTooManyRequests)
        return
    }

    ctx, cancel := context.WithTimeout(r.Context(), 200*time.Millisecond)
    defer cancel()

    job := Job{ID: time.Now().UnixNano()}
    if err := s.pool.Submit(ctx, job); err != nil {
        if errors.Is(err, ErrQueueFull) {
            http.Error(w, "queue full", http.StatusTooManyRequests)
            return
        }
        http.Error(w, err.Error(), http.StatusGatewayTimeout)
        return
    }

    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(Result{OK: true})
}
```

### 为什么这个设计不容易内存爆炸

它有三个上限：

1. `sem` 限制同时进入处理逻辑的请求数。
2. `workerNum` 限制后台任务真实执行并发。
3. `queueSize` 限制内存中等待处理的任务数量。

当压力超过系统能力时，请求会被快速拒绝，而不是无限排队。这样牺牲一部分请求成功率，换取整体服务稳定性。

### 其他优化点

#### 1. 避免一次性读入大请求体

不要无脑：

```go
body, _ := io.ReadAll(r.Body)
```

应该限制大小：

```go
r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
```

或者流式处理。

#### 2. 响应尽量流式处理

对于大结果集，不要全部加载到内存再返回。可以分页、流式编码或异步导出。

#### 3. 控制连接池

数据库和 HTTP 客户端都要设置连接池。

```go
db.SetMaxOpenConns(100)
db.SetMaxIdleConns(20)
db.SetConnMaxLifetime(time.Hour)
```

否则请求并发上来了，可能把下游打爆。

#### 4. 减少热点路径分配

常见手段：

- slice 预分配。
- 复用 buffer。
- 避免频繁 `string` 和 `[]byte` 转换。
- 避免在循环里创建大量临时对象。
- 使用 `sync.Pool` 复用短生命周期对象。

#### 5. 设置缓存上限

缓存必须有容量限制、TTL 或淘汰策略。无上限 map 是高并发服务里很常见的内存风险。

#### 6. 做过载保护

当系统超过承载能力时，要有明确策略：

- 限流。
- 熔断。
- 降级。
- 快速失败。
- 丢弃低优先级任务。
- 返回 429 或 503。

### 面试回答模板

大并发请求最怕无限制：无限 Goroutine、无限队列、无限超时、无限缓存都会导致内存爆炸。所以我的设计会先给系统加边界。

入口层用限流或信号量控制同时处理的请求数，超过直接返回 429 或降级。请求内部使用 `context.WithTimeout`，并且下游 DB、RPC、HTTP 调用都要接收 ctx。异步任务使用 worker pool，worker 数量固定，任务队列有固定大小，队列满时要么快速失败，要么短暂等待后超时。

内存方面，我会避免一次性读取大 body 或大结果集，尽量分页或流式处理；缓存要设置上限和淘汰策略；热点路径减少临时对象分配，必要时用 `sync.Pool` 复用 buffer。最后通过 pprof、runtime metrics、队列长度、Goroutine 数量、GC 指标和 P99 延迟持续观察。

### 常见追问

#### 1. 队列满了怎么办？

常见策略有三种：

- 快速失败，返回 429 或 503。
- 等待一小段时间，超过 timeout 返回失败。
- 丢弃低优先级任务。

不能无限等待，也不能无限扩容队列。

#### 2. 为什么不能每个请求都开一个 Goroutine 慢慢处理？

请求本身已经由 HTTP server 用 Goroutine 处理。如果请求内部再无限制创建 Goroutine，压力大时 Goroutine 数量会快速上涨，占用栈、调度资源和堆内存，还可能增加 GC 压力。应该用 worker pool 或并发限制控制数量。

#### 3. 如何判断内存快爆了？

关注这些指标：

- Goroutine 数量持续增长。
- HeapAlloc 持续增长且 GC 后不下降。
- 队列长度长期接近上限。
- GC 频率和 GC CPU 占比上升。
- P99/P999 延迟变差。
- RSS 接近容器或机器限制。

#### 4. 如何排查是哪里占内存？

使用 pprof：

```bash
go tool pprof http://localhost:6060/debug/pprof/heap
go tool pprof http://localhost:6060/debug/pprof/allocs
go tool pprof http://localhost:6060/debug/pprof/goroutine
```

heap 看当前存活对象，allocs 看累计分配，goroutine 看是否有大量阻塞或泄漏。

#### 5. 如何避免服务关闭时任务丢失？

先停止接收新请求，再等待正在处理的请求和 worker 完成，最后关闭队列。对于必须可靠的任务，不能只依赖内存 channel，需要使用消息队列、数据库状态表或持久化日志。
