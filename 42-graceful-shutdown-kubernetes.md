# Go 服务优雅停机与 Kubernetes 部署治理

## 30 秒回答

Go 服务优雅停机的核心是收到 SIGTERM 后停止接收新请求，等待正在处理的请求、MQ 消费、worker 任务在宽限时间内完成，然后关闭数据库、Redis、MQ 等资源。HTTP 服务用 `server.Shutdown(ctx)`，worker 和 consumer 用 context 取消、关闭任务队列、等待 WaitGroup。

在 Kubernetes 中，要配合 readinessProbe、livenessProbe、preStop 和 `terminationGracePeriodSeconds`。收到终止信号后，先让 readiness 失败，从流量入口摘除实例，再执行优雅关闭。否则滚动发布时容易出现请求丢失、消息重复消费或任务处理中断。

## 高频问题

- Go 服务如何优雅关闭？
- 收到 SIGTERM 后怎么处理？
- HTTP Server 如何 shutdown？
- MQ consumer 如何停止消费并处理完已有消息？
- Kubernetes readinessProbe 和 livenessProbe 区别？
- 滚动发布如何避免请求丢失？
- 容器内存限制下 Go GC 如何配置？

## Go 信号处理

```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer stop()

<-ctx.Done()
```

收到 SIGTERM 后开始优雅关闭。

## HTTP Server 优雅关闭

```go
srv := &http.Server{
    Addr:    ":8080",
    Handler: router,
}

go func() {
    if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
        log.Fatal(err)
    }
}()

<-ctx.Done()

shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

if err := srv.Shutdown(shutdownCtx); err != nil {
    log.Printf("http shutdown failed: %v", err)
}
```

`Shutdown` 会：

- 停止接收新连接。
- 关闭空闲连接。
- 等待活跃请求完成，直到 ctx 超时。

注意：长连接、WebSocket、后台 Goroutine 需要额外处理。

## Worker Pool 优雅关闭

```go
type Pool struct {
    jobs   chan Job
    wg     sync.WaitGroup
    ctx    context.Context
    cancel context.CancelFunc
}

func NewPool(workerNum int) *Pool {
    ctx, cancel := context.WithCancel(context.Background())
    p := &Pool{
        jobs:   make(chan Job, 1000),
        ctx:    ctx,
        cancel: cancel,
    }

    for i := 0; i < workerNum; i++ {
        p.wg.Add(1)
        go p.worker()
    }
    return p
}

func (p *Pool) worker() {
    defer p.wg.Done()
    for {
        select {
        case <-p.ctx.Done():
            return
        case job, ok := <-p.jobs:
            if !ok {
                return
            }
            handle(p.ctx, job)
        }
    }
}

func (p *Pool) Stop() {
    p.cancel()
    close(p.jobs)
    p.wg.Wait()
}
```

如果希望处理完队列中已有任务再退出，要先停止接收新任务，再关闭 jobs，让 worker drain 队列。

## MQ Consumer 优雅关闭

核心步骤：

1. 停止拉取新消息。
2. 等待正在处理的消息完成。
3. 成功处理后提交 offset 或 ack。
4. 未完成的消息不提交，让 MQ 后续重投。
5. 关闭消费者连接。

注意：

- 不要一收到 SIGTERM 就直接退出。
- 不要先提交 offset 再处理。
- 要控制 shutdown 最大等待时间。

## Kubernetes 探针

### readinessProbe

表示 Pod 是否可以接收流量。

readiness 失败后，Pod 会从 Service endpoints 中移除。

适合检查：

- 服务是否初始化完成。
- 是否正在优雅关闭。
- 关键依赖是否可用。

### livenessProbe

表示 Pod 是否还活着。

liveness 失败后，Kubernetes 会重启容器。

不要把下游依赖短暂失败放进 liveness，否则可能造成级联重启。

### startupProbe

启动慢的服务可以使用 startupProbe，避免启动阶段被 liveness 误杀。

## 优雅下线流程

推荐流程：

```text
收到 SIGTERM
  -> 标记 shutting down
  -> readiness 返回失败
  -> 等待短暂 drain 时间
  -> 停止接收新请求
  -> HTTP Shutdown
  -> 停止 MQ consumer 拉新消息
  -> 等待 worker 完成
  -> 关闭 DB/Redis/MQ
  -> 进程退出
```

readiness handler 示例：

```go
var shuttingDown atomic.Bool

func readiness(w http.ResponseWriter, r *http.Request) {
    if shuttingDown.Load() {
        http.Error(w, "shutting down", http.StatusServiceUnavailable)
        return
    }
    w.WriteHeader(http.StatusOK)
}
```

收到 SIGTERM：

```go
shuttingDown.Store(true)
time.Sleep(5 * time.Second) // 等待负载均衡摘流量
```

## preStop 和 terminationGracePeriodSeconds

### preStop

容器终止前执行的 hook。

常用于：

- sleep 几秒等待负载均衡摘除。
- 调用服务自己的 drain 接口。

### terminationGracePeriodSeconds

Kubernetes 给容器优雅退出的总时间。超过后会 SIGKILL。

要保证：

```text
preStop 时间 + 应用 shutdown 时间 < terminationGracePeriodSeconds
```

## Go 容器内存和 GOMEMLIMIT

容器有内存限制时，Go runtime 应该知道软内存目标。

可以设置：

```yaml
env:
  - name: GOMEMLIMIT
    value: "1500MiB"
```

如果容器 limit 是 2GiB，可以给 Go 设置略低的 GOMEMLIMIT，为非 Go 内存、栈、mmap、系统开销留空间。

也可以在代码中：

```go
debug.SetMemoryLimit(1500 << 20)
```

注意：

- GOMEMLIMIT 是软限制，不是硬限制。
- 设置太低会导致 GC 频繁。
- 仍要控制缓存、队列和 Goroutine 数量。

## 滚动发布避免请求丢失

关键点：

- readiness 准确反映是否可接流量。
- SIGTERM 后先 readiness fail。
- 给负载均衡传播时间。
- HTTP server 使用 Shutdown。
- MQ consumer 停止拉新消息后再等待处理完成。
- 幂等处理重复消息。

## 面试回答模板

Go 服务优雅停机我会分几步做。收到 SIGTERM 后，先把服务标记为 shutting down，让 readinessProbe 返回失败，等待几秒让 Kubernetes Service 或负载均衡摘除流量。然后调用 HTTP Server 的 `Shutdown(ctx)` 停止接收新连接并等待已有请求完成。

后台 worker 和 MQ consumer 也要处理。consumer 先停止拉新消息，等待正在处理的消息完成，处理成功后再 ack 或提交 offset；未完成的消息不提交，让 MQ 后续重投。worker pool 通过 context 取消、关闭任务队列和 WaitGroup 等待退出。

Kubernetes 侧要配置 readinessProbe、livenessProbe、preStop 和 `terminationGracePeriodSeconds`。liveness 不要依赖下游短暂故障，避免误杀。容器内存受限时，可以设置 `GOMEMLIMIT`，并配合监控观察 GC 和内存。

