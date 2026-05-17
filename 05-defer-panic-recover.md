# defer、panic、recover 的实际应用

## 30 秒回答

`defer` 用于延迟执行，常见于释放资源、解锁、关闭文件、记录耗时和统一收尾。它会在当前函数返回前执行，多个 defer 按后进先出的顺序执行。

`panic` 表示程序遇到不可恢复的严重错误，会中断当前执行流程并开始栈展开。`recover` 只能在 defer 函数中捕获当前 Goroutine 的 panic。实际项目中，panic/recover 不应该替代普通错误处理，通常用于框架边界兜底，例如 HTTP middleware 防止单个请求 panic 导致进程退出。

## defer 的执行规则

### 1. 函数返回前执行

```go
func f() {
    defer fmt.Println("defer")
    fmt.Println("body")
}
```

输出：

```text
body
defer
```

### 2. 多个 defer 后进先出

```go
func f() {
    defer fmt.Println(1)
    defer fmt.Println(2)
    defer fmt.Println(3)
}
```

输出：

```text
3
2
1
```

### 3. defer 的参数会立即求值

```go
func f() {
    x := 1
    defer fmt.Println(x)
    x = 2
}
```

输出是 `1`，因为 `defer fmt.Println(x)` 注册时参数已经求值。

如果用闭包：

```go
func f() {
    x := 1
    defer func() {
        fmt.Println(x)
    }()
    x = 2
}
```

输出是 `2`，因为闭包捕获变量本身。

### 4. defer 可以修改命名返回值

```go
func f() (err error) {
    defer func() {
        if err != nil {
            err = fmt.Errorf("wrap: %w", err)
        }
    }()

    return errors.New("failed")
}
```

返回值会被 defer 修改。

## defer 的实际应用

### 1. 释放资源

```go
f, err := os.Open(name)
if err != nil {
    return err
}
defer f.Close()
```

### 2. 解锁

```go
mu.Lock()
defer mu.Unlock()
```

这样可以避免中间 return 时忘记解锁。

### 3. 记录耗时

```go
func handle() {
    start := time.Now()
    defer func() {
        log.Printf("cost=%s", time.Since(start))
    }()

    doWork()
}
```

### 4. 事务回滚兜底

```go
tx, err := db.BeginTx(ctx, nil)
if err != nil {
    return err
}
defer tx.Rollback()

if err := update(ctx, tx); err != nil {
    return err
}

return tx.Commit()
```

如果 `Commit` 成功，后续 `Rollback` 通常会返回错误但不会影响已提交事务。实际项目也可以在 defer 中根据状态控制回滚。

## defer 常见坑

### 1. 在循环中 defer 关闭资源

```go
for _, name := range files {
    f, err := os.Open(name)
    if err != nil {
        return err
    }
    defer f.Close()
}
```

这些文件会等函数结束才关闭，如果循环很多，会导致文件描述符占用过高。

更好的方式是拆函数：

```go
for _, name := range files {
    if err := processFile(name); err != nil {
        return err
    }
}

func processFile(name string) error {
    f, err := os.Open(name)
    if err != nil {
        return err
    }
    defer f.Close()

    return handle(f)
}
```

### 2. defer 中忽略 Close 错误

写文件时 `Close` 可能刷新缓冲并返回错误，不能总是忽略。

```go
func writeFile(name string, data []byte) (err error) {
    f, err := os.Create(name)
    if err != nil {
        return err
    }
    defer func() {
        if closeErr := f.Close(); err == nil && closeErr != nil {
            err = closeErr
        }
    }()

    _, err = f.Write(data)
    return err
}
```

### 3. defer 性能问题

现代 Go 对 defer 做了很多优化，普通场景可以放心使用。但在极高频、极小函数、热点循环里，仍然要关注 defer 开销。性能敏感路径应该用 benchmark 和 pprof 判断，而不是凭感觉优化。

## panic 的作用

`panic` 用于表示严重异常。触发 panic 后，当前函数停止正常执行，开始执行 defer，并继续向上层调用栈传播。

```go
func f() {
    defer fmt.Println("defer")
    panic("bad")
}
```

适合 panic 的场景：

- 程序启动时配置严重错误。
- 不应该发生的内部状态错误。
- 框架或库内部遇到无法继续的严重问题。

不适合 panic 的场景：

- 普通业务错误。
- 参数校验失败。
- 数据库查询失败。
- 下游接口超时。

这些应该返回 `error`。

## recover 的规则

`recover` 只能在 defer 函数中生效。

```go
func safeCall() {
    defer func() {
        if r := recover(); r != nil {
            fmt.Println("recovered:", r)
        }
    }()

    panic("boom")
}
```

错误写法：

```go
func bad() {
    recover() // 无效
    panic("boom")
}
```

### recover 只能捕获当前 Goroutine 的 panic

```go
func main() {
    defer func() {
        recover()
    }()

    go func() {
        panic("boom")
    }()

    time.Sleep(time.Second)
}
```

main Goroutine 的 recover 捕获不到新 Goroutine 里的 panic。每个 Goroutine 需要自己做 recover 边界。

## recover 的实际应用

### 1. HTTP middleware 兜底

```go
func RecoverMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        defer func() {
            if rec := recover(); rec != nil {
                log.Printf("panic: %v", rec)
                http.Error(w, "internal server error", http.StatusInternalServerError)
            }
        }()

        next.ServeHTTP(w, r)
    })
}
```

作用：避免单个请求 panic 影响整个服务进程。

### 2. Goroutine 启动包装

```go
func GoSafe(fn func()) {
    go func() {
        defer func() {
            if rec := recover(); rec != nil {
                log.Printf("panic: %v", rec)
            }
        }()

        fn()
    }()
}
```

适合后台任务兜底，但不能吞掉重要错误，至少要记录日志和指标。

### 3. 保证清理逻辑执行

panic 发生时 defer 仍会执行，所以可以确保释放锁、关闭资源、上报指标等。

## 面试回答模板

`defer` 是 Go 的延迟执行机制，通常用于释放资源、解锁、关闭文件、事务回滚和记录耗时。它在函数返回前执行，多个 defer 按后进先出执行，参数会在注册 defer 时立即求值。

`panic` 用来表示不可恢复的严重错误，会触发栈展开并执行沿途 defer。`recover` 只能在 defer 中调用，并且只能捕获当前 Goroutine 的 panic。

实际项目里，我不会用 panic/recover 代替正常 error 处理。业务错误应该返回 error。recover 更多用于框架边界，比如 HTTP middleware 或 Goroutine 启动器，防止 panic 打崩整个进程，同时记录日志、堆栈和指标。

## 常见追问

### 1. defer 在 return 前还是后执行？

准确说，return 语句会先给返回值赋值，然后执行 defer，最后函数真正返回。所以 defer 可以修改命名返回值。

### 2. recover 为什么必须放在 defer 里？

因为 panic 发生后正常控制流已经中断，只有 defer 会在栈展开过程中执行。recover 设计上只能在这个阶段捕获 panic。

### 3. 子 Goroutine panic，父 Goroutine 能 recover 吗？

不能。recover 只对当前 Goroutine 生效。每个 Goroutine 都需要自己的 recover 边界。

### 4. panic 被 recover 后程序会继续从哪里执行？

如果 recover 成功，当前函数不会回到 panic 的位置继续执行，而是结束当前函数，返回给调用方。

