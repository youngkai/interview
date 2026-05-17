# 数据库和缓存实战题

## 题目 1：秒杀系统如何设计库存表避免超卖？

### 30 秒回答

避免超卖的核心是库存扣减必须原子化。数据库层可以用“条件更新”保证库存不会扣成负数，例如 `UPDATE stock SET available_stock = available_stock - 1 WHERE sku_id = ? AND available_stock > 0`，然后检查影响行数。高并发秒杀场景下，还要加 Redis 前置扣减、限流、MQ 削峰、订单幂等、库存流水和对账补偿。

库存表设计上建议区分总库存、可用库存、锁定库存，并记录库存流水。下单时先预占库存，支付成功后确认扣减，支付超时或取消订单时释放库存。

## 库存表设计

基础库存表：

```sql
CREATE TABLE sku_stock (
    sku_id BIGINT PRIMARY KEY,
    total_stock BIGINT NOT NULL,
    available_stock BIGINT NOT NULL,
    locked_stock BIGINT NOT NULL DEFAULT 0,
    sold_stock BIGINT NOT NULL DEFAULT 0,
    version BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMP NOT NULL
);
```

字段含义：

- `total_stock`：总库存。
- `available_stock`：可售库存。
- `locked_stock`：已预占但未最终确认的库存。
- `sold_stock`：已售库存。
- `version`：乐观锁版本。

约束关系：

```text
total_stock = available_stock + locked_stock + sold_stock
```

实际业务中也可能按仓库维度拆：

```text
sku_id + warehouse_id
```

跨境物流场景还可能按国家、仓库、批次拆库存。

## 库存流水表

```sql
CREATE TABLE stock_flow (
    id BIGINT PRIMARY KEY,
    request_id VARCHAR(128) NOT NULL,
    order_id BIGINT NOT NULL,
    sku_id BIGINT NOT NULL,
    change_type VARCHAR(32) NOT NULL,
    change_amount BIGINT NOT NULL,
    status VARCHAR(32) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    UNIQUE (request_id)
);
```

`request_id` 用于幂等，防止重复扣减或重复释放。

常见流水类型：

- `RESERVE`：预占。
- `CONFIRM`：确认扣减。
- `RELEASE`：释放预占。
- `ROLLBACK`：回滚。

## 方案一：数据库条件更新防超卖

预占库存：

```sql
UPDATE sku_stock
SET available_stock = available_stock - ?,
    locked_stock = locked_stock + ?,
    version = version + 1,
    updated_at = NOW()
WHERE sku_id = ?
  AND available_stock >= ?;
```

检查影响行数：

- 1：预占成功。
- 0：库存不足。

确认扣减：

```sql
UPDATE sku_stock
SET locked_stock = locked_stock - ?,
    sold_stock = sold_stock + ?,
    version = version + 1,
    updated_at = NOW()
WHERE sku_id = ?
  AND locked_stock >= ?;
```

释放库存：

```sql
UPDATE sku_stock
SET locked_stock = locked_stock - ?,
    available_stock = available_stock + ?,
    version = version + 1,
    updated_at = NOW()
WHERE sku_id = ?
  AND locked_stock >= ?;
```

这些更新都在数据库里完成判断和修改，是原子的。

## 方案二：Redis 前置扣减

秒杀流量很大时，所有请求直接打数据库会把数据库打爆。

可以活动开始前把库存加载到 Redis：

```text
seckill:stock:{sku_id} = 1000
```

扣减用 Lua：

```lua
local stock = tonumber(redis.call("GET", KEYS[1]))
if stock == nil or stock < tonumber(ARGV[1]) then
    return 0
end
redis.call("DECRBY", KEYS[1], ARGV[1])
return 1
```

Redis 扣减成功后，发送 MQ 异步创建订单和落库扣减。

注意：Redis 是前置流量控制，数据库和库存流水仍是最终事实来源。

## 防重复下单

数据库唯一约束：

```sql
CREATE UNIQUE INDEX uk_seckill_user_sku
ON seckill_order(activity_id, sku_id, user_id);
```

Redis 也可以前置拦截：

```text
SETNX seckill:ordered:{activity_id}:{sku_id}:{user_id} request_id
```

数据库唯一约束是最终兜底。

## 幂等处理

每次扣减请求带 `request_id`。

处理流程：

1. 插入库存流水，`request_id` 唯一。
2. 插入成功才执行扣减。
3. 唯一冲突说明重复请求，查询原流水返回结果。

这样可以处理 MQ 重复消息和接口重试。

## 支付超时释放库存

流程：

```text
下单成功 -> 预占库存 -> 订单 WAIT_PAY
15 分钟未支付 -> 延迟消息触发取消
取消订单 -> 释放 locked_stock
```

取消和支付并发时，用订单状态条件更新：

```sql
UPDATE orders
SET status = 'CANCELED'
WHERE order_id = ?
  AND status = 'WAIT_PAY';
```

只有取消成功后才释放库存。

## 面试回答模板

秒杀系统避免超卖，库存扣减必须在数据库或 Redis 中原子完成。数据库层我会设计 `available_stock`、`locked_stock`、`sold_stock`，下单时用条件更新把可用库存转成锁定库存，条件是 `available_stock >= buy_count`，影响行数为 1 才算成功。支付成功后把 locked 转 sold，支付超时或取消则把 locked 释放回 available。

高并发下，不能让所有请求直接打数据库，所以会在前面加网关限流、Redis Lua 原子预扣、MQ 异步削峰。订单和库存扣减都要有幂等键，数据库唯一索引兜底，比如 `activity_id + sku_id + user_id` 防重复下单，库存流水用 `request_id` 防重复扣减。

最后还要有对账补偿，因为 Redis、MQ、数据库之间可能出现部分失败。通过订单、库存流水、Redis 扣减记录做对账，修复异常。

## 常见追问

### 1. 为什么要 locked_stock？

因为下单和支付之间有时间差。下单时不能直接算已售，也不能不占库存。预占到 locked_stock，可以防止别人买走；支付失败或超时再释放。

### 2. 只用 Redis 扣库存可以吗？

Redis 可以做前置扣减，但最终仍要落数据库并记录库存流水。否则 Redis 故障、消息失败、数据丢失时很难对账。

### 3. 数据库条件更新为什么能防超卖？

因为判断库存是否足够和扣减库存在同一条 UPDATE 中完成，数据库会保证这一行更新的原子性。

## 题目 2：如何设计一个高性能订单查询 API？

### 30 秒回答

高性能订单查询 API 要先明确查询场景：订单详情、订单列表、状态筛选、时间范围查询、物流轨迹查询。设计上要使用合适索引、游标分页、缓存热点数据、读写分离、字段裁剪、避免深分页和 N+1 查询。订单量很大时，可以引入搜索引擎或读模型，把复杂多条件查询从主库转移出去。

跨境物流订单查询还要考虑多租户隔离、权限校验、实时性和状态最终一致。核心订单详情可以查数据库或缓存，复杂检索走 Elasticsearch/OpenSearch，物流轨迹可以从轨迹服务或缓存中聚合。

## API 场景拆分

### 1. 订单详情

```http
GET /orders/{order_id}
```

特点：

- 按主键查询。
- QPS 可能较高。
- 可以缓存订单摘要。

### 2. 商家订单列表

```http
GET /orders?tenant_id=1001&cursor=xxx&limit=20
```

特点：

- 按租户查询。
- 按创建时间倒序。
- 需要分页。

### 3. 状态筛选

```http
GET /orders?tenant_id=1001&status=SHIPPED&cursor=xxx&limit=20
```

### 4. 复杂搜索

例如：

- 按收件人姓名。
- 按手机号。
- 按 tracking_no。
- 按地址关键字。
- 多状态组合。

这类可以考虑搜索引擎或专门读模型。

## 数据库索引设计

订单详情：

```text
PRIMARY KEY(order_id)
```

商家订单列表：

```text
(tenant_id, created_at, order_id)
```

状态筛选：

```text
(tenant_id, status, created_at, order_id)
```

外部订单号：

```text
UNIQUE(tenant_id, external_order_no)
```

物流单号查询：

```text
(tenant_id, tracking_no)
```

注意：索引要根据实际查询设计，不要无限加索引。

## 分页设计

避免深分页：

```sql
LIMIT 100000, 20
```

使用游标分页：

```sql
SELECT order_id, status, created_at, total_amount
FROM orders
WHERE tenant_id = ?
  AND created_at < ?
ORDER BY created_at DESC
LIMIT 20;
```

如果 created_at 可能重复，可以加 order_id 作为稳定游标：

```sql
WHERE tenant_id = ?
  AND (
    created_at < ?
    OR (created_at = ? AND order_id < ?)
  )
ORDER BY created_at DESC, order_id DESC
LIMIT 20;
```

对应索引：

```text
(tenant_id, created_at, order_id)
```

## 缓存设计

### 1. 订单详情缓存

key：

```text
order:detail:{order_id}
```

写策略：

```text
更新数据库 -> 删除缓存
```

适合缓存：

- 订单摘要。
- 最新状态。
- 物流最新节点。

不适合缓存或要短 TTL：

- 支付中状态。
- 刚刚变更的强一致状态。

### 2. 列表缓存谨慎使用

订单列表变化频繁，缓存失效复杂。可以缓存第一页热点列表，但要设置短 TTL。

更常见做法：

- 数据库索引优化。
- 搜索引擎读模型。
- 游标分页。

### 3. 防止缓存问题

- 缓存空值防穿透。
- 热点订单 singleflight 防击穿。
- TTL 加随机抖动防雪崩。

## 字段裁剪

列表接口不要返回所有字段。

列表返回：

```json
{
  "order_id": "1001",
  "status": "SHIPPED",
  "created_at": "2026-05-16T10:00:00Z",
  "total_amount": "12.30",
  "tracking_no": "YT123"
}
```

详情页再查详细字段。

避免大字段：

- 原始承运商 payload。
- 清关文档。
- 备注长文本。
- 操作日志。

## 避免 N+1 查询

错误方式：

```text
查 20 个订单
每个订单再查一次轨迹
每个订单再查一次商品
```

优化：

- 批量查询。
- join 合理使用。
- 聚合读模型。
- 详情按需加载。

## 读写分离

查询流量大时，可以走只读库。

注意：

- 主从延迟。
- 刚写完马上查可能读不到。
- 核心强一致查询可读主库。

例如用户刚创建订单后跳转详情页，可以短时间读主库或使用写后读一致性策略。

## 搜索引擎读模型

复杂搜索可以将订单数据同步到 Elasticsearch/OpenSearch。

适合：

- 多条件筛选。
- 模糊搜索。
- 聚合统计。
- 后台运营查询。

同步方式：

- MQ 订单变更事件。
- CDC。
- 定时补偿同步。

注意：

- 搜索引擎是读模型，不是交易事实来源。
- 要处理同步延迟和重建索引。

## 多租户和权限

所有查询必须带租户或权限约束：

```sql
WHERE tenant_id = ?
```

不能只靠 order_id，否则可能越权查询。

开放平台 API 还要有：

- 认证。
- 租户限流。
- 字段脱敏。
- 审计日志。

## 面试回答模板

高性能订单查询 API 首先要按场景拆分。订单详情走 order_id 主键查询，可以加短 TTL 缓存；订单列表按 tenant_id 和 created_at 做游标分页，避免深分页；状态筛选用 `(tenant_id, status, created_at, order_id)` 这类联合索引。列表接口做字段裁剪，不返回大字段。

复杂搜索不要硬压主库，比如按收件人、手机号、tracking_no、地址关键字多条件查询，可以通过 MQ 或 CDC 同步到 Elasticsearch/OpenSearch 做读模型。查询热点数据可以用 Redis 缓存，但更新订单状态时要先更新数据库再删除缓存，并设置 TTL 兜底。

跨境物流还要考虑多租户隔离和实时性。所有查询都要带 tenant_id 做权限过滤。读写分离时要关注主从延迟，用户刚下单后的详情查询可以读主库或走写后读一致性策略。

## 常见追问

### 1. 为什么不用 offset 深分页？

offset 很大时，数据库要扫描并跳过大量数据，性能差。游标分页可以利用索引从上次位置继续查。

### 2. 订单列表要不要缓存？

列表变化频繁，缓存失效复杂。可以缓存热点第一页短 TTL，但主要还是靠索引、游标分页和读模型。

### 3. 搜索引擎数据延迟怎么办？

搜索引擎作为读模型允许短暂延迟。核心详情以数据库为准。可以通过 MQ/CDC 同步、失败重试、定时校验和重建索引保证最终一致。

