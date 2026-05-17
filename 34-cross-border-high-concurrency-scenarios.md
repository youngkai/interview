# 跨境物流高并发场景实战：订单预报、轨迹查询、结算一致性

这篇面向高级 Golang 开发面试中的业务场景题。重点不是单个 API 怎么写，而是如何在跨境物流场景里把 Go 并发、限流、MQ、熔断、缓存、接口抽象、幂等和最终一致性串起来。

## 场景 1：高并发订单/运单预报系统

### 背景

跨境电商大促期间，例如双 11、Black Friday，系统可能瞬间涌入几十万跨境订单。平台需要：

- 接收商家订单。
- 生成内部订单或运单预报。
- 向海关推单。
- 向第三方物流商申请物流单号。
- 通知仓储和履约系统。

面试官可能会问：

- 如何设计一个能承受 10w QPS 的运单接收服务？
- Go 端如何做限流和降级？
- 下游第三方物流商或海关接口响应慢甚至挂掉，如何保证我们自己的系统不被拖垮？

## 30 秒回答

我会把运单接收服务设计成“入口快速接收 + MQ 异步削峰 + 后台 worker 分级处理”的架构。入口层只做鉴权、参数校验、幂等校验、限流和落队列，不同步等待海关或物流商接口。请求通过后写入 Kafka/RocketMQ，然后快速返回“已受理”。

Go 端使用 `golang.org/x/time/rate` 的令牌桶做本机限流，用租户级、接口级、全局限流保护入口。下游慢或挂掉时，用熔断、超时、重试退避、隔离的 worker pool 和失败队列保护主系统。核心原则是不能让慢下游占满 Goroutine、连接池、队列和内存。

## 总体架构

```text
商家/API Gateway
  -> 运单接收服务
     -> 鉴权
     -> 参数校验
     -> 幂等校验
     -> 限流
     -> 写入订单预报表/接收流水
     -> 写入 Kafka/RocketMQ
     -> 返回 accepted

MQ
  -> 物流商 worker 集群
  -> 海关推单 worker 集群
  -> 仓储通知 worker 集群
  -> 通知/回调 worker 集群
```

## 入口服务设计

入口服务不要做这些事：

- 同步调用第三方物流商。
- 同步等待海关推单结果。
- 在 HTTP 请求里做长时间重试。
- 为每个订单无限制启动 Goroutine。
- 把请求无限堆在内存队列里。

入口服务只做必要动作：

1. 鉴权和租户识别。
2. 参数基础校验。
3. 幂等校验。
4. 限流和降级判断。
5. 写本地接收记录。
6. 写 MQ。
7. 返回受理结果。

## Go 端令牌桶限流

`golang.org/x/time/rate` 是 Go 常用令牌桶限流包。令牌桶适合限制平均速率，同时允许一定突发流量。

### 全局限流示例

```go
package main

import (
    "net/http"
    "time"

    "golang.org/x/time/rate"
)

type Server struct {
    limiter *rate.Limiter
}

func NewServer() *Server {
    return &Server{
        // 每秒 10000 个 token，允许 20000 的突发。
        limiter: rate.NewLimiter(rate.Limit(10000), 20000),
    }
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    if !s.limiter.Allow() {
        http.Error(w, "rate limited", http.StatusTooManyRequests)
        return
    }

    s.handleWaybill(w, r)
}
```

这里用 `Allow()`，超过限流直接拒绝，适合大促入口保护。不要用无限等待，否则请求会在服务内堆积，占用连接和 Goroutine。

### 租户级限流

大促时不能让一个大商家打爆所有资源，所以要按 tenant 隔离限流。

```go
type TenantLimiter struct {
    mu       sync.Mutex
    limiters map[int64]*rate.Limiter
}

func NewTenantLimiter() *TenantLimiter {
    return &TenantLimiter{
        limiters: make(map[int64]*rate.Limiter),
    }
}

func (l *TenantLimiter) Allow(tenantID int64) bool {
    limiter := l.getLimiter(tenantID)
    return limiter.Allow()
}

func (l *TenantLimiter) getLimiter(tenantID int64) *rate.Limiter {
    l.mu.Lock()
    defer l.mu.Unlock()

    limiter, ok := l.limiters[tenantID]
    if ok {
        return limiter
    }

    limiter = rate.NewLimiter(rate.Limit(1000), 2000)
    l.limiters[tenantID] = limiter
    return limiter
}
```

生产中要注意：

- `limiters` map 需要清理，避免租户无限增长造成内存泄漏。
- 多实例部署时，本地限流只能限制单实例，需要网关或 Redis 做分布式限流。
- 大客户可以配置独立限流规则。

## 异步化：接收后写 MQ

入口请求成功后，不同步调用慢下游，而是写 MQ。

```go
type WaybillRequest struct {
    TenantID      int64
    ExternalNo    string
    OrderID       string
    Receiver      string
    Country       string
    IdempotencyKey string
}

func (s *Server) handleWaybill(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()

    req, err := decodeWaybillRequest(r)
    if err != nil {
        http.Error(w, "bad request", http.StatusBadRequest)
        return
    }

    accepted, err := s.acceptService.Accept(ctx, req)
    if err != nil {
        http.Error(w, "internal error", http.StatusInternalServerError)
        return
    }

    if !accepted {
        http.Error(w, "duplicate request", http.StatusConflict)
        return
    }

    w.WriteHeader(http.StatusAccepted)
}
```

`Accept` 内部做：

```text
begin transaction
  insert waybill_accept_log
  insert outbox_message
commit
```

然后由后台 outbox sender 投递 Kafka/RocketMQ。这样可以避免“数据库写成功但 MQ 发送失败”。

## 下游慢接口如何保护自己

下游包括：

- 第三方物流商。
- 海关接口。
- 海外仓。
- 支付或计费系统。

这些系统慢或挂掉时，如果上游继续无限调用，会导致：

- Goroutine 堆积。
- HTTP 连接池耗尽。
- MQ 消费积压。
- 重试风暴。
- 数据库连接被占满。
- 故障级联。

## 保护手段

### 1. 超时

每个下游调用必须有超时。

```go
ctx, cancel := context.WithTimeout(parent, 2*time.Second)
defer cancel()

resp, err := client.PushToCustoms(ctx, req)
```

不要使用没有 timeout 的 HTTP client。

### 2. 并发隔离

不同下游用不同 worker pool。

```text
customs-worker: 100 concurrency
dhl-worker: 200 concurrency
fedex-worker: 150 concurrency
warehouse-worker: 300 concurrency
```

一个下游慢，不应该拖垮所有 worker。

### 3. 熔断

当下游错误率或慢调用比例超过阈值，短时间内不再调用，直接失败或进入延迟重试。

Go 里可以选择：

- Sentinel Go：偏流控、熔断、系统自适应保护。
- hystrix-go：老项目常见的 Hystrix 风格熔断隔离。
- sony/gobreaker：轻量 circuit breaker。
- 服务网格或网关层熔断。

面试中可以这样表达：

> 我更关注熔断模式本身：统计错误率和慢调用，超过阈值打开熔断器，直接快速失败；一段时间后半开试探，如果恢复再关闭。具体库可以用 sentinel-golang，也可能在老项目里看到 hystrix-go。

### 4. 重试退避

重试只针对临时错误：

- 网络超时。
- 5xx。
- 临时限流。

不重试：

- 参数错误。
- 鉴权失败。
- 业务状态不允许。

重试要有：

- 最大次数。
- 指数退避。
- 随机抖动。
- 总超时。
- 幂等键。

### 5. 降级

海关或物流商挂了，不代表接收服务也要挂。

可以降级为：

- 先返回“已受理，处理中”。
- 写入延迟队列稍后重试。
- 切换备用物流商。
- 暂停非核心推送。
- 只保留订单接收能力。

## 面试回答模板

如果要求 10w QPS 的运单接收，我会让入口服务保持轻量，做鉴权、参数校验、幂等、限流和落 MQ，然后快速返回受理结果。不会在入口同步调用海关或物流商，因为这些下游延迟不可控。

Go 端用 `golang.org/x/time/rate` 做令牌桶限流，按全局、接口、租户维度控制流量，超过阈值直接 429 或降级。进入系统的请求通过本地事务写接收流水和 outbox，再异步投递 Kafka/RocketMQ，由后端 worker 集群消费。

下游慢或挂掉时，用超时、熔断、并发隔离、重试退避和死信队列保护自己。每个物流商或海关接口有独立 worker pool 和连接池，一个下游故障不能拖垮整个系统。最终通过 MQ 重试、对账和补偿保证任务最终完成。

## 场景 2：物流轨迹 Track & Trace 高频更新与查询

### 背景

一个包裹从中国到美国，可能产生几十条轨迹状态：

- 已收寄。
- 已入仓。
- 已出库。
- 已离港。
- 清关中。
- 清关完成。
- 派送中。
- 已签收。

用户和商家会高频查询轨迹，系统也会从 UPS、DHL、FedEx 等海外服务商拉取或接收轨迹。

面试官可能会问：

- 轨迹数据读多写多，如何设计存储和缓存策略？
- 如何解决缓存击穿和缓存雪崩？
- 海外多个服务商数据格式不同，在 Go 里如何优雅兼容和解析异构数据？

## 30 秒回答

轨迹系统我会拆成写入链路和查询链路。写入链路接收或拉取各物流商轨迹，统一标准化后写入轨迹事件表，并更新最新状态缓存。查询链路优先读 Redis 最新状态和轨迹摘要，缓存未命中时使用 `singleflight` 合并同一 tracking_no 的并发回源，避免缓存击穿。

对于 UPS、DHL、FedEx 这类异构数据，我会用 Go interface 定义统一 Driver，例如 `Fetch`、`Parse`、`Normalize`，每个物流商实现自己的 Driver，通过工厂注册和策略模式选择具体解析器。这样新增物流商时只增加一个 Driver，不污染主流程。

## 存储设计

### 轨迹事件明细

轨迹明细适合持久化到 MySQL/PostgreSQL、MongoDB 或专门日志/事件库，具体看查询和写入规模。

关系型表例子：

```sql
CREATE TABLE tracking_event (
    id BIGINT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    tracking_no VARCHAR(128) NOT NULL,
    carrier VARCHAR(64) NOT NULL,
    status VARCHAR(64) NOT NULL,
    event_time TIMESTAMP NOT NULL,
    received_at TIMESTAMP NOT NULL,
    location VARCHAR(255),
    raw_payload JSON,
    event_id VARCHAR(128) NOT NULL,
    UNIQUE(event_id)
);
```

常用索引：

```text
(tracking_no, event_time)
(tenant_id, tracking_no)
(carrier, received_at)
```

### 最新状态表

```sql
CREATE TABLE tracking_latest (
    tracking_no VARCHAR(128) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    carrier VARCHAR(64) NOT NULL,
    status VARCHAR(64) NOT NULL,
    status_rank INT NOT NULL,
    event_time TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
```

查询最新状态时不必扫描明细表。

## 缓存策略

### Redis Key 设计

最新状态：

```text
tracking:latest:{tracking_no}
```

轨迹摘要：

```text
tracking:events:{tracking_no}
```

租户维度：

```text
tenant:{tenant_id}:tracking:{tracking_no}
```

### 写入时更新缓存

轨迹事件写入后，可以：

1. 写明细表。
2. 更新 latest 表。
3. 删除或更新 Redis latest key。
4. 发布 TrackingUpdated 事件。

对实时性要求高，可以主动更新缓存；对一致性要求更高，可以删除缓存，让下一次读取回源。

### TTL 随机抖动防雪崩

不要让大量 key 同时过期。

```go
ttl := 10*time.Minute + time.Duration(rand.Intn(300))*time.Second
```

### 空值缓存防穿透

不存在的 tracking_no 可以缓存短 TTL 空值。

```text
tracking:latest:UNKNOWN -> empty, TTL 30s
```

### singleflight 防击穿

同一包裹缓存过期时，大量并发查询可能同时打数据库。用 `singleflight` 合并同一个 key 的回源请求。

```go
type TrackingQueryService struct {
    cache RedisClient
    repo  TrackingRepository
    group singleflight.Group
}

func (s *TrackingQueryService) GetLatest(ctx context.Context, trackingNo string) (*TrackingLatest, error) {
    cacheKey := "tracking:latest:" + trackingNo

    if v, ok := s.cache.Get(ctx, cacheKey); ok {
        return decodeLatest(v)
    }

    value, err, _ := s.group.Do(cacheKey, func() (any, error) {
        if v, ok := s.cache.Get(ctx, cacheKey); ok {
            return decodeLatest(v)
        }

        latest, err := s.repo.GetLatest(ctx, trackingNo)
        if err != nil {
            return nil, err
        }

        _ = s.cache.Set(ctx, cacheKey, encodeLatest(latest), jitterTTL())
        return latest, nil
    })
    if err != nil {
        return nil, err
    }
    return value.(*TrackingLatest), nil
}
```

注意这里在 `singleflight` 内部再次读缓存，是为了避免多个请求排队期间，前一个请求已经写入缓存。

## 异构物流商解析：interface + Driver

### 统一接口

```go
type TrackingDriver interface {
    Carrier() string
    Fetch(ctx context.Context, trackingNo string) ([]byte, error)
    Parse(ctx context.Context, payload []byte) ([]CarrierEvent, error)
    Normalize(ctx context.Context, events []CarrierEvent) ([]TrackingEvent, error)
}
```

### DHL Driver

```go
type DHLDriver struct {
    client *http.Client
}

func (d *DHLDriver) Carrier() string {
    return "DHL"
}

func (d *DHLDriver) Fetch(ctx context.Context, trackingNo string) ([]byte, error) {
    // 调用 DHL API。
    return nil, nil
}

func (d *DHLDriver) Parse(ctx context.Context, payload []byte) ([]CarrierEvent, error) {
    // 解析 DHL 原始格式。
    return nil, nil
}

func (d *DHLDriver) Normalize(ctx context.Context, events []CarrierEvent) ([]TrackingEvent, error) {
    // 映射为内部统一状态。
    return nil, nil
}
```

### Driver 工厂

```go
type DriverFactory struct {
    drivers map[string]TrackingDriver
}

func NewDriverFactory(drivers ...TrackingDriver) *DriverFactory {
    f := &DriverFactory{
        drivers: make(map[string]TrackingDriver),
    }
    for _, driver := range drivers {
        f.drivers[driver.Carrier()] = driver
    }
    return f
}

func (f *DriverFactory) Get(carrier string) (TrackingDriver, bool) {
    driver, ok := f.drivers[carrier]
    return driver, ok
}
```

### 调用流程

```go
func (s *TrackingSyncService) Sync(ctx context.Context, carrier, trackingNo string) error {
    driver, ok := s.factory.Get(carrier)
    if !ok {
        return ErrUnsupportedCarrier
    }

    payload, err := driver.Fetch(ctx, trackingNo)
    if err != nil {
        return err
    }

    carrierEvents, err := driver.Parse(ctx, payload)
    if err != nil {
        return err
    }

    events, err := driver.Normalize(ctx, carrierEvents)
    if err != nil {
        return err
    }

    return s.store.SaveEvents(ctx, events)
}
```

好处：

- 主流程不关心具体物流商格式。
- 新增物流商只新增 Driver。
- 每个 Driver 可以独立测试。
- 状态映射规则集中在各自 Driver 或配置中。

## 高频查询保护

### 1. Redis Cluster

轨迹查询量大时，Redis 单点可能成为瓶颈。可以使用 Redis Cluster 分片。

注意：

- key 设计要均匀。
- 避免大 key。
- 热点 tracking_no 可能仍然集中。

### 2. 本地缓存

对非常热点的最新状态，可以加短 TTL 本地缓存，减少 Redis 压力。

### 3. 批量查询

商家后台可能一次查多个包裹，不要循环单查。

```text
MGET tracking:latest:a tracking:latest:b tracking:latest:c
```

数据库也用批量查询。

### 4. 降级

数据库慢时：

- 返回 Redis 中稍旧数据。
- 只返回最新状态，不返回完整轨迹。
- 提示轨迹更新中。

## 面试回答模板

轨迹系统读多写多，我会把明细事件和最新状态分开存。明细表保存完整轨迹，按 `tracking_no + event_time` 建索引；最新状态表保存每个包裹当前状态，查询不用扫明细。Redis 缓存最新状态和轨迹摘要，TTL 加随机抖动防雪崩，空值缓存防穿透，热点 key 用本地缓存或请求合并。

缓存击穿我会用 Go 的 `singleflight`，同一个 tracking_no 缓存 miss 时，只允许一个请求回源数据库，其他请求等待同一个结果。这样能避免一个热点包裹缓存过期时把数据库打爆。

对于 UPS、DHL、FedEx 这些异构格式，我会用 interface 定义统一 Driver，每个物流商实现自己的 Fetch、Parse、Normalize。主流程只依赖 TrackingDriver 接口，通过工厂按 carrier 获取具体 Driver。新增物流商时扩展一个实现即可。

## 场景 3：跨境结算与数据强一致性

### 背景

包裹计费可能涉及：

- 运费。
- 清关费。
- 海外仓储费。
- 偏远地区附加费。
- 退件费。

当运单状态改为“已出库”时，系统可能需要：

1. 扣减商户余额。
2. 更新运单状态。
3. 通知海外仓。
4. 生成财务流水。

面试官可能会问：

- 在微服务架构下，如何保证扣费和状态更新最终一致？
- 如果 MQ 消息重复发送，Go 消费者端如何做到幂等？

## 30 秒回答

跨境结算不适合把多个服务强行放进一个分布式大事务。更常见的做法是本地事务 + outbox 本地消息表 + MQ + 消费者幂等 + 状态机 + 对账补偿。运单服务在本地事务里更新运单状态并写 outbox 消息，账务服务消费消息后幂等扣费并生成账务流水，海外仓服务消费消息后执行通知。

如果要求更强的资源预留，可以用 TCC 或 Saga。Go 消费者幂等可以用 MySQL 唯一索引、业务流水号、`order_id + status`、`event_id + consumer_group`，也可以用 Redis SetNX 做前置去重，但最终应以数据库唯一约束兜底。

## 方案一：本地消息表 + MQ

### 运单服务本地事务

```text
begin
  update shipment set status = 'OUTBOUND'
  insert shipment_status_flow
  insert outbox_message(event_type='ShipmentOutbound')
commit
```

关键点：

- 状态更新和消息记录在同一个本地事务。
- 避免状态改了但消息没发。
- 后台 outbox sender 负责投递 MQ。

### MQ 消费者

```text
Billing Service 消费 ShipmentOutbound
  -> 幂等校验
  -> 扣减余额
  -> 生成账务流水
  -> ack

Warehouse Service 消费 ShipmentOutbound
  -> 幂等校验
  -> 通知海外仓
  -> ack
```

## 数据表设计

### 运单状态流水

```sql
CREATE TABLE shipment_status_flow (
    id BIGINT PRIMARY KEY,
    shipment_id BIGINT NOT NULL,
    from_status VARCHAR(32) NOT NULL,
    to_status VARCHAR(32) NOT NULL,
    event_id VARCHAR(128) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    UNIQUE(event_id)
);
```

### Outbox 表

```sql
CREATE TABLE outbox_message (
    id BIGINT PRIMARY KEY,
    event_id VARCHAR(128) NOT NULL,
    event_type VARCHAR(64) NOT NULL,
    aggregate_id VARCHAR(128) NOT NULL,
    payload JSON NOT NULL,
    status VARCHAR(32) NOT NULL,
    retry_count INT NOT NULL DEFAULT 0,
    next_retry_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    UNIQUE(event_id)
);
```

### 账务流水

```sql
CREATE TABLE merchant_balance_flow (
    id BIGINT PRIMARY KEY,
    merchant_id BIGINT NOT NULL,
    shipment_id BIGINT NOT NULL,
    event_id VARCHAR(128) NOT NULL,
    amount DECIMAL(18, 2) NOT NULL,
    currency VARCHAR(8) NOT NULL,
    flow_type VARCHAR(32) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    UNIQUE(event_id)
);
```

`event_id` 是账务幂等关键。重复消息不能重复扣费。

## 扣费的原子性

扣减余额使用条件更新：

```sql
UPDATE merchant_account
SET balance = balance - ?
WHERE merchant_id = ?
  AND balance >= ?;
```

影响行数：

- 1：扣费成功。
- 0：余额不足。

扣费和流水要在账务服务本地事务中完成：

```text
begin
  insert merchant_balance_flow(event_id unique)
  update merchant_account set balance = balance - amount where balance >= amount
commit
```

如果 `event_id` 唯一冲突，说明已经处理过，直接返回成功。

## 消费者幂等设计

### 方式一：MySQL 唯一索引

最可靠。

```sql
UNIQUE(event_id)
```

或：

```sql
UNIQUE(shipment_id, fee_type)
```

适合财务流水、状态流水、消息消费记录。

### 方式二：消费记录表

```sql
CREATE TABLE processed_message (
    event_id VARCHAR(128) NOT NULL,
    consumer_group VARCHAR(64) NOT NULL,
    processed_at TIMESTAMP NOT NULL,
    PRIMARY KEY(event_id, consumer_group)
);
```

消费者处理前插入：

```text
insert processed_message
```

唯一冲突说明已处理。

### 方式三：Redis SetNX

可以做前置快速去重：

```text
SETNX idem:billing:{event_id} 1
EXPIRE idem:billing:{event_id} 7d
```

但财务类场景不能只靠 Redis，因为 Redis 数据可能过期或丢失，最终要有数据库唯一约束。

## 状态机保证状态一致

运单状态不能随便覆盖。

```sql
UPDATE shipment
SET status = 'OUTBOUND'
WHERE shipment_id = ?
  AND status = 'PACKED';
```

只有从 `PACKED` 才能变成 `OUTBOUND`。

重复消息或乱序消息：

- 如果已经是 `OUTBOUND`，可以视为幂等成功。
- 如果已经是更后面的状态，例如 `DELIVERED`，不能回退。
- 如果状态不允许，进入异常队列或人工处理。

## Saga 补偿思路

如果流程是：

```text
运单出库 -> 扣费 -> 通知海外仓
```

扣费成功但通知海外仓失败：

- 通知海外仓可以重试。
- 不一定需要回滚扣费。
- 多次失败进入人工处理。

如果扣费失败：

- 运单可以进入 `OUTBOUND_PENDING_BILLING`。
- 或回退到待出库。
- 或进入财务异常状态。

这取决于业务规则。

## Seata-Go 怎么回答

如果面试官提到 Seata-Go，可以这样说：

> Seata 这类分布式事务框架可以提供 AT/TCC/Saga 等模式，但在高并发跨境物流业务里，我更常用本地消息表 + MQ + 幂等 + 补偿，因为它对业务可控、可观测性好，也更适合长流程和外部系统协作。库存预占或余额冻结这种资源类操作，可以借鉴 TCC 思路，Try 冻结资源，Confirm 扣减，Cancel 释放。

## 面试回答模板

运单出库同时扣减商户余额和通知海外仓，我会用最终一致方案。运单服务先在本地事务里做状态机条件更新，把状态从 PACKED 改成 OUTBOUND，同时写状态流水和 outbox 消息。outbox sender 再把 ShipmentOutbound 投递到 MQ。

账务服务消费消息后，用 event_id 或 shipment_id + fee_type 做幂等，先插入账务流水唯一记录，再用条件更新扣减余额，保证不会重复扣费。海外仓通知服务独立消费同一事件，失败可以重试或进入死信队列，不影响账务服务。

如果 MQ 重复投递，消费者通过 processed_message 表、业务唯一索引或账务流水唯一索引去重。Redis SetNX 可以做前置去重，但财务场景最终必须有数据库唯一约束兜底。最后通过定时对账检查运单状态、账务流水和仓库通知是否一致，异常进入补偿或人工处理。

## 三个场景的统一高级总结

这三个场景本质上都在考同一件事：高并发跨境物流系统不能靠同步链路硬扛。

我的总体设计原则是：

- 入口用限流、鉴权、幂等和快速失败保护系统。
- 核心请求先落本地事务和 MQ，慢操作异步处理。
- 下游调用必须有超时、熔断、隔离和重试退避。
- 查询链路用缓存、singleflight、本地缓存和读模型扛高频访问。
- 异构系统接入用 interface + Driver 策略模式隔离差异。
- 财务、库存、状态流转用数据库唯一约束、条件更新、状态机和 outbox 保证最终一致。
- 所有消费者都要幂等，所有异常都要能重试、进死信或对账补偿。

面试时不要只说“上 MQ、加缓存、用 Redis”。高级回答要讲清楚：

- 超过系统能力时如何拒绝。
- 下游慢时如何隔离。
- 消息重复时如何幂等。
- 缓存失效时如何防止打爆数据库。
- 状态乱序时如何防止回退。
- 数据不一致时如何发现和补偿。
