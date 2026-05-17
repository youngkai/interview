# MySQL MVCC、锁与死锁排查

## 30 秒回答

MySQL InnoDB 的并发控制核心是 MVCC 和锁。普通 `SELECT` 通常是快照读，通过 undo log 和 Read View 看到事务开始时或语句开始时的一致性视图；`UPDATE`、`DELETE`、`SELECT ... FOR UPDATE` 是当前读，需要读取最新数据并加锁。

InnoDB 常见锁包括行锁、间隙锁、next-key lock。Repeatable Read 下，为了避免幻读，范围查询可能加 next-key lock，也就是记录锁 + 间隙锁。死锁排查主要看 `SHOW ENGINE INNODB STATUS`、慢日志、事务持锁时间、SQL 顺序和索引是否命中。优化方向是事务短小、固定加锁顺序、条件命中索引、避免大范围锁和长事务。

## 高频问题

- MVCC 是什么？
- 快照读和当前读区别？
- Read Committed 和 Repeatable Read 下 Read View 有什么区别？
- 行锁、间隙锁、next-key lock 是什么？
- 什么情况下锁很多行甚至像表锁？
- 死锁如何排查？
- MySQL Repeatable Read 如何避免幻读？

## MVCC 是什么

MVCC 是 Multi-Version Concurrency Control，多版本并发控制。

它的目标是：

- 读写不互相阻塞。
- 普通读不加锁。
- 通过版本链读取一致性视图。

InnoDB 每行记录有隐藏字段，可以理解为：

- 创建或最后修改该行的事务 ID。
- 指向 undo log 的回滚指针。

当数据被更新时，旧版本会保存在 undo log 中，形成版本链。

## Read View

Read View 用来判断某个版本对当前事务是否可见。

大致包含：

- 当前活跃事务列表。
- 最小活跃事务 ID。
- 下一个将分配的事务 ID。
- 当前事务 ID。

不同隔离级别：

### Read Committed

每条 SQL 都生成新的 Read View。

所以同一事务中两次 SELECT，可能看到不同结果。

### Repeatable Read

事务中第一次快照读生成 Read View，后续复用。

所以同一事务中多次快照读结果一致。

## 快照读和当前读

### 快照读

普通 SELECT：

```sql
SELECT * FROM orders WHERE order_id = ?;
```

特点：

- 读取历史版本。
- 通常不加锁。
- 依赖 MVCC。

### 当前读

读取最新数据并加锁：

```sql
SELECT * FROM orders WHERE order_id = ? FOR UPDATE;
```

```sql
UPDATE orders SET status = ? WHERE order_id = ?;
```

```sql
DELETE FROM orders WHERE order_id = ?;
```

当前读必须看到最新已提交数据，并对相关记录加锁。

## InnoDB 常见锁

### 行锁 Record Lock

锁住索引记录。

```sql
UPDATE orders SET status='PAID' WHERE order_id = 1001;
```

如果 `order_id` 是索引，只锁相关记录。

### 间隙锁 Gap Lock

锁住索引记录之间的间隙，防止其他事务插入。

例如锁住：

```text
(10, 20)
```

防止插入 11 到 19。

### Next-Key Lock

记录锁 + 间隙锁。

锁住：

```text
(前一个索引值, 当前索引值]
```

Repeatable Read 下范围查询可能使用 next-key lock 来避免幻读。

## 幻读是什么

事务内两次按同一条件查询，第二次多出或少了符合条件的行。

例如：

```sql
SELECT * FROM orders WHERE amount > 100;
```

另一个事务插入一条 amount=200 的订单并提交，第一次和第二次查询结果行集合不同。

InnoDB 在 Repeatable Read 下：

- 快照读通过 MVCC Read View 避免幻读现象。
- 当前读通过 next-key lock 防止其他事务插入范围内新记录。

## 为什么有时行锁像表锁

常见原因：

### 1. 条件没有命中索引

```sql
UPDATE orders SET status='X' WHERE external_no = 'abc';
```

如果 `external_no` 没索引，可能扫描大量记录并加锁，影响很大。

### 2. 范围过大

```sql
UPDATE orders SET status='X'
WHERE created_at < '2026-01-01';
```

范围太大，会锁很多记录和间隙。

### 3. 索引选择性差

例如只按 `status` 更新，status 只有几个值，可能锁大量行。

### 4. 长事务

锁持有时间太长，其他事务等待。

## 死锁例子

```text
T1: lock order 1
T2: lock order 2
T1: wait order 2
T2: wait order 1
```

形成循环等待。

## 死锁如何排查

### 1. 查看 InnoDB 状态

```sql
SHOW ENGINE INNODB STATUS;
```

看最近一次 deadlock：

- 哪两个事务。
- 执行什么 SQL。
- 等待什么锁。
- 持有什么锁。

### 2. 看慢日志

找长事务、慢更新、大范围扫描。

### 3. 看应用日志

结合 trace_id、order_id、SQL、耗时。

### 4. 看事务加锁顺序

多个资源必须按固定顺序加锁。

例如统一按 order_id 从小到大更新。

## 死锁优化手段

- 保持事务短小。
- 不在事务中调用外部 RPC。
- 固定加锁顺序。
- 查询条件命中索引。
- 避免大范围更新。
- 分批处理大任务。
- 使用合理隔离级别。
- 对死锁错误做有限重试。

## 订单状态更新示例

推荐条件更新：

```sql
UPDATE orders
SET status = 'PAID'
WHERE order_id = ?
  AND status = 'WAIT_PAY';
```

优点：

- 原子状态流转。
- 防重复回调。
- 减少显式锁。

库存扣减：

```sql
UPDATE sku_stock
SET available_stock = available_stock - ?
WHERE sku_id = ?
  AND available_stock >= ?;
```

## 面试回答模板

InnoDB 的 MVCC 通过 undo log 版本链和 Read View 实现一致性读。普通 SELECT 是快照读，一般不加锁；UPDATE、DELETE、SELECT FOR UPDATE 是当前读，会读取最新数据并加锁。

锁方面，InnoDB 有行锁、间隙锁和 next-key lock。Repeatable Read 下，范围当前读可能加 next-key lock，防止其他事务在范围内插入新记录，从而避免幻读。很多锁问题其实是 SQL 没走索引、大范围扫描或长事务导致的。

死锁排查我会看 `SHOW ENGINE INNODB STATUS` 里的 latest deadlock，结合慢日志和应用 trace。优化上保持事务短小，固定加锁顺序，确保条件命中索引，避免大范围更新，必要时对死锁做有限重试。

