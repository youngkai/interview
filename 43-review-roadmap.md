# Go 高级后端面试复习路线

这份路线基于当前 42 篇笔记设计，目标是让你从“资料很多”变成“知道先背什么、后补什么、面试前看什么”。

## 总体策略

不要从第 1 篇一路平均看完。面试复习要分层：

1. 先抓 Go 核心高频。
2. 再抓系统设计和跨境物流业务场景。
3. 然后补数据库、缓存、MQ。
4. 最后用速背题做临场强化。

## 第一轮：Go 核心能力

目标：先保证 Go 面试基础不丢分。

建议阅读：

- [01 Goroutine 与线程的区别](./01-goroutine-vs-thread.md)
- [02 GMP 调度模型](./02-gmp-scheduler.md)
- [03 Channel、select 的使用场景](./03-channel-select.md)
- [04 context 包的使用](./04-context.md)
- [05 defer、panic、recover](./05-defer-panic-recover.md)
- [06 slice、map 的底层实现](./06-slice-map-internals.md)
- [07 接口和类型断言](./07-interface-type-assertion.md)
- [08 GC 对高并发系统的影响](./08-gc-impact-high-concurrency.md)
- [09 内存泄漏和内存优化](./09-memory-leak-optimization.md)
- [10 高并发下的锁优化](./10-lock-optimization.md)
- [36 Go 核心高频强化版](./36-go-core-high-frequency-review.md)

重点背：

- Goroutine 和线程区别。
- GMP：G/M/P、work stealing、syscall hand off、sysmon。
- channel：无缓冲/有缓冲、select、close panic 规则、hchan。
- context：超时、取消、树形传播、Done 原理。
- GC：三色标记、写屏障、逃逸分析、pprof。
- slice：ptr/len/cap、append 扩容、共享底层数组。
- map：并发不安全、扩容、无序遍历。
- 锁：Mutex/RWMutex/atomic 选择。

## 第二轮：Go 并发实战

目标：能写出代码，能解释为什么这么写。

建议阅读：

- [11 Go 并发实战例题](./11-practical-concurrency-questions.md)
- [23 并发安全的数据结构](./23-concurrent-safe-data-structures.md)
- [26 数据结构和分布式算法实战题](./26-data-structure-practical-questions.md)
- [33 高级 Go Runtime、gRPC 与 Redis 分布式锁](./33-advanced-go-runtime-grpc-redis-lock.md)
- [39 Go HTTP Client/Server 与网络超时调优](./39-go-http-network-timeout.md)
- [42 Go 服务优雅停机与 Kubernetes 部署治理](./42-graceful-shutdown-kubernetes.md)

重点背：

- 生产者消费者模型。
- worker pool。
- 大并发请求如何保证内存不爆。
- 并发安全 LRU。
- 延迟队列。
- HTTP client 连接池。
- HTTP server 超时。
- 优雅停机和 MQ consumer 退出。

## 第三轮：微服务和分布式系统

目标：能回答高级后端系统设计题。

建议阅读：

- [12 微服务架构](./12-microservice-architecture.md)
- [13 如何设计微服务接口](./13-microservice-api-design.md)
- [14 RPC vs HTTP REST](./14-rpc-vs-http-rest.md)
- [15 数据一致性问题](./15-distributed-consistency.md)
- [16 高并发和高可用](./16-high-concurrency-high-availability.md)
- [17 负载均衡](./17-load-balancing.md)
- [19 消息队列](./19-message-queue.md)
- [25 分布式系统常用算法](./25-distributed-common-algorithms.md)
- [32 高级 Golang 系统设计必考题](./32-advanced-golang-system-design.md)
- [34 跨境物流高并发场景实战](./34-cross-border-high-concurrency-scenarios.md)
- [35 系统监控与可观测性](./35-observability-opentelemetry-jaeger-prometheus.md)
- [37 高级后端综合面试强化](./37-senior-backend-synthesis-review.md)

重点背：

- 服务拆分原则。
- gRPC vs REST。
- 接口幂等设计。
- 本地消息表。
- TCC、Saga、最终一致。
- 限流、熔断、降级、重试。
- OpenTelemetry、Jaeger、Prometheus。
- 跨境物流系统设计三大题。

## 第四轮：数据库、缓存、MQ

目标：能抗住数据库和中间件深挖。

建议阅读：

- [18 缓存策略](./18-cache-strategy.md)
- [20 数据库分库分表策略](./20-database-sharding.md)
- [27 MySQL/PostgreSQL 调优](./27-mysql-postgresql-tuning.md)
- [28 Redis 缓存策略和分布式锁](./28-redis-cache-distributed-lock.md)
- [29 MongoDB 等 NoSQL 使用场景](./29-mongodb-nosql-scenarios.md)
- [30 数据一致性和幂等性处理](./30-data-consistency-idempotency.md)
- [31 数据库和缓存实战题](./31-database-practical-questions.md)
- [38 慢查询索引优化与消息队列可靠性强化](./38-db-index-mq-reliability-review.md)
- [40 MySQL MVCC、锁与死锁排查](./40-mysql-mvcc-locks-deadlock.md)
- [41 Redis 与 Kafka 深入机制](./41-redis-kafka-advanced-mechanisms.md)

重点背：

- EXPLAIN。
- 深度分页优化。
- 联合索引最左前缀。
- B+ 树 IO 估算。
- MVCC、Read View、快照读、当前读。
- 行锁、间隙锁、next-key lock。
- Redis hot key、big key、Cluster、RDB/AOF。
- Kafka partition、consumer group、offset、rebalance、ISR。
- RabbitMQ vs Kafka 选型。

## 第五轮：跨境物流业务专项

目标：让回答更贴近岗位，而不是只讲通用八股。

建议阅读：

- [21 跨境物流系统设计实战题](./21-logistics-system-design-practical.md)
- [31 数据库和缓存实战题](./31-database-practical-questions.md)
- [32 高级 Golang 系统设计必考题](./32-advanced-golang-system-design.md)
- [34 跨境物流高并发场景实战](./34-cross-border-high-concurrency-scenarios.md)
- [35 系统监控与可观测性](./35-observability-opentelemetry-jaeger-prometheus.md)
- [37 高级后端综合面试强化](./37-senior-backend-synthesis-review.md)

重点背：

- 秒杀库存防超卖。
- 跨境订单状态最终一致。
- 物流跟踪系统。
- 跨境支付/清关异步处理。
- 高并发运单预报。
- 轨迹高频更新与查询。
- 跨境结算一致性。
- OpenTelemetry 追踪订单链路。

## 7 天复习计划

### 第 1 天：Go 核心

看：

- 01、02、03、04、06、08、36

必须能讲：

- GMP。
- channel。
- context。
- GC。
- slice。

### 第 2 天：并发和性能

看：

- 09、10、11、23、26、33、39

必须能讲：

- worker pool。
- Goroutine 泄漏。
- 锁优化。
- 并发安全 LRU。
- HTTP 连接池和超时。

### 第 3 天：微服务和分布式

看：

- 12、13、14、15、16、19、25

必须能讲：

- 服务拆分。
- gRPC vs REST。
- MQ 使用场景。
- 本地消息表。
- TCC/Saga。
- 限流/熔断/重试。

### 第 4 天：数据库

看：

- 20、27、31、38、40

必须能讲：

- 分库分表。
- EXPLAIN。
- 深度分页。
- 索引设计。
- MVCC 和锁。
- 死锁排查。

### 第 5 天：Redis / Kafka / MQ

看：

- 18、28、30、38、41

必须能讲：

- 缓存穿透/击穿/雪崩。
- 分布式锁。
- Redis hot key/big key。
- Kafka partition/offset/rebalance。
- 消息重复和丢失。

### 第 6 天：系统设计

看：

- 21、32、34、35、37、42

必须能讲：

- 物流跟踪系统。
- 运单预报 10w QPS。
- 跨境清关/支付异步处理。
- 可观测性。
- 优雅停机。

### 第 7 天：速背和模拟面试

看：

- [44 高频 30 问速背版](./44-top-30-interview-qa.md)
- 36、37、38

做法：

- 每题用 30 秒回答一遍。
- 每个系统设计题用 3 分钟讲完整架构。
- 每个回答都尽量带一个跨境物流业务例子。

## 面试前 2 小时只看什么

只看：

- [36 Go 核心高频强化版](./36-go-core-high-frequency-review.md)
- [37 高级后端综合面试强化](./37-senior-backend-synthesis-review.md)
- [38 慢查询索引优化与消息队列可靠性强化](./38-db-index-mq-reliability-review.md)
- [44 高频 30 问速背版](./44-top-30-interview-qa.md)

不要再看长文细节。面试前要保持回答流畅。

## 回答结构模板

任何问题都尽量按这个结构回答：

```text
1. 先给结论。
2. 解释核心原理。
3. 说工程风险。
4. 给解决方案。
5. 落到项目场景。
```

例子：

```text
Goroutine 很轻量，但不是无限免费。
它由 Go runtime 调度，初始栈小，但数量过多会带来栈内存、调度和 GC 压力。
在物流数据批量解析里，我不会每条记录开一个 Goroutine，而是用 worker pool 和有界队列控制并发。
同时用 context 做取消，用 pprof 监控 Goroutine 数和堆内存。
```

