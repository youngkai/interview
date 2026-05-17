# 高级 Golang 高频 30 问速背版

这篇只保留面试最容易问、最适合临场背的 30 个问题。每题按“30 秒回答 + 追问点”整理。

## 1. Goroutine 和线程有什么区别？

### 30 秒回答

Goroutine 是 Go runtime 管理的轻量级并发执行单元，线程是操作系统管理的调度单元。Goroutine 初始栈小、可增长，创建和切换成本比线程低。Go runtime 通过 GMP 模型把大量 Goroutine 调度到少量 OS 线程上执行。

但 Goroutine 不是零成本。数量过多会带来栈内存、调度开销、GC 压力和泄漏风险，所以生产中要用 worker pool、context、限流和 pprof 控制。

### 追问点

- GMP。
- Goroutine 泄漏。
- CPU 密集任务不能无限加 Goroutine。

## 2. GMP 模型如何工作？

### 30 秒回答

G 是 Goroutine，M 是 OS 线程，P 是执行 Go 代码需要的调度上下文。M 必须绑定 P 才能运行 G。P 的数量由 `GOMAXPROCS` 决定，代表 Go 代码最大并行度。

G 创建后通常进入当前 P 的本地队列。M 从本地队列取 G 执行，本地没有就查全局队列，或者从其他 P 偷任务，也就是 work stealing。G 进入阻塞 syscall 时，runtime 会把 P 从 M 上解绑并移交给其他 M。

### 追问点

- sysmon。
- syscall hand off。
- work stealing。
- `_Grunnable`、`_Grunning`、`_Gwaiting`。

## 3. 如何定位 Goroutine 泄漏？

### 30 秒回答

先看 Goroutine 数量是否持续上涨，再用 pprof goroutine profile 查看大量 Goroutine 卡在哪里。常见泄漏点是 channel send/receive 阻塞、select 没有退出条件、ticker 没 stop、context 没监听、下游 I/O 卡住。

命令可以用 `go tool pprof http://host/debug/pprof/goroutine`，或者访问 `/debug/pprof/goroutine?debug=2` 看堆栈。

### 追问点

- context 取消。
- channel 阻塞。
- worker pool 退出。

## 4. 无缓冲 channel 和有缓冲 channel 区别？

### 30 秒回答

无缓冲 channel 发送和接收必须同时就绪，适合同步交接。有缓冲 channel 在缓冲区未满时发送不阻塞，在缓冲区非空时接收不阻塞，适合削峰和解耦生产消费速度差。

但有缓冲 channel 不是无限队列，满了仍会阻塞。生产中要设置合理容量，并考虑队列满时阻塞、超时、丢弃还是降级。

### 追问点

- hchan。
- sendq/recvq。
- close panic 规则。

## 5. select 如何工作？

### 30 秒回答

`select` 可以同时等待多个 channel 操作。如果多个 case 同时 ready，会伪随机选择一个；如果没有 ready 且没有 default，会阻塞；如果有 default，会立即执行 default。nil channel 永远不会 ready。

常用场景是超时控制、取消信号、多路复用和非阻塞收发。

### 追问点

- `ctx.Done()`。
- `time.After` 在循环里的开销。
- default 防阻塞。

## 6. 关闭 channel 有哪些 panic 场景？

### 30 秒回答

向已关闭 channel 发送会 panic，重复 close 会 panic，关闭 nil channel 会 panic。从已关闭 channel 接收不会 panic，会返回零值和 `ok=false`。向 nil channel 发送或接收会永久阻塞。

一般原则是由发送方关闭 channel；多发送方时由协调者在所有发送方结束后统一 close。

### 追问点

- `for range ch`。
- 多 producer 关闭。
- done channel 广播。

## 7. Go GC 是怎么工作的？

### 30 秒回答

Go GC 是并发标记清扫，核心是三色标记。白色表示未标记，灰色表示已发现但引用没扫描完，黑色表示扫描完成。标记从 GC roots 出发，最终剩下的白色对象可回收。

因为标记阶段和用户 Goroutine 并发执行，指针关系会变化，所以 Go 使用写屏障保证不会漏标对象。现代 Go GC 目标是降低 STW，但 GC 仍会影响 CPU、延迟和吞吐。

### 追问点

- 混合写屏障。
- STW。
- GOGC / GOMEMLIMIT。

## 8. 什么是逃逸分析？

### 30 秒回答

逃逸分析是编译器判断变量应该分配在栈上还是堆上的过程。如果变量生命周期超出函数栈帧，或者编译器无法证明它只在当前函数内使用，就可能逃逸到堆。

常见原因包括返回局部变量指针、闭包捕获、interface 装箱、反射、对象过大。可以用 `go build -gcflags="-m" ./...` 查看逃逸原因。

### 追问点

- 堆分配增加 GC 压力。
- 不要为了零逃逸牺牲可读性。

## 9. GC 频繁怎么优化？

### 30 秒回答

先用 pprof heap/allocs、CPU profile、trace、benchmem 定位分配热点。优化方向包括减少临时对象、slice 预分配、复用 buffer、`sync.Pool`、减少指针数量、控制缓存和队列上限、避免 Goroutine 泄漏。

参数上可以根据压测调整 `GOGC`，容器环境可以设置 `GOMEMLIMIT`，但调参不是第一步，降低分配速率和存活对象数量才是根本。

### 追问点

- `sync.Pool` 不是可靠缓存。
- inuse vs alloc。

## 10. slice 底层结构是什么？

### 30 秒回答

slice 是对底层数组的描述符，包含指针、len 和 cap 三个字段。函数传参时拷贝的是 slice header，但底层数组共享，所以修改已有元素会影响外部。

append 时如果容量够，会复用底层数组；容量不够会分配新数组并复制数据。扩容后函数内 slice 指向新数组，外部 slice 不会自动变化。

### 追问点

- 子切片引用大数组。
- full slice expression。
- 扩容倍数不能依赖。

## 11. map 为什么并发不安全？

### 30 秒回答

Go 普通 map 底层是哈希表，写入时可能扩容、迁移 bucket、修改内部结构。多个 Goroutine 并发读写会破坏内部状态，甚至触发 `fatal error: concurrent map read and map write`。

并发访问 map 要用 Mutex/RWMutex、sync.Map、分片锁或 channel owner 模型。

### 追问点

- sync.Map 场景。
- map 扩容。
- map 遍历无序。

## 12. Mutex、RWMutex、atomic 怎么选？

### 30 秒回答

复杂共享状态用 Mutex，读多写少且读临界区有一定成本时可以用 RWMutex，简单计数、状态标记、指针替换可以用 atomic。多个字段需要保持一致时，不要用多个 atomic 硬拼，应该用锁。

RWMutex 不一定比 Mutex 快，写多或读临界区很短时，RWMutex 的额外开销可能不划算。

### 追问点

- 锁内不要做慢 I/O。
- atomic 复合操作不自动原子。

## 13. context 有什么用？

### 30 秒回答

context 用于跨 API 边界传递取消信号、超时截止时间和少量请求级元数据。常见场景是 HTTP/gRPC 请求超时、客户端取消、服务关闭、worker 退出、下游 DB/RPC 调用取消。

context 是树形传播，父 context 取消后子 context 也会取消。使用 `WithTimeout`、`WithCancel` 后要 `defer cancel()` 释放资源。

### 追问点

- `Done()` channel。
- `cancelCtx` children。
- Value 不传业务参数。

## 14. worker pool 怎么设计？

### 30 秒回答

worker pool 用固定数量 worker 从有界 jobs channel 里取任务处理，控制并发和内存。它适合批量订单解析、物流轨迹处理、异步任务消费等场景。

关键点是 jobs channel 要有容量上限，worker 数根据 CPU 或下游能力设置，任务处理要支持 context 取消，关闭时要停止接收新任务并等待 worker 退出。

### 追问点

- 队列满怎么办。
- 错误如何收敛。
- 下游连接池限制。

## 15. gRPC 为什么适合内部微服务？

### 30 秒回答

gRPC + Protobuf 有强类型 IDL、代码生成、二进制序列化、HTTP/2 多路复用和流式能力，适合内部高频服务调用。对外开放接口一般 REST 更友好。

gRPC 的 `ClientConn` 并发安全，应该复用，不要每次请求 Dial。每个 RPC 都要设置 deadline，重试只对幂等接口开启。

### 追问点

- 连接池。
- metadata 传 trace。
- deadline 设计。

## 16. 微服务接口如何设计？

### 30 秒回答

接口设计要关注业务语义、数据归属、幂等、错误码、超时、重试、版本兼容和可观测性。订单状态由订单服务维护，库存数量由库存服务维护，其他服务不能直接跨库改数据。

创建订单、扣库存、支付、创建运单这类接口必须有幂等键或业务唯一键，避免超时重试导致重复执行。

### 追问点

- Idempotency-Key。
- 状态机。
- 错误码稳定。

## 17. 分布式事务怎么做？

### 30 秒回答

高并发微服务里不优先使用强 2PC，因为性能和可用性成本高。常见做法是最终一致：本地事务 + outbox 本地消息表 + MQ + 消费者幂等 + 重试补偿 + 定时对账。

资源预留类场景可以用 TCC，例如库存预占、余额冻结；长流程业务如履约、清关、退款适合 Saga。

### 追问点

- 2PC 阻塞。
- TCC 空回滚/悬挂。
- Saga 补偿。

## 18. 本地消息表解决什么问题？

### 30 秒回答

本地消息表解决“业务数据写成功，但消息发送失败”的一致性问题。在同一个数据库事务里写业务表和 outbox_message，事务提交后由后台任务可靠投递 MQ，发送成功再标记 sent。

消费者可能重复收到消息，所以必须幂等。

### 追问点

- outbox sender 重试。
- 消息重复。
- 对账补偿。

## 19. Redis 缓存三大问题怎么处理？

### 30 秒回答

缓存穿透是查不存在数据，可以缓存空值、布隆过滤器、参数校验。缓存击穿是热点 key 过期瞬间大量请求回源，可以用 singleflight、互斥锁、热点 key 后台刷新。缓存雪崩是大量 key 同时过期或 Redis 故障，可以 TTL 加随机抖动、多级缓存、限流降级。

核心交易数据不能只依赖缓存，缓存和数据库通常做最终一致。

### 追问点

- Cache Aside。
- 更新 DB 后删缓存。
- hot key/big key。

## 20. Redis 分布式锁怎么实现？

### 30 秒回答

基础实现是 `SET key token NX PX ttl`，token 是唯一值，释放时用 Lua 判断 token 一致再删除，避免误删别人的锁。

坑包括锁过期业务没执行完、不可重入、续期失败、Redis 主从切换。看门狗可以自动续期，但核心库存、支付、状态更新还要靠数据库条件更新、唯一约束、状态机或 fencing token 兜底。

### 追问点

- Redlock。
- 看门狗。
- fencing token。

## 21. 库存防超卖怎么设计？

### 30 秒回答

核心是原子扣减和幂等。数据库可以用条件更新：`UPDATE stock SET available=available-1 WHERE sku_id=? AND available>=1`，影响行数为 1 才成功。秒杀高并发可以用 Redis Lua 做前置原子扣减，再通过 MQ 异步创建订单，数据库库存流水和对账做最终兜底。

支付超时要释放库存，释放也必须幂等。

### 追问点

- locked_stock。
- 库存流水。
- Redis 和 DB 对账。

## 22. MySQL 深度分页怎么优化？

### 30 秒回答

`OFFSET 1000000 LIMIT 10` 慢是因为数据库要扫描并丢弃大量记录。优先用游标分页，通过上一页最后一条记录继续查询。必须跳页时可以用覆盖索引 + 延迟关联，先查主键再回表。

大数据导出不要在线深分页，应该异步导出。

### 追问点

- 覆盖索引。
- 延迟关联。
- created_at + order_id 复合游标。

## 23. 联合索引最左前缀怎么理解？

### 30 秒回答

联合索引 `(a,b)` 最适合 `WHERE a=?` 或 `WHERE a=? AND b=?`。`WHERE b=1 AND a=2` 虽然条件顺序写反，但优化器通常能重排等值条件，仍然可以使用 `(a,b)`。真正不能充分利用的是只有 `WHERE b=1`，因为缺少最左列 `a`。

范围条件后面的列是否能继续用于索引定位要特别注意。

### 追问点

- Using index。
- Using filesort。
- 索引下推。

## 24. MySQL MVCC 是什么？

### 30 秒回答

MVCC 是多版本并发控制。InnoDB 通过 undo log 版本链和 Read View 实现一致性读。普通 SELECT 是快照读，通常不加锁；UPDATE、DELETE、SELECT FOR UPDATE 是当前读，会读取最新数据并加锁。

Read Committed 每条 SQL 生成新的 Read View，Repeatable Read 通常事务内复用 Read View。

### 追问点

- 快照读 vs 当前读。
- 间隙锁。
- next-key lock。

## 25. Kafka 和 RabbitMQ 怎么选？

### 30 秒回答

Kafka 适合高吞吐事件流、日志、轨迹状态流、CDC、搜索索引同步和可回放场景。RabbitMQ 适合传统任务队列、复杂路由、通知、延迟重试和死信队列。

跨境物流里，轨迹事件和订单状态流更适合 Kafka；邮件短信、仓储任务、复杂 routing key 可以用 RabbitMQ。

### 追问点

- Kafka partition。
- consumer group。
- RabbitMQ exchange。

## 26. 如何避免消息丢失和重复消费？

### 30 秒回答

消息可靠性从三段看：生产者、broker、消费者。生产者用发送确认、本地消息表或事务消息；broker 开启持久化和多副本；消费者处理成功后再 ack 或提交 offset。

重复消费无法完全避免，所以消费者必须幂等。常见方式是 event_id 去重表、业务唯一索引、状态机条件更新。

### 追问点

- processed_message。
- ack 失败。
- offset 提交时机。

## 27. 物流跟踪系统怎么设计？

### 30 秒回答

采用事件驱动架构。承运商、仓储、清关、末端配送推送轨迹事件，接入层做签名校验、限流、标准化和幂等，然后写 MQ。轨迹服务消费事件，保存明细，更新 latest 状态，并发布 TrackingUpdated 事件。

乱序事件用状态机或 status_rank 防止主状态回退，重复事件用 event_id 去重。查询走 Redis 最新状态和明细缓存，缓存击穿用 singleflight。

### 追问点

- DHL/UPS/FedEx Driver。
- tracking_no 分区。
- WebSocket/SSE 推送。

## 28. 如何设计高性能订单查询 API？

### 30 秒回答

先拆场景：订单详情、商家订单列表、状态筛选、复杂搜索。订单详情走主键和缓存；列表用 `(tenant_id, created_at, order_id)` 联合索引和游标分页；状态筛选用 `(tenant_id, status, created_at, order_id)`。

复杂搜索不要压主库，可以同步到 Elasticsearch/OpenSearch。列表接口字段裁剪，避免 N+1 查询，所有查询必须带 tenant_id 做多租户隔离。

### 追问点

- 读写分离延迟。
- 列表缓存谨慎。
- 搜索读模型最终一致。

## 29. OpenTelemetry / Jaeger / Prometheus 怎么用？

### 30 秒回答

OpenTelemetry 用来统一采集和传播 trace、metrics、logs。Go 服务在 HTTP、gRPC、MQ 中透传 trace context，通过 OTLP 上报到 Collector，再导出到 Jaeger 查调用链。

Prometheus 采集 QPS、错误率、P95/P99、Goroutine、GC、DB 连接池、Redis 命中率、MQ lag、第三方接口耗时等指标，Grafana 展示，Alertmanager 告警。日志要带 trace_id、order_id、tracking_no。

### 追问点

- 高基数 label。
- MQ trace 传播。
- RED/USE。

## 30. Go 服务如何优雅停机？

### 30 秒回答

收到 SIGTERM 后，先标记 shutting down，让 readiness 失败，从负载均衡摘除流量。然后调用 `http.Server.Shutdown(ctx)` 停止接收新连接并等待已有请求完成。

MQ consumer 要停止拉新消息，等待正在处理的消息完成后再 ack 或提交 offset。worker pool 用 context 取消和 WaitGroup 等待退出。Kubernetes 中要配合 preStop 和 `terminationGracePeriodSeconds`。

### 追问点

- readiness vs liveness。
- 滚动发布。
- GOMEMLIMIT。
