# 高级 Go Runtime、gRPC 与 Redis 分布式锁

这篇是高级 Golang 面试追问版，重点覆盖：

- GMP 调度细节：syscall、sysmon、P hand off、work stealing。
- 大规模 Goroutine 控制：worker pool、背压、限流、内存保护。
- GC：三色标记、混合写屏障、逃逸分析、sync.Pool、GOGC、SetMemoryLimit。
- gRPC/Protobuf 高性能微服务通信。
- Redis 分布式锁的 Go 实现、锁续期、看门狗、Redlock 和 fencing token。

## 1. GMP 模型与调度优化

### 面试题

请详细解释 GMP 调度机制。如果一个 Goroutine 发生了系统调用，Go 是如何处理的？在大规模物流数据解析时，如何防止 Goroutine 暴涨导致内存崩塌？

### 30 秒回答

GMP 中 G 是 Goroutine，M 是 OS 线程，P 是执行 Go 代码所需的调度上下文。M 必须持有 P 才能运行 G。P 的数量由 `GOMAXPROCS` 决定，代表 Go 代码的最大并行度。

当 Goroutine 发生阻塞系统调用时，运行它的 M 会进入 syscall 状态，runtime 会把 P 从这个 M 上解绑，交给其他空闲 M 或新建 M 继续执行其他 Goroutine，这就是 P 的 hand off，避免一个阻塞 syscall 占住 P。sysmon 会监控长时间阻塞、网络轮询、抢占和定时器等情况，必要时触发抢占或调度。

在大规模物流数据解析场景，不能为每条数据无限创建 Goroutine。要用 worker pool、有界 channel、批处理、context 取消、背压和限流控制并发，避免 Goroutine 数量、栈内存、队列堆积和 GC 压力失控。

## GMP 关键角色

### G：Goroutine

G 保存：

- Goroutine 栈。
- 当前状态。
- 要执行的函数。
- 调度信息。
- panic/defer 信息。

常见状态：

- runnable：可运行，等待调度。
- running：正在运行。
- waiting：等待 channel、锁、定时器、网络 I/O。
- syscall：系统调用中。
- dead：结束。

### M：Machine

M 是 OS 线程。真正执行 CPU 指令的是 M。

M 可能：

- 执行 Go 代码。
- 执行系统调用。
- 休眠。
- 自旋寻找任务。

### P：Processor

P 是调度器上下文。M 必须绑定 P 才能执行 Go 代码。

P 中包含：

- 本地 runnable 队列。
- 内存分配缓存。
- 定时器相关结构。
- 调度状态。

`GOMAXPROCS` 控制 P 的数量。

## Goroutine 调度流程

典型流程：

1. `go func()` 创建一个 G。
2. 新 G 优先放入当前 P 的本地队列。
3. M 绑定 P，从本地队列取 G 执行。
4. 本地队列为空时，尝试从全局队列取。
5. 全局队列也为空时，从其他 P 的本地队列偷任务，也就是 work stealing。
6. 还没有任务时，检查 netpoller 是否有就绪的网络 I/O。
7. 仍然没有任务时，M 休眠或短暂自旋。

## Work Stealing

每个 P 都有本地队列。某个 P 忙，另一个 P 闲时，空闲 P 会从忙 P 的队列中偷一部分 G。

好处：

- 减少全局队列锁竞争。
- 平衡各 P 负载。
- 提高 CPU 利用率。

面试表达：

> Work stealing 是 Go 调度器的负载均衡机制。某个 P 没有可运行 G 时，会尝试从其他 P 的本地队列偷取一部分 G，而不是马上休眠。

## Goroutine 发生系统调用时 Go 如何处理

系统调用可能阻塞 OS 线程。如果 M 执行 G 时进入 syscall：

```text
G -> syscall
M -> blocked in syscall
P -> detach from M
P -> hand off to another M
```

关键点：

1. G 状态变为 syscall。
2. M 进入系统调用，可能阻塞。
3. runtime 将 P 和 M 解绑。
4. P 被交给其他 M，继续执行其他 G。
5. 系统调用返回后，原 G 需要重新获得 P 才能继续执行。

这样可以避免阻塞系统调用拖住整个 P。

### syscall 返回后发生什么

系统调用返回后：

- 如果原来的 P 仍空闲，M 可能重新绑定 P 继续执行。
- 如果没有空闲 P，G 会被放回可运行队列，M 可能休眠。

### cgo 或长时间 syscall 的风险

cgo 或某些阻塞 syscall 可能长期占用线程。runtime 可以创建更多 M 来维持调度，但线程数量过多也会带来资源压力。

所以在高并发服务中要避免：

- 大量阻塞系统调用。
- 大量 cgo 阻塞调用。
- 在请求链路里做不可控外部阻塞。

## sysmon 的作用

sysmon 是 runtime 的监控线程，不依赖 P 运行。

它会做：

- 监控长时间运行的 G，触发抢占。
- 监控长时间 syscall，协助 P hand off。
- 处理 netpoller 中就绪的网络事件。
- 处理定时器。
- 辅助 GC。

面试中可以这样说：

> sysmon 是 Go runtime 的后台监控线程，它会观察调度器状态，比如长时间运行的 Goroutine、长时间阻塞的系统调用、网络轮询事件和定时器。它可以触发抢占，也可以帮助把因为 syscall 被阻塞的 P 释放出来，让其他 M 继续执行 Go 代码。

## 大规模物流数据解析如何防止 Goroutine 暴涨

### 问题场景

例如一次导入 1000 万条物流轨迹：

```go
for _, row := range rows {
    go parseAndSave(row)
}
```

风险：

- Goroutine 数量暴涨。
- 每个 Goroutine 栈占内存。
- 下游 DB/Redis/MQ 被打爆。
- 队列堆积导致堆内存暴涨。
- GC 扫描压力变大。
- 调度成本升高。

### 正确做法：Worker Pool

```go
func Process(ctx context.Context, rows <-chan Row, workerNum int) error {
    jobs := make(chan Row, workerNum*2)
    var wg sync.WaitGroup

    for i := 0; i < workerNum; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for {
                select {
                case <-ctx.Done():
                    return
                case row, ok := <-jobs:
                    if !ok {
                        return
                    }
                    parseAndSave(ctx, row)
                }
            }
        }()
    }

    for {
        select {
        case <-ctx.Done():
            close(jobs)
            wg.Wait()
            return ctx.Err()
        case row, ok := <-rows:
            if !ok {
                close(jobs)
                wg.Wait()
                return nil
            }
            select {
            case jobs <- row:
            case <-ctx.Done():
                close(jobs)
                wg.Wait()
                return ctx.Err()
            }
        }
    }
}
```

### 关键设计

- worker 数量固定，控制 CPU 和下游压力。
- jobs channel 有界，防止无限堆积。
- context 控制取消。
- 下游 DB/MQ 批量写入。
- 对失败数据进入错误队列或重试队列。

### 并发度如何设置

看任务类型：

- CPU 密集：接近 `GOMAXPROCS` 或 CPU 核数。
- I/O 密集：可以高于 CPU 核数，但要受 DB/MQ/Redis 连接池限制。
- 下游有限流：并发度不能超过下游承载能力。

## 面试回答模板：GMP

GMP 中 G 是 Goroutine，M 是 OS 线程，P 是执行 Go 代码的上下文。M 必须绑定 P 才能运行 G，P 的数量由 `GOMAXPROCS` 决定。每个 P 有本地运行队列，空闲 P 会通过 work stealing 从其他 P 偷任务，减少全局队列竞争。

如果 G 进入系统调用，执行它的 M 可能阻塞。Go runtime 会把 P 从这个 M 上解绑，交给其他 M 继续执行其他 G，这就是 P hand off。sysmon 会监控长时间 syscall、抢占、netpoller 和定时器，保证调度器不会被某个阻塞点拖住。

在大规模物流数据解析中，我不会为每条记录开一个 Goroutine，而是用 worker pool 和有界 channel 控制并发，同时配合 context 取消、批量写入、下游限流和 pprof 监控 Goroutine 数量、堆内存和 GC 压力。

## 2. GC 三色标记、混合写屏障与调优

### 面试题

Go 的三色标记和混合写屏障机制是怎样的？在海量订单路由计算时产生大量小对象，如何进行 GC 调优？你用过哪些工具定位内存泄漏或 CPU 瓶颈？

### 30 秒回答

Go GC 是并发标记清扫。三色标记把对象分为白、灰、黑：白色表示未标记，灰色表示已发现但引用还没扫描完，黑色表示对象和它引用的对象都扫描完成。并发标记期间，用户程序还在修改指针，所以需要写屏障保证不会漏标对象。

Go 使用混合写屏障，核心是结合插入写屏障和删除写屏障的思想，保证并发标记时对象引用变化也能被 GC 正确追踪，同时减少 STW。调优上重点不是手动回收，而是减少分配、减少逃逸、复用对象、降低指针数量、控制缓存和 Goroutine 生命周期，并用 pprof、trace、gctrace、benchmem 定位分配热点和泄漏。

## 三色标记

GC 从根对象开始扫描：

```text
Root -> reachable objects
```

三种颜色：

- 白色：还未标记，标记结束后仍为白色就会被回收。
- 灰色：已经发现，但它引用的对象还没扫描完。
- 黑色：已经扫描完成，确认存活。

标记流程：

1. 根对象标为灰色。
2. 取出灰色对象，扫描它引用的对象。
3. 被引用的白色对象变灰。
4. 当前对象扫描完变黑。
5. 灰色队列为空时，标记结束。
6. 剩余白色对象可以回收。

## 为什么需要写屏障

Go GC 标记阶段和用户 Goroutine 并发执行。用户程序可能在 GC 扫描时修改指针：

```go
a.child = b
```

如果没有写屏障，GC 可能已经把 `a` 扫成黑色，之后 `a` 又引用了一个白色对象 `b`。如果 GC 不知道这个变化，`b` 可能被误回收。

写屏障就是在指针写入时执行一小段 runtime 逻辑，帮助 GC 维护标记正确性。

## 混合写屏障

写屏障常见思想：

- 插入写屏障：关注新写入的指针。
- 删除写屏障：关注被覆盖或删除的旧指针。

Go 的混合写屏障结合两者思想，保证并发标记期间指针变化不会导致漏标，并减少重新扫描栈等 STW 成本。

面试不必背 runtime 细节到源码级，但要讲清楚三点：

1. GC 标记和业务代码并发执行。
2. 并发期间指针引用会变化。
3. 写屏障用于记录或标记这些变化，保证可达对象不被误回收。

## 海量订单路由计算中的 GC 压力

场景：

- 大量订单路由规则匹配。
- 每个订单构造多个临时对象。
- 频繁字符串拼接。
- JSON marshal/unmarshal。
- `[]byte` 和 `string` 来回转换。
- map 临时结构大量创建。

结果：

- 分配速率升高。
- GC 更频繁。
- CPU 被 GC 占用。
- P99 延迟抖动。
- 堆内存上涨。

## GC 调优方向

### 1. 减少分配

预分配 slice：

```go
routes := make([]RouteCandidate, 0, len(rules))
```

避免循环里重复创建临时对象：

```go
for _, order := range orders {
    candidates := make([]RouteCandidate, 0, 16)
    _ = candidates
}
```

如果这个循环非常热，可以考虑复用 buffer。

### 2. 降低逃逸

用逃逸分析查看对象是否逃逸到堆：

```bash
go build -gcflags="-m" ./...
```

常见逃逸原因：

- 返回局部变量指针。
- interface{} 装箱。
- 闭包捕获变量。
- 大对象或不确定大小对象。
- 变量生命周期超出栈帧。

优化思路：

- 避免不必要指针返回。
- 热点路径减少 interface{}。
- 避免闭包捕获大对象。
- 小对象优先值语义。

注意：不要为了“零逃逸”牺牲代码可读性。先用 pprof 确认热点。

### 3. 使用 sync.Pool 复用临时对象

适合复用临时 buffer：

```go
var bufPool = sync.Pool{
    New: func() any {
        return new(bytes.Buffer)
    },
}

func encode(v any) ([]byte, error) {
    buf := bufPool.Get().(*bytes.Buffer)
    buf.Reset()
    defer bufPool.Put(buf)

    if err := json.NewEncoder(buf).Encode(v); err != nil {
        return nil, err
    }

    out := append([]byte(nil), buf.Bytes()...)
    return out, nil
}
```

注意：

- `sync.Pool` 适合临时对象，不适合作为可靠缓存。
- GC 可能清理 pool 内对象。
- 放回 pool 前要 Reset。
- 不要把仍被外部引用的对象放回 pool。

### 4. 减少指针数量

指针越多，GC 扫描成本越高。

例如：

```go
[]Route
```

可能比：

```go
[]*Route
```

更少 GC 扫描压力，但可能增加拷贝成本。要结合对象大小和访问模式判断。

### 5. 控制缓存和队列上限

无上限 map、slice、channel 都可能导致堆增长。

必须有：

- 最大容量。
- TTL。
- 淘汰策略。
- 队列满处理。

### 6. 调整 GOGC

`GOGC` 控制下一次 GC 触发时堆增长比例。

默认通常是：

```text
GOGC=100
```

表示下一次 GC 目标堆大约是上次存活堆的 2 倍。

调高：

- GC 频率下降。
- 内存占用上升。
- GC CPU 可能下降。

调低：

- 内存占用下降。
- GC 更频繁。
- GC CPU 可能上升。

在容器环境中，不要只调 GOGC，还要看内存上限。

### 7. 使用 SetMemoryLimit

Go 提供软内存限制能力：

```go
debug.SetMemoryLimit(2 << 30) // 2 GiB
```

也可以用环境变量：

```bash
GOMEMLIMIT=2GiB
```

作用：

- 告诉 runtime 尽量把内存控制在限制附近。
- 容器环境中比单独 GOGC 更好控制 OOM 风险。

注意：

- 它不是硬限制。
- 业务仍然要控制队列、缓存和对象生命周期。
- 设置太低可能导致 GC 过于频繁，影响吞吐。

## pprof 和 trace 定位问题

### 1. heap profile

查看当前存活对象：

```bash
go tool pprof http://localhost:6060/debug/pprof/heap
```

重点：

- `inuse_space`：当前占用。
- `alloc_space`：累计分配。

泄漏看 `inuse_space`，分配热点看 `alloc_space`。

### 2. allocs profile

累计分配：

```bash
go tool pprof http://localhost:6060/debug/pprof/allocs
```

### 3. CPU profile

```bash
go tool pprof http://localhost:6060/debug/pprof/profile
```

看 CPU 是否消耗在：

- JSON 编解码。
- 正则。
- 加密压缩。
- 锁竞争。
- runtime GC。

### 4. goroutine profile

```bash
go tool pprof http://localhost:6060/debug/pprof/goroutine
```

看是否有大量 Goroutine 阻塞在：

- channel send/receive。
- mutex。
- select。
- net/http。
- database/sql。

### 5. trace

```bash
go test -trace trace.out ./...
go tool trace trace.out
```

或线上采集：

```text
/debug/pprof/trace?seconds=10
```

trace 可以看：

- Goroutine 调度。
- 阻塞原因。
- syscall。
- GC。
- network poll。
- STW。

### 6. benchmem

```bash
go test -bench=. -benchmem
```

关注：

- `ns/op`
- `B/op`
- `allocs/op`

## 面试回答模板：GC

Go GC 是并发标记清扫，核心是三色标记。白色是未标记对象，灰色是已发现但引用还没扫描完，黑色是扫描完成的存活对象。因为标记阶段和用户 Goroutine 并发执行，指针关系会变化，所以 runtime 需要写屏障。Go 的混合写屏障结合插入和删除写屏障的思想，保证并发标记期间不会漏标对象，同时减少 STW。

如果海量订单路由计算产生大量小对象，我会先用 pprof 和 benchmem 找分配热点，而不是直接调 GC 参数。优化手段包括 slice 预分配、减少临时对象、减少不必要的 interface 装箱和逃逸、复用 buffer、使用 sync.Pool、降低指针数量、控制队列和缓存上限。

参数上可以根据压测结果调整 GOGC，容器环境还可以用 `GOMEMLIMIT` 或 `debug.SetMemoryLimit` 控制内存目标。但这些是最后的调参，根本还是要降低分配速率和存活对象数量。

## 3. 高性能微服务通信：gRPC / Protobuf

### 面试题

你们的微服务之间是如何通信的？为什么选择 gRPC / Protobuf？在高并发下，如何设计 gRPC 的连接池和超时间隔？

### 30 秒回答

内部微服务高频调用适合使用 gRPC + Protobuf，因为它有强类型 IDL、代码生成、二进制序列化、HTTP/2 多路复用和流式能力。对外开放接口可以继续用 HTTP REST，内部服务间调用用 gRPC 提升契约管理和性能。

gRPC 的 `ClientConn` 本身是并发安全的，并且基于 HTTP/2 支持多路复用，通常不应该每次请求都新建连接，而是复用长连接。如果单个连接受最大并发 stream、负载均衡或热点影响，可以维护少量连接池。所有 RPC 必须设置 deadline/timeout，配合重试、熔断、限流和连接 keepalive。

## 为什么选择 gRPC / Protobuf

### 1. 强类型契约

接口由 `.proto` 定义：

```proto
service InventoryService {
  rpc ReserveStock(ReserveStockRequest) returns (ReserveStockResponse);
}
```

调用方和服务方通过代码生成共享契约，减少文档不一致。

### 2. 性能较好

Protobuf 是二进制编码，通常比 JSON 更小、更快。

### 3. HTTP/2 多路复用

一个连接上可以并发多个 stream，减少连接数量。

### 4. 支持流式通信

适合：

- 大批量物流轨迹同步。
- 实时状态流。
- 分片数据传输。

### 5. 适合内部多语言服务

不同语言都可以通过 proto 生成代码。

## gRPC 连接管理

### ClientConn 要复用

错误方式：

```go
func Call(ctx context.Context, req *pb.Request) error {
    conn, err := grpc.Dial(addr)
    if err != nil {
        return err
    }
    defer conn.Close()

    client := pb.NewServiceClient(conn)
    _, err = client.Do(ctx, req)
    return err
}
```

问题：

- 每次建连接成本高。
- TLS 握手成本高。
- 连接数暴涨。
- 延迟和资源占用上升。

正确方式：

```go
type Client struct {
    conn   *grpc.ClientConn
    client pb.InventoryServiceClient
}
```

进程启动时初始化，长期复用。

### 是否需要连接池

gRPC 的 `ClientConn` 并发安全，并且支持 HTTP/2 多路复用。很多场景一个 `ClientConn` 足够。

需要多个连接的情况：

- 单连接最大并发 stream 成为瓶颈。
- 负载均衡策略需要多个子连接。
- 高 QPS 下单连接出现排队。
- 想隔离不同优先级流量。

连接池不是越大越好，过大会增加服务端连接压力。

## 超时设计

每个 RPC 都必须有 deadline：

```go
ctx, cancel := context.WithTimeout(parent, 200*time.Millisecond)
defer cancel()

resp, err := client.ReserveStock(ctx, req)
```

超时要根据业务链路预算设计。

例如下单接口总超时 800ms：

```text
订单参数校验 50ms
库存预占 200ms
价格计算 150ms
订单写库 200ms
预留 buffer 200ms
```

不要所有下游都设置很长超时，否则链路会堆积。

## 重试策略

重试只适合幂等请求或明确可重试错误。

适合重试：

- UNAVAILABLE。
- DEADLINE_EXCEEDED 的部分场景。
- 临时网络错误。

不适合盲目重试：

- 库存扣减。
- 支付扣款。
- 创建运单。

除非接口有 idempotency key。

重试要有：

- 最大次数。
- 退避。
- jitter。
- 总 deadline。
- 熔断。

## Keepalive

keepalive 用于探测连接是否可用，但不能设置过于激进，否则会给服务端造成额外压力。

要和服务端配置匹配。

## 服务发现和负载均衡

内部 gRPC 调用常见：

- DNS。
- Consul/Etcd/Nacos。
- Kubernetes Service。
- xDS/service mesh。

负载均衡策略：

- pick_first。
- round_robin。
- xDS 策略。

## 高并发保护

### 客户端保护

- 每个下游设置并发上限。
- 设置 deadline。
- 连接复用。
- 熔断和限流。
- 指标监控。

### 服务端保护

- 限制最大并发 RPC。
- 限制请求体大小。
- worker pool 或资源池隔离。
- 拦截器做鉴权、限流、日志、trace。
- 慢请求监控。

## 面试回答模板：gRPC

内部微服务我会优先考虑 gRPC + Protobuf，原因是它有强类型契约、代码生成、二进制序列化、HTTP/2 多路复用和流式能力，适合订单、库存、支付、清关这些内部高频调用。对外 API 仍然可以用 REST，因为开放性和调试更友好。

连接上，gRPC 的 `ClientConn` 是并发安全的，底层 HTTP/2 支持多路复用，所以不会每次请求都 Dial，而是在进程启动时创建并复用。高 QPS 下如果单连接 stream 限制或负载均衡成为瓶颈，可以维护少量连接或使用 gRPC resolver/balancer。

每个 RPC 必须设置 deadline，并按链路预算分配超时时间。重试只对幂等接口和可重试错误开启，库存扣减、支付这类接口必须有 idempotency key。高并发下还要有客户端限流、熔断、服务端最大并发控制、连接 keepalive 和完整的 metrics/tracing。

## 4. Redis 分布式锁的 Go 实现

### 面试题

物流库存扣减、运单状态更新需要分布式锁。如果用 Go 实现基于 Redis 的分布式锁，如何解决“锁过期但业务没执行完”的问题？红锁或看门狗机制怎么理解？

### 30 秒回答

Redis 分布式锁基础实现是 `SET key value NX PX ttl`，value 用唯一 token 标识持锁者，释放时用 Lua 校验 token 后删除，避免误删别人的锁。锁过期但业务没执行完的问题，可以通过合理设置 TTL、缩短临界区、看门狗自动续期解决。

但 Redis 锁不能作为强一致的唯一保障。对于库存扣减、运单状态更新，最终还要用数据库条件更新、唯一约束、状态机或 fencing token 防止旧持锁者在锁过期后继续写入造成并发覆盖。Redlock 可以提高多 Redis 节点场景的容错，但在强一致场景仍要谨慎评估。

## 基础加锁

```text
SET lock:shipment:1001 token NX PX 10000
```

含义：

- `NX`：不存在才设置。
- `PX 10000`：10 秒过期。
- `token`：唯一随机值，标识锁持有者。

Go 伪代码：

```go
func Lock(ctx context.Context, rdb *redis.Client, key string, ttl time.Duration) (string, bool, error) {
    token := uuid.NewString()
    ok, err := rdb.SetNX(ctx, key, token, ttl).Result()
    if err != nil {
        return "", false, err
    }
    return token, ok, nil
}
```

## 安全释放锁

不能直接：

```text
DEL lock:key
```

因为锁可能已经过期并被别人拿到。

正确做法：校验 token 后删除。

```lua
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
end
return 0
```

Go 伪代码：

```go
const unlockScript = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
end
return 0
`

func Unlock(ctx context.Context, rdb *redis.Client, key, token string) error {
    return rdb.Eval(ctx, unlockScript, []string{key}, token).Err()
}
```

## 锁过期但业务没执行完的问题

### 问题过程

```text
T1 获取锁，TTL=10s
T1 执行业务卡住 15s
锁在第 10s 过期
T2 获取同一把锁
T1 恢复执行，继续写数据库
T2 也在写数据库
```

此时互斥被破坏。

## 解决方案一：合理 TTL + 缩短临界区

锁内只做必要操作，不做慢 RPC 和不可控 I/O。

错误：

```text
加锁
  调用承运商 API
  调用支付
  写数据库
释放锁
```

更好：

```text
调用外部 API
加锁
  检查状态
  条件更新数据库
释放锁
```

## 解决方案二：看门狗自动续期

看门狗机制：

- 加锁成功后启动后台 Goroutine。
- 每隔 TTL 的一部分时间检查锁是否仍属于自己。
- 如果 token 匹配，就延长 TTL。
- 业务完成后停止续期并释放锁。

Lua 续期：

```lua
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 0
```

Go 伪代码：

```go
func Watchdog(ctx context.Context, rdb *redis.Client, key, token string, ttl time.Duration) {
    ticker := time.NewTicker(ttl / 3)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            _ = rdb.Eval(ctx, renewScript, []string{key}, token, ttl.Milliseconds()).Err()
        }
    }
}
```

注意：

- 续期必须校验 token。
- 业务结束要停止 watchdog。
- watchdog 只能缓解业务超时问题，不能解决所有一致性问题。
- 如果进程长时间 STW、网络隔离或 Redis 故障，仍可能续期失败。

## 解决方案三：Fencing Token

更严谨的做法是引入 fencing token。

每次获取锁时，Redis 或数据库生成一个单调递增 token：

```text
lock token = 101
下一次 lock token = 102
```

下游资源只接受更大的 token：

```sql
UPDATE shipment
SET status = ?, fence_token = ?
WHERE shipment_id = ?
  AND fence_token < ?;
```

这样即使旧持锁者 T1 在锁过期后恢复，它拿的是旧 token，写入会被拒绝。

这是解决“旧锁持有者复活写入”的关键手段。

## Redlock

Redlock 思路：

- 向多个独立 Redis 节点尝试加锁。
- 在多数节点加锁成功，并且耗时小于锁 TTL，认为加锁成功。
- 释放时释放所有节点。

它的目标是避免单 Redis 节点故障导致锁安全性下降。

但面试中要说清楚：

- Redlock 比单节点锁复杂。
- 是否足够安全在业界有争议。
- 强一致场景不能只依赖 Redlock。
- 库存、支付、状态更新仍要有数据库约束、状态机或 fencing token。

## Go 分布式锁实现注意点

### 1. token 必须唯一

使用 UUID 或高质量随机数。

### 2. 加锁要有等待超时

不能无限等待锁。

```go
ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
defer cancel()
```

### 3. 解锁必须 defer，但要处理业务结果

```go
token, ok, err := Lock(ctx, rdb, key, 10*time.Second)
if err != nil || !ok {
    return err
}
defer Unlock(context.Background(), rdb, key, token)
```

释放锁可以用独立短 timeout，避免继承已取消 ctx 导致解锁失败。

### 4. 业务必须有兜底

例如运单状态更新：

```sql
UPDATE shipment
SET status = 'DELIVERED'
WHERE shipment_id = ?
  AND status IN ('SHIPPED', 'OUT_FOR_DELIVERY');
```

即使用了锁，也要用状态机条件保护。

## 面试回答模板：Redis 分布式锁

Redis 分布式锁我会用 `SET key value NX PX ttl` 实现，value 是唯一 token，释放锁时用 Lua 判断 token 一致才删除，避免锁过期后误删别人的锁。

锁过期但业务没执行完有几种处理。第一是锁内逻辑尽量短，不在锁里做慢 RPC；第二是设置合理 TTL；第三是加看门狗续期，后台定期校验 token 并延长 TTL。但看门狗也不是绝对安全，进程卡顿、网络隔离、Redis 故障都可能导致续期失败。

对于库存扣减、运单状态更新这类核心业务，我不会只靠 Redis 锁。数据库层还要有条件更新、唯一约束、状态机，必要时用 fencing token 防止旧持锁者在锁过期后继续写入。Redlock 可以提高多 Redis 节点下的容错，但强一致场景仍要有业务层兜底。

## 高频追问

### 1. Redis 锁释放为什么要用 Lua？

因为判断 token 和删除锁必须是原子操作。如果先 GET 再 DEL，中间锁可能过期并被别人拿到，导致误删。

### 2. 看门狗能完全解决锁过期问题吗？

不能。它能在业务执行超过 TTL 时自动续期，但如果进程暂停、网络故障、Redis 故障或续期失败，锁仍可能过期。所以核心一致性还要靠数据库状态机或 fencing token。

### 3. 库存扣减一定要分布式锁吗？

不一定。很多库存扣减可以直接用数据库条件更新或 Redis Lua 原子扣减。分布式锁可以减少并发冲突，但不能替代原子扣减和幂等。

### 4. gRPC 高并发下为什么不每次 Dial？

因为 Dial 成本高，连接建立、TLS 握手、HTTP/2 初始化都会增加延迟和资源占用。`ClientConn` 并发安全，应该复用。

### 5. GC 调优先调 GOGC 吗？

不是。先用 pprof、trace、benchmem 找到分配热点和存活对象，再减少分配、复用对象、控制缓存和队列。GOGC/GOMEMLIMIT 是压测后的参数调优，不是第一步。
