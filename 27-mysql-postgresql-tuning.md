# MySQL/PostgreSQL 调优：索引、事务隔离级别

## 30 秒回答

MySQL/PostgreSQL 调优通常从慢查询、索引、SQL 写法、事务、连接池和表结构几个方向入手。跨境物流订单量大、轨迹事件多、查询实时性要求高，核心是让高频查询走合适索引，避免大事务和长事务，控制连接池，减少锁冲突，并通过分区、归档、读写分离、缓存和搜索引擎分担压力。

事务隔离级别要结合业务选择。默认级别一般能满足大部分业务，但库存扣减、支付状态更新、订单状态流转这类核心逻辑，必须通过条件更新、唯一约束、乐观锁或行锁保证并发正确，而不是只依赖“把隔离级别调高”。

## 调优先看什么

### 1. 慢查询

先找到慢 SQL，而不是凭感觉建索引。

常看：

- SQL 执行时间。
- 扫描行数。
- 返回行数。
- 是否走索引。
- 是否 filesort。
- 是否临时表。
- 锁等待时间。

MySQL 用：

```sql
EXPLAIN SELECT ...;
```

PostgreSQL 用：

```sql
EXPLAIN ANALYZE SELECT ...;
```

### 2. QPS 和延迟

关注：

- P95/P99 查询延迟。
- 写入 TPS。
- 连接数。
- 锁等待。
- 慢查询数量。
- Buffer cache 命中率。

### 3. 表数据量和增长趋势

跨境物流系统里这些表容易膨胀：

- 订单表。
- 订单状态流水表。
- 物流轨迹事件表。
- 库存流水表。
- 操作日志表。
- API 调用日志表。

需要提前考虑归档、分区或冷热分离。

## 索引设计原则

### 1. 根据查询模式建索引

不要看到字段就建索引，要看高频 SQL。

订单详情：

```sql
SELECT *
FROM orders
WHERE order_id = ?;
```

适合主键或唯一索引：

```text
order_id
```

商家订单列表：

```sql
SELECT *
FROM orders
WHERE tenant_id = ?
ORDER BY created_at DESC
LIMIT 20;
```

适合联合索引：

```text
(tenant_id, created_at)
```

订单状态查询：

```sql
SELECT *
FROM orders
WHERE tenant_id = ?
  AND status = ?
ORDER BY created_at DESC
LIMIT 20;
```

适合：

```text
(tenant_id, status, created_at)
```

### 2. 联合索引遵循最左前缀

索引：

```text
(tenant_id, status, created_at)
```

可以支持：

```text
tenant_id
tenant_id + status
tenant_id + status + created_at
```

不适合单独按 `status` 查询。

### 3. 区分度太低的字段不适合单独建索引

例如订单状态只有几个值：

```text
WAIT_PAY, PAID, SHIPPED, DELIVERED
```

单独给 `status` 建索引，选择性可能很差。通常要和 `tenant_id`、`created_at` 组合。

### 4. 覆盖索引

如果查询字段都在索引里，可以减少回表。

```sql
SELECT order_id, status, created_at
FROM orders
WHERE tenant_id = ?
ORDER BY created_at DESC
LIMIT 20;
```

索引可以设计为：

```text
(tenant_id, created_at, order_id, status)
```

是否值得做覆盖索引，要看查询频率和写入成本。

### 5. 避免索引失效

常见问题：

- 对索引列使用函数。
- 隐式类型转换。
- 前置模糊匹配。
- OR 条件不当。
- 联合索引不满足最左前缀。

低效示例：

```sql
WHERE DATE(created_at) = '2026-05-16'
```

更好：

```sql
WHERE created_at >= '2026-05-16 00:00:00'
  AND created_at <  '2026-05-17 00:00:00'
```

## 事务隔离级别

### 常见隔离级别

| 隔离级别 | 能解决的问题 | 代价 |
| --- | --- | --- |
| Read Uncommitted | 基本不用 | 可能脏读 |
| Read Committed | 避免脏读 | 可能不可重复读 |
| Repeatable Read | 避免脏读、不可重复读 | 锁和 MVCC 成本更高 |
| Serializable | 最强隔离 | 并发性能最低 |

MySQL InnoDB 默认通常是 Repeatable Read。PostgreSQL 默认通常是 Read Committed。

## 隔离级别相关问题

### 1. 脏读

读到其他事务尚未提交的数据。

### 2. 不可重复读

同一事务中两次读取同一行，结果不同。

### 3. 幻读

同一事务中按条件查询，两次查到的行集合不同。

## 库存扣减不要只靠隔离级别

防超卖常用条件更新：

```sql
UPDATE sku_stock
SET available_stock = available_stock - ?
WHERE sku_id = ?
  AND available_stock >= ?;
```

然后检查影响行数：

- 1：扣减成功。
- 0：库存不足。

这个写法比“先查库存再更新”安全。

不安全示例：

```sql
SELECT available_stock FROM sku_stock WHERE sku_id = ?;
-- 应用判断库存够
UPDATE sku_stock SET available_stock = available_stock - 1 WHERE sku_id = ?;
```

并发下多个事务可能都读到库存足够。

## 乐观锁

表中增加 version：

```sql
UPDATE orders
SET status = ?, version = version + 1
WHERE order_id = ?
  AND version = ?;
```

适合：

- 冲突不太高。
- 状态更新。
- 防止覆盖写。

如果影响行数为 0，说明版本已变化，需要重试或返回冲突。

## 悲观锁

使用 `SELECT ... FOR UPDATE` 锁定行。

```sql
BEGIN;

SELECT *
FROM sku_stock
WHERE sku_id = ?
FOR UPDATE;

UPDATE sku_stock
SET available_stock = available_stock - 1
WHERE sku_id = ?;

COMMIT;
```

适合：

- 强一致要求高。
- 冲突较明显。
- 必须串行修改同一资源。

缺点：

- 锁等待增加。
- 容易降低并发。
- 长事务风险大。

## 长事务问题

长事务会带来：

- 锁长期不释放。
- MVCC 版本堆积。
- undo/redo 压力。
- 影响 vacuum 或 purge。
- 造成复制延迟。

跨境物流订单流程不要把外部 RPC、MQ 发送、文件上传放在数据库事务里。

错误示例：

```text
BEGIN
  写订单
  调用库存服务
  调用承运商 API
  发送 MQ
COMMIT
```

事务里应该只做本地数据库必要操作。

## 连接池调优

应用侧连接池不是越大越好。过大的连接池会让数据库上下文切换和锁竞争更严重。

Go 中常见设置：

```go
db.SetMaxOpenConns(100)
db.SetMaxIdleConns(20)
db.SetConnMaxLifetime(time.Hour)
db.SetConnMaxIdleTime(10 * time.Minute)
```

要结合数据库能力、服务实例数和查询耗时计算。

如果有 10 个服务实例，每个实例 `MaxOpenConns=100`，数据库最多可能被打 1000 个连接。

## PostgreSQL 额外关注点

### 1. EXPLAIN ANALYZE

PostgreSQL 的 `EXPLAIN ANALYZE` 会实际执行 SQL，能看到真实耗时。

### 2. Vacuum

PostgreSQL 使用 MVCC，更新和删除会产生 dead tuples，需要 vacuum 清理。

长事务会影响 vacuum，导致表膨胀。

### 3. JSONB

JSONB 适合存扩展字段，例如承运商原始回调、不同国家的清关附加信息。

但核心查询字段不要只放 JSONB 里，否则索引和约束会复杂。

## MySQL 额外关注点

### 1. InnoDB 聚簇索引

主键决定数据物理组织。主键要尽量稳定、短、递增或趋势递增。

### 2. 回表

二级索引查到主键后，再回主键索引取完整行。覆盖索引可以减少回表。

### 3. 间隙锁

InnoDB 在某些隔离级别和查询条件下可能产生 gap lock，影响并发写入。

## 面试回答模板

MySQL/PostgreSQL 调优我会先从慢查询和执行计划入手，确认瓶颈是索引、SQL、锁、连接池还是数据量。订单系统里最常见的是订单详情、商家订单列表、状态筛选和时间范围查询，所以索引要围绕 `order_id`、`tenant_id`、`status`、`created_at` 这些查询模式设计，而不是盲目给每个字段建索引。

事务方面，我不会简单通过提高隔离级别解决所有并发问题。库存扣减要用条件更新，订单状态流转要用状态机、乐观锁或条件更新，幂等要靠唯一约束。事务要短，不能在事务里调用外部服务或发送慢请求。

对于大数据量表，还要考虑归档、分区、读写分离、缓存和搜索引擎。连接池也要控制，否则服务实例一多会把数据库连接打满。

## 常见追问

### 1. 索引是不是越多越好？

不是。索引提升查询，但会增加写入成本、存储空间和维护成本。高频查询和约束字段才值得建索引。

### 2. 库存扣减为什么用条件更新？

因为判断库存和扣减库存必须在数据库里原子完成。`WHERE available_stock >= ?` 可以保证并发下不会扣成负数。

### 3. 什么时候用乐观锁，什么时候用悲观锁？

冲突少时用乐观锁，失败后重试或返回冲突；冲突高且必须串行时用悲观锁，但要控制事务时间。

