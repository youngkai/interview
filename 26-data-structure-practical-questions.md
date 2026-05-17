# 数据结构和分布式算法实战题

## 题目 1：实现一个支持并发安全的 LRU 缓存

### 30 秒回答

LRU 缓存要求 `Get` 和 `Put` 尽量 O(1)，并且容量满时淘汰最久未使用的数据。典型实现是哈希表加双向链表：哈希表根据 key O(1) 找到节点，双向链表维护访问顺序，链表头表示最近访问，链表尾表示最久未访问。

并发安全可以用 `sync.Mutex` 保护整个缓存结构。因为 `Get` 虽然看起来是读操作，但它会把节点移动到链表头，本质上会修改链表，所以不能只加读锁。简单可靠的做法是 `Get` 和 `Put` 都加互斥锁。

## 设计要点

### 1. 数据结构

```text
map[key]*node
双向链表 head <-> ... <-> tail
```

每个节点保存：

- key。
- value。
- prev。
- next。

为什么节点里要保存 key？

因为淘汰尾节点时，需要从 map 中删除对应 key。

### 2. 操作复杂度

| 操作 | 复杂度 |
| --- | --- |
| Get | O(1) |
| Put | O(1) |
| 删除节点 | O(1) |
| 移动到头部 | O(1) |
| 淘汰尾部 | O(1) |

### 3. 并发安全

需要锁保护：

- map。
- 双向链表。
- size。

`Get` 也要加写锁，因为它会更新访问顺序。

## Go 实现

```go
package lru

import "sync"

type entry[K comparable, V any] struct {
    key   K
    value V
    prev  *entry[K, V]
    next  *entry[K, V]
}

type Cache[K comparable, V any] struct {
    mu       sync.Mutex
    capacity int
    items    map[K]*entry[K, V]
    head     *entry[K, V]
    tail     *entry[K, V]
}

func New[K comparable, V any](capacity int) *Cache[K, V] {
    if capacity <= 0 {
        panic("capacity must be positive")
    }

    c := &Cache[K, V]{
        capacity: capacity,
        items:    make(map[K]*entry[K, V], capacity),
    }

    c.head = &entry[K, V]{}
    c.tail = &entry[K, V]{}
    c.head.next = c.tail
    c.tail.prev = c.head

    return c
}

func (c *Cache[K, V]) Get(key K) (V, bool) {
    c.mu.Lock()
    defer c.mu.Unlock()

    node, ok := c.items[key]
    if !ok {
        var zero V
        return zero, false
    }

    c.moveToFront(node)
    return node.value, true
}

func (c *Cache[K, V]) Put(key K, value V) {
    c.mu.Lock()
    defer c.mu.Unlock()

    if node, ok := c.items[key]; ok {
        node.value = value
        c.moveToFront(node)
        return
    }

    node := &entry[K, V]{
        key:   key,
        value: value,
    }
    c.items[key] = node
    c.addToFront(node)

    if len(c.items) > c.capacity {
        oldest := c.removeTail()
        delete(c.items, oldest.key)
    }
}

func (c *Cache[K, V]) Len() int {
    c.mu.Lock()
    defer c.mu.Unlock()
    return len(c.items)
}

func (c *Cache[K, V]) moveToFront(node *entry[K, V]) {
    c.remove(node)
    c.addToFront(node)
}

func (c *Cache[K, V]) addToFront(node *entry[K, V]) {
    node.prev = c.head
    node.next = c.head.next
    c.head.next.prev = node
    c.head.next = node
}

func (c *Cache[K, V]) remove(node *entry[K, V]) {
    node.prev.next = node.next
    node.next.prev = node.prev
    node.prev = nil
    node.next = nil
}

func (c *Cache[K, V]) removeTail() *entry[K, V] {
    node := c.tail.prev
    c.remove(node)
    return node
}
```

## 为什么不用 RWMutex

直觉上 `Get` 是读操作，但 LRU 的 `Get` 会改变访问顺序：

```text
访问 key -> 移动到链表头
```

所以 `Get` 会写链表。用 `RLock` 会导致并发修改链表，产生数据竞争。除非设计成“读不更新顺序”的近似 LRU，否则标准 LRU 的 Get 应该加写锁。

## 可以怎么优化

### 1. 分片 LRU

高并发下，单把锁可能成为瓶颈。可以按 key hash 拆成多个 shard，每个 shard 一个 LRU。

```text
shard = hash(key) % 64
```

优点：

- 降低锁竞争。

缺点：

- 全局 LRU 变成近似 LRU。
- 容量要分配到每个 shard。

### 2. 读写分离或近似 LRU

如果读极多，可以不在每次 Get 都移动节点，而是异步采样更新热度。这是性能和精确性的权衡。

### 3. 使用成熟库

生产中可以考虑成熟 LRU 库，重点关注：

- 并发安全。
- 淘汰回调。
- TTL。
- 指标。
- 内存限制。

## 面试回答模板

并发安全 LRU 我会用哈希表加双向链表。哈希表负责 O(1) 定位节点，双向链表维护访问顺序，最近访问放头部，最久未访问在尾部。Put 时如果 key 存在就更新并移动到头部；如果不存在就插入头部，超过容量则删除尾部节点，同时从 map 删除。

并发安全方面，我会用 Mutex 保护 map 和链表。因为 LRU 的 Get 不是纯读，它需要更新访问顺序，所以不能简单用 RLock。高并发下如果单锁成为瓶颈，可以做分片 LRU，用多个小 LRU 降低锁竞争，但这是近似全局 LRU。

## 常见追问

### 1. 为什么哈希表加链表能做到 O(1)？

哈希表 O(1) 找到节点，双向链表在已知节点指针时 O(1) 删除和插入，所以 Get、Put、淘汰都可以 O(1)。

### 2. 为什么节点里要存 key？

淘汰尾节点时，需要从 map 删除这个节点对应的 key。如果节点不存 key，就无法 O(1) 删除 map 项。

### 3. 高并发下怎么优化？

可以分片，每个 shard 一把锁和一个 LRU，减少锁竞争。但这会牺牲严格全局 LRU，只能做到近似。

## 题目 2：如何实现订单延迟队列，也就是延时消息队列？

### 30 秒回答

订单延迟队列常用于“下单后 15 分钟未支付自动取消”。实现方式有多种：Redis ZSet、消息队列延迟消息、时间轮、数据库定时扫描。核心设计是把任务按执行时间排序，到期后投递给消费者处理。消费者处理时必须再次查询订单状态，确认仍未支付再取消，不能只依赖延迟消息本身。

可靠实现还要考虑幂等、重复投递、服务重启、消息丢失、延迟精度、积压和补偿。

## 方案一：Redis ZSet

### 原理

使用 ZSet 保存延迟任务：

```text
key: order_delay_queue
member: order_id
score: execute_at_timestamp
```

扫描到期任务：

```text
ZRANGEBYSCORE order_delay_queue 0 now LIMIT 0 100
```

取出后删除并处理。

## Redis ZSet 简化实现

```go
type DelayQueue struct {
    rdb *redis.Client
    key string
}

func (q *DelayQueue) Add(ctx context.Context, orderID string, executeAt time.Time) error {
    return q.rdb.ZAdd(ctx, q.key, redis.Z{
        Score:  float64(executeAt.UnixMilli()),
        Member: orderID,
    }).Err()
}

func (q *DelayQueue) Poll(ctx context.Context, now time.Time, limit int64) ([]string, error) {
    script := `
local items = redis.call("ZRANGEBYSCORE", KEYS[1], "-inf", ARGV[1], "LIMIT", 0, ARGV[2])
if #items == 0 then
    return items
end
redis.call("ZREM", KEYS[1], unpack(items))
return items
`

    result, err := q.rdb.Eval(
        ctx,
        script,
        []string{q.key},
        now.UnixMilli(),
        limit,
    ).StringSlice()
    if err != nil {
        return nil, err
    }
    return result, nil
}
```

这里用 Lua 保证“取到期任务”和“删除任务”是原子的，避免多个消费者重复抢同一批任务。

### 消费逻辑

```go
func HandleExpiredOrder(ctx context.Context, orderID string) error {
    order, err := orderRepo.Get(ctx, orderID)
    if err != nil {
        return err
    }

    if order.Status != "WAIT_PAY" {
        return nil
    }

    return orderService.Cancel(ctx, orderID, "payment timeout")
}
```

关键点：延迟消息只是提醒，不是最终事实。真正取消前必须查订单当前状态。

## 方案二：MQ 延迟消息

如果消息队列支持延迟消息，可以直接发送延迟消息。

流程：

```text
创建订单
  -> 发送延迟 15 分钟的 OrderPaymentTimeout 消息
  -> 到期后消费者收到消息
  -> 查询订单状态
  -> 未支付则取消
```

优点：

- 不用自己轮询。
- 和业务消息体系统一。

缺点：

- 依赖 MQ 能力。
- 延迟精度和最大延迟时间受 MQ 实现限制。
- 排查需要依赖 MQ 工具。

## 方案三：时间轮

时间轮适合大量定时任务。

原理是把时间拆成很多槽：

```text
slot 0, slot 1, slot 2, ... slot N
```

每个 tick 推进一个槽，到槽后执行里面的任务。

优点：

- 性能高。
- 适合大量短延迟任务。

缺点：

- 实现复杂。
- 服务重启后需要持久化恢复。
- 单机时间轮不适合强可靠订单场景。

## 方案四：数据库定时扫描

订单表有字段：

```text
status
expire_at
```

定时任务扫描：

```sql
SELECT order_id
FROM orders
WHERE status = 'WAIT_PAY'
  AND expire_at <= NOW()
LIMIT 100;
```

然后取消订单。

优点：

- 实现简单。
- 数据可靠。

缺点：

- 扫描压力大。
- 延迟精度一般。
- 数据量大时要做好索引和分片。

必须建立索引：

```text
(status, expire_at)
```

## 推荐组合方案

生产中常用组合：

```text
MQ/Redis 延迟队列负责及时触发
数据库 expire_at 扫描负责兜底补偿
```

原因是任何单一机制都可能失败：

- MQ 消息丢失或积压。
- Redis 数据过期或故障。
- 消费者处理失败。

兜底扫描能保证最终一致。

## 可靠性设计

### 1. 幂等取消

取消订单必须幂等：

```sql
UPDATE orders
SET status = 'CANCELED'
WHERE order_id = ?
  AND status = 'WAIT_PAY';
```

只有待支付状态才能取消。

### 2. 重复消息处理

延迟消息可能重复。消费者查询订单状态后，如果已经支付或已经取消，直接返回成功。

### 3. 支付和取消并发

可能出现支付成功和超时取消同时发生。

解决方式：

- 使用订单状态机。
- 数据库条件更新。
- 支付成功只允许 `WAIT_PAY -> PAID`。
- 取消只允许 `WAIT_PAY -> CANCELED`。
- 谁先更新成功，另一个更新影响行数为 0。

### 4. 失败重试和死信

取消订单失败时要重试，多次失败进入死信队列或异常表。

### 5. 监控

关注：

- 延迟队列长度。
- 到期未处理数量。
- 消费延迟。
- 取消失败数。
- 死信数量。
- WAIT_PAY 超时订单数量。

## 面试回答模板

订单延迟队列可以用 Redis ZSet、MQ 延迟消息、时间轮或数据库扫描实现。比如用 Redis ZSet 时，score 存订单过期时间，member 存 order_id，消费者定期取出 score 小于当前时间的订单进行处理。取出和删除要用 Lua 保证原子性，避免多个消费者重复处理。

但延迟消息只是一种触发机制，不能直接认为订单一定要取消。消费者必须重新查询订单状态，如果订单仍是 WAIT_PAY，才用条件更新改成 CANCELED；如果已经支付或取消，就直接忽略。这样可以处理重复消息和支付取消并发。

为了可靠，我会加兜底扫描任务，定期扫描 `status=WAIT_PAY and expire_at <= now()` 的订单，防止 MQ 或 Redis 漏消息。取消操作本身要幂等，失败要重试，多次失败进死信队列或异常表。

## 常见追问

### 1. Redis ZSet 多消费者会不会重复取任务？

如果先查再删，两步不是原子操作，可能重复。要用 Lua 脚本把到期查询和删除合并成原子操作，或者用更完整的 claimed 状态设计。

### 2. 延迟消息到了，但订单刚好支付成功怎么办？

消费者必须查询订单状态，并用条件更新。只有 `WAIT_PAY` 才能取消。如果支付已经把状态改成 `PAID`，取消更新会失败。

### 3. 只用数据库扫描可以吗？

可以，简单可靠，但实时性和性能一般。订单量大时要用索引、分批扫描、分片扫描。更好的方式是延迟队列做实时触发，数据库扫描做兜底。

