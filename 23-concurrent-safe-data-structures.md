# 并发安全的数据结构

## 30 秒回答

并发安全的数据结构是指多个 Goroutine 同时访问时不会发生数据竞争，也不会破坏内部状态。Go 的普通 map、slice、list 都不是天然并发安全的，需要通过 `sync.Mutex`、`sync.RWMutex`、`sync.Map`、channel、atomic 或分片锁保护。

选择时要看读写比例和操作复杂度：复杂状态用锁，读多写少可以用 RWMutex 或 copy-on-write，高并发 map 可以用分片锁或 sync.Map，简单计数和状态标记可以用 atomic。

## 为什么普通数据结构不安全

以 map 为例，Go 普通 map 并发读写可能直接 panic：

```text
fatal error: concurrent map read and map write
```

原因是 map 内部可能扩容、迁移 bucket、修改哈希结构。多个 Goroutine 同时操作会破坏内部状态。

slice 也类似。多个 Goroutine 同时 append 同一个 slice，可能导致：

- 数据覆盖。
- len/cap 不一致。
- 底层数组扩容时引用混乱。
- 数据竞争。

## 常见实现方式

### 1. Mutex 保护

适合保护复杂状态。

```go
type SafeMap struct {
    mu sync.Mutex
    m  map[string]int
}

func (s *SafeMap) Set(key string, value int) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.m[key] = value
}

func (s *SafeMap) Get(key string) (int, bool) {
    s.mu.Lock()
    defer s.mu.Unlock()
    v, ok := s.m[key]
    return v, ok
}
```

优点：

- 简单。
- 容易维护复杂不变量。

缺点：

- 并发高时锁竞争明显。

### 2. RWMutex 保护

读多写少时可以用读写锁。

```go
type SafeCache struct {
    mu sync.RWMutex
    m  map[string]string
}

func (c *SafeCache) Get(key string) (string, bool) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    v, ok := c.m[key]
    return v, ok
}

func (c *SafeCache) Set(key, value string) {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.m[key] = value
}
```

注意：RWMutex 不一定比 Mutex 快。如果读操作非常短、写也不少，RWMutex 的额外开销可能不划算。

### 3. sync.Map

Go 标准库提供的并发安全 map。

```go
var m sync.Map

m.Store("order:1", "created")
v, ok := m.Load("order:1")
m.Delete("order:1")
```

适合：

- 读多写少。
- key 相对稳定。
- 多 Goroutine 读、少量写。
- 缓存类场景。

不适合：

- 写多。
- 需要复杂事务。
- 需要和其他字段一起保持一致。
- 强类型 API 要求高的场景。

### 4. 分片锁

把一个大 map 拆成多个 shard，每个 shard 一把锁。

```go
type ShardedMap struct {
    shards []shard
}

type shard struct {
    mu sync.RWMutex
    m  map[string]Value
}
```

访问时根据 key hash 选择 shard。

优点：

- 降低单把锁竞争。
- 适合高并发 map。

缺点：

- 实现复杂。
- Range、统计总数、批量操作更麻烦。

### 5. channel 串行化

把所有操作发送给一个 Goroutine，由它独占数据结构。

```go
type request struct {
    key   string
    value int
    reply chan int
}
```

适合：

- 状态机。
- 事件循环。
- 单 owner 模型。

缺点：

- 单 Goroutine 可能成为瓶颈。
- API 设计较复杂。

### 6. atomic

适合简单数值和状态。

```go
var count atomic.Int64

count.Add(1)
v := count.Load()
```

适合：

- 计数器。
- 开关。
- 版本号。
- 指针替换。

不适合：

- 多字段一致性。
- 复杂复合操作。

## 常见并发安全结构

### 并发安全队列

实现方式：

- channel。
- Mutex + slice/list。
- 无锁队列。

Go 中大多数业务场景直接使用 channel 更简单。

### 并发安全缓存

实现方式：

- map + Mutex。
- map + RWMutex。
- 分片 map。
- sync.Map。
- LRU + Mutex。

### 并发安全集合

可以用：

```go
map[string]struct{}
```

再配合锁保护。

## 如何选择

| 场景 | 推荐 |
| --- | --- |
| 普通共享 map | Mutex/RWMutex |
| 读多写少缓存 | RWMutex、sync.Map |
| 高并发大 map | 分片锁 |
| 简单计数 | atomic |
| 生产者消费者 | channel |
| 复杂状态机 | 单 Goroutine owner 或 Mutex |
| LRU 缓存 | map + 双向链表 + Mutex |

## 面试回答模板

Go 的普通 map、slice 不是并发安全的。如果多个 Goroutine 同时访问，必须加同步保护。最常见的是 `map + Mutex/RWMutex`，读多写少可以考虑 RWMutex，key 稳定且读多写少可以用 `sync.Map`，高并发 map 可以做分片锁。

如果是任务队列，Go 里 channel 就是很自然的并发安全队列。如果只是计数器或状态标记，可以用 atomic。复杂对象的多个字段需要保持一致时，不适合用 atomic 拼，应该用锁保护整个临界区。

## 常见追问

### 1. sync.Map 为什么不适合所有场景？

因为它为了特定读多写少场景做了优化，API 也比较弱类型。写多、需要复杂组合操作或需要和其他字段保持一致时，普通 map 加锁更清晰。

### 2. 分片锁为什么能提高并发？

因为不同 key 落到不同 shard，可以并行操作不同 shard，避免所有请求竞争同一把锁。

### 3. atomic 能替代锁吗？

只能替代简单单变量操作。涉及多个字段、多个步骤、复杂不变量时，锁更可靠。

