# 高并发下的锁优化

## 30 秒回答

Go 中常见同步手段包括 `sync.Mutex`、`sync.RWMutex` 和 `sync/atomic`。`Mutex` 适合保护普通临界区；`RWMutex` 适合读多写少，并且读操作耗时相对明显的场景；`atomic` 适合简单计数、状态标记、指针替换等非常小的原子操作。

锁优化的核心不是盲目换成无锁，而是减少锁竞争、缩小临界区、降低锁粒度、避免锁内 I/O、分片、读写分离、copy-on-write，并用 pprof、trace、mutex profile 验证瓶颈。

## Mutex

`sync.Mutex` 是互斥锁，同一时间只允许一个 Goroutine 进入临界区。

```go
var mu sync.Mutex
var count int

func Inc() {
    mu.Lock()
    defer mu.Unlock()
    count++
}
```

适合：

- 保护 map。
- 保护结构体内部状态。
- 写多读多都比较频繁的共享资源。
- 临界区短小的场景。

注意：

- 不要复制已经使用过的 Mutex。
- Lock 后必须 Unlock。
- 避免锁内执行慢 I/O。

## RWMutex

`sync.RWMutex` 是读写锁，允许多个读锁并发，但写锁独占。

```go
var mu sync.RWMutex
var cache = map[string]string{}

func Get(key string) (string, bool) {
    mu.RLock()
    defer mu.RUnlock()
    v, ok := cache[key]
    return v, ok
}

func Set(key, value string) {
    mu.Lock()
    defer mu.Unlock()
    cache[key] = value
}
```

适合：

- 读多写少。
- 读临界区不是极短。
- 允许多个读并发有明显收益。

不适合：

- 写很多。
- 读操作极短，RWMutex 额外开销可能不划算。
- 写锁长时间等待导致整体延迟升高。

## atomic

`sync/atomic` 提供原子操作，适合简单共享变量。

```go
var count atomic.Int64

func Inc() {
    count.Add(1)
}

func Load() int64 {
    return count.Load()
}
```

适合：

- 计数器。
- 开关状态。
- 版本号。
- 指针原子替换。
- 低复杂度无锁读。

不适合：

- 多字段一致性更新。
- 复杂不变量。
- 需要组合多个操作保持原子性。

错误倾向：

```go
if atomic.LoadInt64(&count) > 0 {
    atomic.AddInt64(&count, -1)
}
```

这两个操作组合起来不是原子的，可能出现竞态。需要 CAS 或锁。

## Mutex、RWMutex、atomic 如何选择

| 场景 | 推荐 |
| --- | --- |
| 保护 map 或复杂结构体 | Mutex/RWMutex |
| 读多写少缓存 | RWMutex、分片锁、copy-on-write |
| 高频计数 | atomic |
| 多字段要保持一致 | Mutex |
| 状态开关 | atomic |
| 临界区包含复杂逻辑 | Mutex |
| 极高并发 map | 分片 map 或 sync.Map |

一句话：复杂状态用锁，简单数值用 atomic，读多写少再考虑 RWMutex。

## 锁优化思路

### 1. 缩小临界区

不要把耗时操作放在锁里。

错误：

```go
mu.Lock()
defer mu.Unlock()

data := loadFromDB()
cache[key] = data
```

优化：

```go
data := loadFromDB()

mu.Lock()
cache[key] = data
mu.Unlock()
```

注意：实际缓存场景还要处理并发重复加载，可以结合 singleflight。

### 2. 降低锁粒度

一个大锁保护所有数据，竞争可能很高。可以拆成多个锁。

```go
type ShardedMap struct {
    shards []shard
}

type shard struct {
    mu sync.RWMutex
    m  map[string]Value
}
```

根据 key hash 定位 shard，降低多个 key 之间的竞争。

### 3. 读写分离

读多写少时可以使用 `RWMutex`，让多个读并发。

但要压测验证，因为 `RWMutex` 本身比 `Mutex` 更复杂。

### 4. copy-on-write

适合读非常多、写很少的配置或路由表。

```go
var current atomic.Value // stores map[string]Config

func Get(key string) (Config, bool) {
    m := current.Load().(map[string]Config)
    v, ok := m[key]
    return v, ok
}

func Update(key string, value Config) {
    old := current.Load().(map[string]Config)
    next := make(map[string]Config, len(old)+1)
    for k, v := range old {
        next[k] = v
    }
    next[key] = value
    current.Store(next)
}
```

读路径无锁，写路径复制整份数据。适合写少且数据量可控的场景。

### 5. 使用 singleflight 合并重复请求

缓存击穿时，大量请求可能同时加载同一个 key。

```go
// golang.org/x/sync/singleflight
```

它可以把同一个 key 的并发请求合并成一次下游调用。

### 6. 避免锁顺序死锁

如果必须拿多把锁，要固定顺序。

错误风险：

```text
G1: lock A -> lock B
G2: lock B -> lock A
```

这可能导致死锁。

### 7. 避免复制锁

```go
type Counter struct {
    mu sync.Mutex
    n  int
}

func (c Counter) Inc() {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.n++
}
```

这里值接收者会复制 `Counter`，也复制了锁。应该用指针接收者：

```go
func (c *Counter) Inc() {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.n++
}
```

## sync.Map

`sync.Map` 是并发安全 map，但不是普通 map 加锁的万能替代。

适合：

- 读多写少。
- key 相对稳定。
- 多 Goroutine 读、少量写。
- 缓存类场景。

不适合：

- 写多。
- 需要复杂事务逻辑。
- 需要和其他字段保持一致。
- 需要稳定类型约束和复杂操作。

普通业务中，`map + RWMutex` 往往更清晰。

## 锁问题排查

### 1. race detector

```bash
go test -race ./...
```

用于发现数据竞争。

### 2. mutex profile

在程序中开启：

```go
runtime.SetMutexProfileFraction(1)
```

然后通过 pprof 查看锁竞争。

### 3. block profile

```go
runtime.SetBlockProfileRate(1)
```

查看 Goroutine 阻塞情况，包括 channel、select、锁等。

### 4. go tool trace

可以观察调度、阻塞、系统调用、GC 等。

### 5. benchmark

```bash
go test -bench=. -benchmem
```

不同锁方案必须压测验证，不要只凭直觉。

## 常见错误

### 1. 认为 RWMutex 一定比 Mutex 快

不一定。读操作很短、写操作不少时，RWMutex 的额外开销可能比收益更大。

### 2. 锁内调用外部系统

锁内访问 DB、RPC、磁盘、网络，会导致锁持有时间不可控。

### 3. atomic 滥用

atomic 只能保证单个原子操作的原子性。多个 atomic 操作组合起来不自动具备事务语义。

### 4. 忘记 Unlock

优先使用：

```go
mu.Lock()
defer mu.Unlock()
```

在热点路径里可以手动 Unlock，但要保证所有分支都释放。

### 5. 并发读写普通 map

普通 map 并发读写不安全，必须保护。

## 面试回答模板

高并发下锁优化我会先判断共享状态的复杂度和读写比例。普通共享状态用 `Mutex`，读多写少可以考虑 `RWMutex`，简单计数或状态标记用 `atomic`。如果是高并发 map，可以考虑分片锁、`sync.Map` 或 copy-on-write。

优化时我不会一上来追求无锁，而是先用 pprof、mutex profile、block profile 或 trace 找到竞争点。常见手段是缩小临界区，避免锁内 I/O，降低锁粒度，按 key 分片，读写分离，或者用 singleflight 合并重复请求。

`RWMutex` 和 `atomic` 都不是万能的。`RWMutex` 只有读多写少且读临界区有一定成本时才明显有收益；atomic 适合简单变量，不适合维护复杂不变量。

## 常见追问

### 1. Mutex 和 RWMutex 怎么选？

如果读多写少，并且读临界区有一定耗时，可以考虑 RWMutex。否则 Mutex 往往更简单，性能也可能更好。最终要靠 benchmark 验证。

### 2. atomic 为什么容易写错？

因为 atomic 只保证单次操作原子。如果业务逻辑需要“先判断再修改”这种复合操作，就可能仍然有竞态，需要 CAS 循环或锁。

### 3. sync.Map 适合什么场景？

适合读多写少、key 稳定、缓存类场景。不适合复杂更新、不适合需要和其他字段保持一致的场景。

### 4. 怎么发现锁竞争？

可以用 mutex profile、block profile、go tool trace 和 benchmark。线上可以结合延迟、CPU、Goroutine 阻塞数量、pprof 数据一起判断。

