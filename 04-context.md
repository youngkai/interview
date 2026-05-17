# context 包的使用：超时、取消、请求链路

## 30 秒回答

`context.Context` 用来在请求链路中传递取消信号、超时时间、截止时间和少量请求级元数据。典型场景是 HTTP/RPC 请求超时、用户主动取消、服务关闭时通知下游 Goroutine 退出。

实际开发中，函数一般把 `ctx context.Context` 作为第一个参数。耗时操作、循环、I/O 调用都应该监听 `ctx.Done()`，收到取消后及时返回 `ctx.Err()`。`context` 不应该用来传递可选参数或大对象，也不应该存储业务核心数据。

## context 解决什么问题

在高并发服务中，一个请求可能会启动多个下游调用或后台 Goroutine。如果上游请求已经取消，或者已经超时，下游任务继续执行就会浪费资源，甚至造成 Goroutine 泄漏。

`context` 解决的是请求生命周期传播问题：

```text
HTTP 请求
  -> Service
    -> DB 查询
    -> RPC 调用
    -> 缓存访问
```

当 HTTP 请求取消或超时时，取消信号可以沿着调用链传递到 DB、RPC、缓存等操作。

## context 的核心方法

```go
type Context interface {
    Deadline() (deadline time.Time, ok bool)
    Done() <-chan struct{}
    Err() error
    Value(key any) any
}
```

### Deadline

返回截止时间。如果没有设置 deadline，`ok=false`。

### Done

返回一个只读 channel。context 被取消或超时后，这个 channel 会关闭。

### Err

返回取消原因：

- `context.Canceled`
- `context.DeadlineExceeded`

### Value

获取请求级键值数据，例如 trace id、request id、用户身份信息等。

## 创建 context 的方式

### context.Background

通常作为根 context。

```go
ctx := context.Background()
```

常见于 main 函数、初始化逻辑、测试。

### context.TODO

表示暂时不知道该传什么 context，后续需要替换。

```go
ctx := context.TODO()
```

### WithCancel

手动取消。

```go
ctx, cancel := context.WithCancel(parent)
defer cancel()
```

### WithTimeout

设置相对超时时间。

```go
ctx, cancel := context.WithTimeout(parent, 2*time.Second)
defer cancel()
```

### WithDeadline

设置绝对截止时间。

```go
deadline := time.Now().Add(2 * time.Second)
ctx, cancel := context.WithDeadline(parent, deadline)
defer cancel()
```

### WithValue

传递请求级元数据。

```go
ctx := context.WithValue(parent, requestIDKey{}, requestID)
```

建议 key 使用自定义不可导出类型，避免冲突。

## 为什么要 defer cancel

调用 `WithCancel`、`WithTimeout`、`WithDeadline` 后，应该调用返回的 `cancel`。

```go
ctx, cancel := context.WithTimeout(parent, time.Second)
defer cancel()
```

原因：

- 释放 timer 等资源。
- 让子 context 尽快收到取消信号。
- 避免资源滞留到超时自然发生。

即使函数正常返回，也建议 `defer cancel()`。

## 超时控制示例

```go
func GetUser(ctx context.Context, id int64) (*User, error) {
    ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
    defer cancel()

    user, err := queryUser(ctx, id)
    if err != nil {
        return nil, err
    }
    return user, nil
}
```

下游函数也要接收 ctx：

```go
func queryUser(ctx context.Context, id int64) (*User, error) {
    row := db.QueryRowContext(ctx, "select id, name from users where id = ?", id)
    var user User
    if err := row.Scan(&user.ID, &user.Name); err != nil {
        return nil, err
    }
    return &user, nil
}
```

关键点：使用支持 context 的 API，例如 `QueryRowContext`、`http.NewRequestWithContext`。

## 取消控制示例

```go
func worker(ctx context.Context, jobs <-chan Job) error {
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case job, ok := <-jobs:
            if !ok {
                return nil
            }
            if err := handle(ctx, job); err != nil {
                return err
            }
        }
    }
}
```

这个 worker 能响应：

- 上游取消。
- 超时。
- jobs channel 关闭。

## HTTP 请求中的 context

服务端：

```go
func handler(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()

    user, err := service.GetUser(ctx, 1001)
    if err != nil {
        http.Error(w, err.Error(), http.StatusGatewayTimeout)
        return
    }

    _ = json.NewEncoder(w).Encode(user)
}
```

当客户端断开连接或请求超时，`r.Context()` 会被取消。

客户端：

```go
ctx, cancel := context.WithTimeout(context.Background(), time.Second)
defer cancel()

req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
if err != nil {
    return err
}

resp, err := http.DefaultClient.Do(req)
```

## context.Value 的正确使用

适合放：

- trace id。
- request id。
- auth token 或用户身份摘要。
- 日志字段。

不适合放：

- 大对象。
- 可选参数。
- 业务核心对象。
- 数据库连接。
- 配置对象。

错误倾向：

```go
ctx = context.WithValue(ctx, "pageSize", 20)
```

这种参数应该显式出现在函数参数中。

推荐 key 写法：

```go
type requestIDKey struct{}

func WithRequestID(ctx context.Context, requestID string) context.Context {
    return context.WithValue(ctx, requestIDKey{}, requestID)
}

func RequestIDFromContext(ctx context.Context) string {
    v, _ := ctx.Value(requestIDKey{}).(string)
    return v
}
```

## 常见坑

### 1. 创建 timeout context 后忘记 cancel

```go
ctx, _ := context.WithTimeout(parent, time.Second)
```

这会让 timer 资源直到超时才释放。应该：

```go
ctx, cancel := context.WithTimeout(parent, time.Second)
defer cancel()
```

### 2. 只传 context，但下游不检查

传了 ctx 不代表自动取消。下游必须使用支持 context 的 API，或者主动监听 `ctx.Done()`。

### 3. 把 context 存到 struct 里

通常不建议把 context 长期存在结构体中。更推荐作为函数第一个参数显式传递。

例外是一些生命周期明确的对象，但面试中回答“不要把 context 存进 struct，按调用链传递”更稳。

### 4. 用 context.Value 传业务参数

这会让函数依赖变隐式，降低可读性和可测试性。

## 面试回答模板

`context` 主要解决请求链路中的取消、超时和元数据传递问题。比如一个 HTTP 请求进来后，可能会调用数据库、RPC 和缓存。如果请求已经超时或客户端断开连接，下游继续执行就是浪费资源，所以需要把 `r.Context()` 一路传下去。

我一般会把 `ctx context.Context` 放在函数第一个参数。对于外部调用，会用 `WithTimeout` 设置超时，并且 `defer cancel()` 释放资源。对于循环或 worker，会在 `select` 里监听 `ctx.Done()`，收到取消后返回 `ctx.Err()`。

`context.Value` 我只会放请求级元数据，例如 trace id、request id，不会放业务参数或大对象。因为 context 的重点是生命周期控制，不是参数容器。

## 常见追问

### 1. context 超时后，正在执行的函数会自动停止吗？

不会。context 只是发出取消信号。函数必须主动监听 `ctx.Done()`，或者调用支持 context 的 API，才会及时停止。

### 2. `context.Canceled` 和 `context.DeadlineExceeded` 有什么区别？

`context.Canceled` 表示主动取消，例如调用了 `cancel()`。`context.DeadlineExceeded` 表示超过 deadline 或 timeout。

### 3. 为什么 context 要作为第一个参数？

这是 Go 的惯例，表示它控制当前调用的生命周期。放第一个参数可以让调用链更清晰，也方便统一检查和传递。

### 4. 可以传 nil context 吗？

不应该。没有合适 context 时使用 `context.Background()` 或 `context.TODO()`。

