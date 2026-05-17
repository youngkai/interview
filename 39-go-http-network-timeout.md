# Go HTTP Client/Server 与网络超时调优

## 30 秒回答

Go HTTP 服务端和客户端调优的核心是连接复用、超时控制和资源保护。服务端要设置 `ReadHeaderTimeout`、`ReadTimeout`、`WriteTimeout`、`IdleTimeout`，防止慢客户端、慢请求体和连接长期占用资源。客户端要复用 `http.Client` 和 `Transport`，不要每次请求新建 client；每次请求必须带 `context timeout`，响应体必须读取或关闭，否则连接无法复用，甚至连接泄漏。

跨境物流系统经常调用海外承运商、海关、支付接口，网络延迟和抖动都更明显。对这类下游调用，要设置连接池、总超时、连接超时、TLS 超时、响应头超时、重试退避、熔断和限流，避免慢下游拖垮自己的服务。

## 高频问题

- Go HTTP Server 每个请求都会开 Goroutine 吗？
- HTTP Server 如何设置超时？
- `ReadTimeout`、`ReadHeaderTimeout`、`WriteTimeout`、`IdleTimeout` 区别？
- 为什么不能每次请求都 new 一个 `http.Client`？
- `Transport` 连接池如何配置？
- 为什么 response body 必须 close？
- 海外第三方接口慢，如何避免拖垮 Go 服务？

## HTTP Server 处理模型

Go `net/http` 服务端会为连接和请求创建 Goroutine 处理。简单理解：

```text
listen socket
  -> accept connection
  -> connection goroutine
  -> request handler
```

这让 Go 写 HTTP 服务很简单，但也意味着：

- 慢连接会占资源。
- 没有超时可能导致 Goroutine 堆积。
- 请求体太大可能导致内存和带宽压力。
- handler 内部再无限开 Goroutine 会放大风险。

## 服务端超时配置

推荐显式配置 `http.Server`，不要直接用 `http.ListenAndServe` 的默认配置。

```go
srv := &http.Server{
    Addr:              ":8080",
    Handler:           router,
    ReadHeaderTimeout: 2 * time.Second,
    ReadTimeout:       10 * time.Second,
    WriteTimeout:      10 * time.Second,
    IdleTimeout:       60 * time.Second,
    MaxHeaderBytes:    1 << 20,
}

if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
    log.Fatal(err)
}
```

## 超时含义

### ReadHeaderTimeout

读取请求头的最大时间。

作用：

- 防止 slowloris 类慢请求头攻击。
- 推荐一定要设置。

### ReadTimeout

读取整个请求，包括 header 和 body 的最大时间。

适合：

- 有请求体的接口。
- 防止客户端慢慢上传拖住连接。

注意：如果有大文件上传，不能简单设置太短，要结合业务设计流式上传和大小限制。

### WriteTimeout

写响应的最大时间。

防止客户端接收很慢导致服务端长期阻塞写响应。

### IdleTimeout

keep-alive 连接空闲多久关闭。

设置太短会降低连接复用，设置太长会占用连接资源。

## 请求体大小限制

不要无限读取 body。

```go
r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10 MB
```

然后再 decode：

```go
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    http.Error(w, "bad request", http.StatusBadRequest)
    return
}
```

跨境物流里，清关资料、附件、原始 payload 可能很大，要限制大小或走对象存储异步上传。

## HTTP Client 为什么要复用

错误方式：

```go
func call(url string) error {
    client := &http.Client{}
    resp, err := client.Get(url)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    return nil
}
```

问题：

- 每次创建 client 和连接，无法充分复用连接池。
- TCP/TLS 握手成本高。
- 高并发下连接数暴涨。

正确方式：全局或按下游复用 client。

```go
var carrierClient = &http.Client{
    Timeout: 3 * time.Second,
    Transport: &http.Transport{
        MaxIdleConns:        1000,
        MaxIdleConnsPerHost: 100,
        MaxConnsPerHost:     200,
        IdleConnTimeout:     90 * time.Second,
        TLSHandshakeTimeout:  3 * time.Second,
    },
}
```

## Transport 参数

### MaxIdleConns

全局最大空闲连接数。

### MaxIdleConnsPerHost

每个 host 最大空闲连接数。调用少数几个第三方接口时，这个值很重要。

### MaxConnsPerHost

每个 host 最大连接数，包括正在使用和空闲连接。可以防止某个下游拖住过多连接。

### IdleConnTimeout

空闲连接保留时间。

### TLSHandshakeTimeout

TLS 握手超时时间。

### ResponseHeaderTimeout

等待响应头的超时时间。适合防止下游迟迟不返回响应。

```go
transport := &http.Transport{
    MaxIdleConns:          1000,
    MaxIdleConnsPerHost:   100,
    MaxConnsPerHost:       200,
    IdleConnTimeout:       90 * time.Second,
    TLSHandshakeTimeout:    3 * time.Second,
    ResponseHeaderTimeout: 2 * time.Second,
    ExpectContinueTimeout: 1 * time.Second,
}
```

## 请求级超时

客户端可以有全局 `Timeout`，每次请求也应该有 context。

```go
ctx, cancel := context.WithTimeout(parent, 2*time.Second)
defer cancel()

req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
if err != nil {
    return err
}

resp, err := carrierClient.Do(req)
```

请求级 context 适合根据业务链路预算控制超时。

## 为什么必须关闭 response body

```go
resp, err := client.Do(req)
if err != nil {
    return err
}
defer resp.Body.Close()
```

如果不关闭：

- 连接无法复用。
- 文件描述符泄漏。
- Goroutine 和连接资源堆积。

如果希望连接复用，通常要读取完 body 或确保 body 被关闭。业务不关心响应体时，也要 drain 一定量后关闭，具体看响应大小和场景。

## 海外接口慢如何保护自己

跨境物流常见慢下游：

- DHL/UPS/FedEx API。
- 海关接口。
- 海外仓接口。
- 支付网关。

保护手段：

- 每个下游独立 `http.Client` 和连接池。
- 每个请求设置 context timeout。
- `MaxConnsPerHost` 限制最大连接。
- worker pool 限制并发。
- 熔断器阻断持续失败接口。
- 指数退避重试。
- 失败进入延迟队列。
- 降级返回“已受理，处理中”。

## 面试回答模板

Go HTTP 服务端我会显式配置 `http.Server` 的超时，包括 `ReadHeaderTimeout` 防慢请求头，`ReadTimeout` 防慢 body，`WriteTimeout` 防慢客户端接收，`IdleTimeout` 控制 keep-alive 空闲连接。请求体也要用 `MaxBytesReader` 限制大小。

客户端侧不会每次请求都新建 `http.Client`，而是按下游复用 client 和 `Transport`，配置 `MaxIdleConns`、`MaxIdleConnsPerHost`、`MaxConnsPerHost`、`IdleConnTimeout`、`TLSHandshakeTimeout`、`ResponseHeaderTimeout`。每次请求用 `context.WithTimeout` 控制总耗时，响应体必须 close，否则连接无法复用。

跨境物流调用海外接口时，下游慢很常见，所以还要配合限流、熔断、重试退避、worker pool 和失败队列，避免慢下游把自己的 Goroutine、连接池和内存拖垮。

