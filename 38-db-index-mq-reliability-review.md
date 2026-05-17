# 慢查询索引优化与消息队列可靠性强化

这篇面向高级后端面试中两个非常高频的追问方向：

- MySQL/PostgreSQL 慢查询优化、深度分页、联合索引、B+ 树 IO 估算。
- RabbitMQ vs Kafka 选型、重复消费、消息丢失、可靠投递。

## 1. 慢查询优化与索引设计

### 高频问题

- 深度分页 `OFFSET 1000000 LIMIT 10` 如何优化？
- 联合索引 `(a,b)` 下，`WHERE b=1 AND a=2` 会走索引吗？
- B+ 树 3 层能存多少数据？查询走几次 IO？
- 如何使用 EXPLAIN 分析执行计划？
- 覆盖索引、延迟关联、游标分页分别是什么？

## 30 秒回答

慢查询优化要先用 `EXPLAIN` 看执行计划，确认是否走索引、扫描行数、是否回表、是否 filesort、是否临时表。深度分页 `OFFSET 1000000 LIMIT 10` 慢，是因为数据库要扫描并跳过大量行，可以用游标分页、延迟关联、覆盖索引或业务上限制跳页来优化。

联合索引遵循最左前缀原则。索引 `(a,b)` 最适合 `WHERE a=?`、`WHERE a=? AND b=?`。`WHERE b=1 AND a=2` 虽然 SQL 条件顺序写成 b 在前，但优化器通常可以重排等值条件，仍然可以使用 `(a,b)`；真正不能有效利用的是只写 `WHERE b=1`，因为缺少最左列 `a`。

B+ 树高度通常很低，3 层索引可以存储大量数据。一次主键索引查询大致需要从根节点到叶子节点的几次页访问，缓存命中时 IO 更少。二级索引如果不是覆盖索引，还需要回表查询主键索引。

## EXPLAIN 看什么

MySQL 常用：

```sql
EXPLAIN SELECT ...
```

重点字段：

| 字段 | 含义 |
| --- | --- |
| type | 访问类型，system/const/ref/range/index/ALL |
| possible_keys | 可能使用的索引 |
| key | 实际使用的索引 |
| rows | 估算扫描行数 |
| filtered | 过滤比例 |
| Extra | 额外信息，如 Using index、Using filesort |

### type 常见顺序

大致从好到差：

```text
system > const > eq_ref > ref > range > index > ALL
```

要警惕：

- `type = ALL`：全表扫描。
- `rows` 很大：扫描行数过多。
- `Using filesort`：需要额外排序。
- `Using temporary`：使用临时表。

## 深度分页为什么慢

SQL：

```sql
SELECT *
FROM orders
WHERE tenant_id = 1001
ORDER BY created_at DESC
LIMIT 1000000, 10;
```

数据库需要找到符合条件的前 1000010 条记录，然后丢弃前 1000000 条，只返回 10 条。

问题：

- 扫描行数大。
- 排序成本高。
- 可能大量回表。
- offset 越大越慢。

## 深度分页优化方案

### 1. 游标分页

用上一页最后一条记录作为游标。

```sql
SELECT order_id, status, created_at
FROM orders
WHERE tenant_id = ?
  AND created_at < ?
ORDER BY created_at DESC
LIMIT 10;
```

如果 `created_at` 可能重复，用复合游标：

```sql
SELECT order_id, status, created_at
FROM orders
WHERE tenant_id = ?
  AND (
    created_at < ?
    OR (created_at = ? AND order_id < ?)
  )
ORDER BY created_at DESC, order_id DESC
LIMIT 10;
```

索引：

```text
(tenant_id, created_at, order_id)
```

优点：

- 性能稳定。
- 不随页数变深明显变慢。

缺点：

- 不适合直接跳到第 N 页。
- 前端需要使用 cursor。

### 2. 延迟关联

先用覆盖索引查出主键，再回表关联完整数据。

原始 SQL：

```sql
SELECT *
FROM orders
WHERE tenant_id = ?
ORDER BY created_at DESC
LIMIT 1000000, 10;
```

优化：

```sql
SELECT o.*
FROM orders o
JOIN (
    SELECT order_id
    FROM orders
    WHERE tenant_id = ?
    ORDER BY created_at DESC
    LIMIT 1000000, 10
) t ON o.order_id = t.order_id;
```

子查询只走覆盖索引，不读取整行，减少回表成本。

适合：

- 不能改成交互式 cursor。
- 必须支持一定程度跳页。
- 结果行很宽。

注意：offset 很大时仍然要扫描很多索引记录，只是比扫描整行轻。

### 3. 覆盖索引

如果查询字段都在索引里，避免回表。

```sql
SELECT order_id, status, created_at
FROM orders
WHERE tenant_id = ?
ORDER BY created_at DESC
LIMIT 20;
```

索引：

```text
(tenant_id, created_at, order_id, status)
```

`Extra` 可能出现：

```text
Using index
```

表示覆盖索引。

### 4. 限制深翻页

业务上限制最大页数，比如最多查前 100 页。后台导出走异步任务，不能让用户在线深翻几百万条。

### 5. 搜索引擎或读模型

复杂筛选和深分页可以同步到 Elasticsearch/OpenSearch 或专门读模型，但也要注意搜索引擎深分页成本。

## 联合索引最左前缀

索引：

```text
(a, b)
```

可以有效使用：

```sql
WHERE a = 2;
```

```sql
WHERE a = 2 AND b = 1;
```

SQL 条件顺序不重要：

```sql
WHERE b = 1 AND a = 2;
```

优化器通常会重排等值条件，仍然可使用 `(a,b)`。

不能有效使用最左列：

```sql
WHERE b = 1;
```

因为缺少 `a`。

## 联合索引范围条件

索引：

```text
(tenant_id, status, created_at)
```

SQL：

```sql
WHERE tenant_id = ?
  AND status = ?
  AND created_at > ?
```

可以使用 tenant_id、status、created_at。

如果：

```sql
WHERE tenant_id = ?
  AND created_at > ?
  AND status = ?
```

是否能用 status，要看优化器和索引顺序。一般原则是联合索引中遇到范围查询后，后续列很难继续用于精确定位。

所以如果常见查询是：

```sql
WHERE tenant_id = ?
  AND status = ?
ORDER BY created_at DESC
```

索引应设计为：

```text
(tenant_id, status, created_at)
```

## 索引下推

MySQL 的 Index Condition Pushdown 可以在存储引擎层用索引中的字段先过滤，减少回表。

但不要把它当成万能优化，索引顺序仍然要根据查询模式设计。

## 覆盖索引

覆盖索引是指查询需要的字段都在索引中，不需要回表。

例子：

```sql
SELECT order_id, status
FROM orders
WHERE tenant_id = ?
ORDER BY created_at DESC
LIMIT 20;
```

索引：

```text
(tenant_id, created_at, order_id, status)
```

优点：

- 减少随机 IO。
- 减少回表。
- 对高频列表查询很有用。

缺点：

- 索引变大。
- 写入成本增加。
- 索引维护成本增加。

## B+ 树 3 层能存多少数据

面试一般考估算能力，不需要精确到字节。

假设：

- InnoDB 页大小 16KB。
- 主键 BIGINT 8 字节。
- 子指针约 6 到 8 字节。
- 内部节点每个索引项约 16 字节左右。

一个内部页大概能放：

```text
16KB / 16B ≈ 1000 个索引项
```

如果 B+ 树 3 层：

```text
root
  -> internal
    -> leaf
```

内部节点扇出约 1000。

叶子页能放多少行取决于行大小。

如果一行 1KB：

```text
每个叶子页约 16 行
3 层约 1000 * 1000 * 16 = 1600 万行
```

如果一行 200B：

```text
每个叶子页约 80 行
3 层约 1000 * 1000 * 80 = 8000 万行
```

所以 3 层 B+ 树支撑千万到上亿级数据是常见的。实际还受页头、行格式、填充率、主键大小、二级索引结构影响。

## B+ 树查询几次 IO

如果索引页都不在缓存中：

- 3 层主键索引查询大致 3 次页访问。
- 根节点通常常驻内存，所以实际磁盘 IO 可能更少。
- 如果二级索引非覆盖，需要先查二级索引，再回表查主键索引。

二级索引非覆盖查询：

```text
二级索引 B+ 树查到主键
再查主键 B+ 树拿完整行
```

可能是：

```text
二级索引 3 次 + 主键索引 3 次
```

但实际数据库有 buffer pool，不能简单等同真实磁盘 IO 次数。

## 面试回答模板：索引优化

深度分页慢是因为 offset 很大时，数据库需要扫描并丢弃大量记录。优化上优先用游标分页，通过上一页最后一条记录继续查；如果业务必须支持跳页，可以用延迟关联，先通过覆盖索引查主键，再回表查完整行；列表接口还可以用覆盖索引减少回表。

联合索引 `(a,b)` 遵循最左前缀。`WHERE b=1 AND a=2` 虽然写法上 b 在前，但优化器通常能重排等值条件，所以仍可能走 `(a,b)`；真正不能充分利用的是只写 `WHERE b=1`。范围查询后面的列是否能继续用于索引定位要特别注意。

B+ 树高度通常很低，3 层可以支撑千万到上亿级数据。一次主键查询大致从 root 到 leaf 访问几层页，根节点通常在内存里。二级索引如果不是覆盖索引，还要回表，所以高频列表查询要尽量设计覆盖索引。

## 2. 消息队列选型与可靠性

### 高频问题

- RabbitMQ 还是 Kafka？物流场景中如何选型？
- 如何解决消息重复消费？
- 如何解决消息丢失？
- 如何保证消息顺序？
- MQ 积压怎么办？

## 30 秒回答

Kafka 更适合高吞吐事件流、日志流、轨迹状态流、可回放和多消费者订阅；RabbitMQ 更适合传统业务队列、灵活路由、任务分发、延迟/死信队列等场景。跨境物流里，轨迹事件、订单状态变更、日志、CDC 更适合 Kafka；邮件短信、仓储任务、低吞吐但路由复杂的业务任务可以用 RabbitMQ。

消息可靠性要从生产者、MQ broker、消费者三段保证。生产端要有发送确认、本地消息表或事务消息；broker 要开启持久化、多副本或镜像/仲裁队列；消费者要处理成功后再 ack。重复消费无法完全避免，所以消费者必须幂等，常用 event_id、业务唯一键、去重表、唯一索引和状态机。

## RabbitMQ 和 Kafka 对比

| 维度 | RabbitMQ | Kafka |
| --- | --- | --- |
| 模型 | Exchange + Queue | Topic + Partition |
| 优势 | 路由灵活、任务队列、ack、死信 | 高吞吐、事件流、可回放、分区扩展 |
| 顺序性 | 单队列单消费者较容易保证 | 单 partition 内有序 |
| 消费模式 | 消息被消费确认后从队列删除 | 消费者维护 offset，可回放 |
| 适合 | 通知、任务分发、复杂路由 | 轨迹流、日志、订单事件、CDC |
| 吞吐 | 通常低于 Kafka | 高吞吐 |
| 延迟 | 低延迟任务较常见 | 高吞吐流式处理 |

## 物流场景如何选型

### Kafka 更适合

- 物流轨迹事件流。
- 订单状态变更事件。
- 用户行为日志。
- API 调用日志。
- CDC 数据同步。
- 搜索索引同步。
- BI/数仓数据流。

原因：

- 吞吐高。
- 分区扩展。
- 多消费者组订阅。
- 消息可保留和回放。

### RabbitMQ 更适合

- 邮件短信通知。
- 仓储任务分发。
- 需要复杂 routing key 的任务。
- 延迟重试队列。
- 死信队列。
- 低到中等吞吐业务命令。

原因：

- 路由模型灵活。
- ack 和死信机制成熟。
- 任务队列语义直观。

## 消息丢失的三个阶段

```text
生产者 -> MQ broker -> 消费者
```

### 1. 生产者丢失

问题：

- 业务数据写成功，但消息没发出去。
- 消息发送失败但应用没感知。
- 网络超时，不知道 broker 是否收到。

解决：

- 发送确认。
- 本地消息表 outbox。
- 事务消息。
- 失败重试。

最稳妥常见方案：

```text
begin
  写业务表
  写 outbox_message
commit

后台扫描 outbox 发送 MQ
发送成功标记 sent
```

### 2. Broker 丢失

RabbitMQ：

- 队列持久化。
- 消息持久化。
- publisher confirm。
- quorum queue 或镜像机制。

Kafka：

- replication factor。
- producer acks=all。
- min.insync.replicas。
- 合理配置重试。
- 避免 unclean leader election。

### 3. 消费者丢失

问题：

- 消费者拿到消息后还没处理完就 ack。
- 处理失败但提交 offset。

解决：

- 处理成功后再 ack 或提交 offset。
- 失败不 ack，进入重试或死信。
- 消费者幂等，允许重复投递。

## 重复消费怎么解决

消息系统通常更容易做到至少一次投递，而不是严格一次。

重复来源：

- 生产者重试。
- broker 重投。
- 消费者处理成功但 ack 失败。
- 消费者 rebalance 后重复处理。
- outbox sender 重复发送。

解决：消费者幂等。

### 1. event_id 去重

消息带唯一 ID：

```json
{
  "event_id": "shipment-1001-outbound-v1",
  "event_type": "ShipmentOutbound",
  "shipment_id": 1001
}
```

消费记录表：

```sql
CREATE TABLE processed_message (
    event_id VARCHAR(128) NOT NULL,
    consumer_group VARCHAR(64) NOT NULL,
    processed_at TIMESTAMP NOT NULL,
    PRIMARY KEY(event_id, consumer_group)
);
```

插入冲突说明已处理。

### 2. 业务唯一索引

账务流水：

```sql
UNIQUE(event_id)
```

库存流水：

```sql
UNIQUE(request_id)
```

订单状态流水：

```sql
UNIQUE(order_id, status)
```

### 3. 状态机

重复状态事件不会重复生效。

```sql
UPDATE shipment
SET status = 'OUTBOUND'
WHERE shipment_id = ?
  AND status = 'PACKED';
```

如果已经 `OUTBOUND`，重复消息可以直接返回成功。

### 4. Redis SetNX 前置去重

```text
SETNX mq:processed:{event_id}:{consumer_group} 1
EX 7d
```

适合减轻数据库压力，但不能作为财务类最终兜底。最终仍要靠数据库唯一约束。

## 如何保证消息顺序

### Kafka

Kafka 只保证单 partition 内有序。

如果同一订单需要有序：

```text
key = order_id
```

如果同一包裹轨迹需要有序：

```text
key = tracking_no
```

注意：

- 不同 partition 之间无全局顺序。
- 单 key 热点可能导致某个 partition 压力大。
- 消费失败可能阻塞同 partition 后续消息。

### RabbitMQ

单队列单消费者更容易保证顺序。多个消费者并发时，处理完成顺序可能变化。

实际业务仍建议用状态机和版本号防乱序，而不是完全依赖 MQ 顺序。

## MQ 积压怎么办

原因：

- 消费者处理慢。
- 下游 DB 或第三方接口慢。
- 消费者实例不足。
- 单条毒性消息反复失败。
- Kafka partition 数不足。
- 消费者发生频繁 rebalance。

处理：

- 扩容消费者。
- 增加 partition。
- 批量消费。
- 优化下游 SQL。
- 降级非核心任务。
- 毒性消息进死信队列。
- 按租户或业务优先级拆 topic。
- 监控 consumer lag 和消费耗时。

## 面试回答模板：MQ 可靠性

RabbitMQ 和 Kafka 的选择要看业务。Kafka 更适合高吞吐事件流，比如物流轨迹、订单状态、日志、CDC 和搜索索引同步，因为它分区扩展能力强、消息可回放、多消费者组方便。RabbitMQ 更适合任务队列和复杂路由，比如通知、仓储任务、延迟重试和死信队列。

消息可靠性要从生产者、broker、消费者三段考虑。生产者用发送确认、本地消息表或事务消息，保证业务数据和消息不丢；broker 开启持久化和多副本；消费者处理成功后再 ack 或提交 offset。

重复消费无法完全避免，所以消费者必须幂等。常见做法是 event_id 去重表、业务唯一索引、状态机条件更新。财务、库存这类核心业务不能只靠 Redis SetNX 去重，必须有数据库唯一约束兜底。

## 高频追问速答

### 1. `WHERE b=1 AND a=2` 会走 `(a,b)` 索引吗？

通常会。SQL 条件书写顺序不等于索引使用顺序，优化器可以重排等值条件。真正的问题是只有 `WHERE b=1` 时，缺少最左列 `a`，不能充分利用 `(a,b)`。

### 2. 深度分页最推荐什么？

交互式列表优先用游标分页。必须跳页时可以用覆盖索引 + 延迟关联，但 offset 极深仍然有成本。大数据导出走异步任务。

### 3. 覆盖索引为什么快？

查询字段都在索引中，不需要回表读取完整行，减少随机 IO。

### 4. Kafka 能保证消息不重复吗？

不能简单这么说。Kafka 可以通过幂等生产者和事务增强语义，但端到端业务仍然要消费者幂等，因为重试、rebalance、offset 提交时机都可能导致重复处理。

### 5. 消息丢了怎么排查？

看生产者发送确认、outbox 状态、broker 持久化和副本状态、消费者 ack/offset、死信队列、消费日志和 event_id 是否被处理。
