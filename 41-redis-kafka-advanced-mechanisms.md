# Redis 与 Kafka 深入机制

## 30 秒回答

Redis 高级面试重点通常围绕单线程为什么快、持久化、主从复制、哨兵、Cluster、hot key、big key、内存淘汰、pipeline 和 Lua。Kafka 高级面试重点是 partition、consumer group、offset、rebalance、ISR、acks、replication factor、顺序性、高吞吐和消息积压处理。

跨境物流中，Redis 常用于热点轨迹缓存、限流、分布式锁和延迟队列；Kafka 常用于轨迹事件流、订单状态事件、CDC、搜索索引同步和数仓同步。核心是根据数据特征选工具，并理解故障和一致性边界。

## Redis 为什么快

常见原因：

- 内存操作。
- 单线程事件循环避免大量锁竞争。
- I/O 多路复用。
- 高效数据结构。
- 命令处理简单。

注意：Redis 不是所有工作都单线程，例如持久化、异步删除、网络 I/O 在新版本中可能有多线程能力。但命令执行主路径通常可以按单线程模型理解。

## Redis 持久化

### RDB

定期生成快照。

优点：

- 文件紧凑。
- 恢复快。
- 适合备份。

缺点：

- 两次快照之间的数据可能丢失。

### AOF

追加写命令日志。

优点：

- 数据丢失更少。
- 可配置每秒 fsync。

缺点：

- 文件更大。
- 恢复可能慢。
- 需要 rewrite。

### 如何选择

缓存场景可以接受丢失，RDB 或关闭持久化都可能。分布式锁、限流、会话等要根据业务容忍度评估。核心交易事实不要只存在 Redis。

## Redis 主从、哨兵、Cluster

### 主从复制

主节点写，从节点复制。

用途：

- 读扩展。
- 高可用基础。
- 数据备份。

风险：

- 异步复制可能丢数据。
- 主从延迟。

### Sentinel

哨兵负责：

- 监控 master。
- 故障发现。
- 自动故障转移。
- 通知客户端新 master。

### Cluster

Redis Cluster 使用 hash slot 分片，总共 16384 个槽。

key 通过 hash 映射到 slot，不同 slot 分布在不同 master。

注意：

- 多 key 操作要求 key 在同一 slot，或者使用 hash tag。
- hot key 仍然可能打爆单个节点。
- big key 会影响单节点性能。

## hot key

热点 key 被大量访问，例如爆款商品库存、热门包裹轨迹。

发现方式：

- Redis hotkeys。
- 客户端统计。
- 代理层统计。
- 慢日志和监控。

解决：

- 本地缓存。
- 多副本缓存。
- key 拆分。
- 请求合并 singleflight。
- 限流。

## big key

value 很大或集合元素很多。

风险：

- 网络传输慢。
- 阻塞 Redis。
- 删除慢。
- 迁移慢。

解决：

- 拆分 key。
- 分页读取。
- 控制集合大小。
- 使用 UNLINK 异步删除。
- 避免一个 hash/list/zset 无限增长。

## Redis 内存淘汰策略

常见策略：

- noeviction：不淘汰，写入报错。
- allkeys-lru：所有 key 中淘汰最近最少使用。
- volatile-lru：有过期时间的 key 中做 LRU。
- allkeys-lfu：所有 key 中按 LFU 淘汰。
- volatile-ttl：优先淘汰快过期的 key。

缓存系统常用 allkeys-lru 或 allkeys-lfu，但要结合业务。

## Pipeline 和 Lua

### Pipeline

批量发送命令，减少 RTT。

适合：

- 批量读取轨迹缓存。
- 批量写入状态。

### Lua

在 Redis 内原子执行多步逻辑。

适合：

- 原子库存扣减。
- 分布式锁释放。
- 限流计数。

注意：Lua 不要执行太久，否则阻塞 Redis。

## Kafka Partition

Topic 被拆成多个 partition。

作用：

- 并行写入。
- 并行消费。
- 水平扩展。
- 单 partition 内有序。

物流轨迹：

```text
topic: tracking_events
key: tracking_no
```

同一个 tracking_no 进入同一 partition，保证包裹维度有序。

## Consumer Group

一个 consumer group 内，partition 分配给不同消费者。

规则：

- 同一个 partition 同一时刻只能被同 group 的一个消费者消费。
- 不同 consumer group 可以独立消费同一 topic。

例子：

```text
tracking_events
  -> tracking-service group
  -> notification-service group
  -> data-warehouse group
```

## Offset 提交

offset 表示消费位置。

提交策略：

### 自动提交

简单，但可能处理失败后 offset 已提交，造成消息丢失语义。

### 手动提交

业务处理成功后再提交 offset。

更可靠，但要处理重复消费。

面试表达：

> 我一般选择处理成功后再提交 offset，保证至少一次处理。重复消费通过业务幂等解决。

## Rebalance

当 consumer group 成员变化、partition 变化或心跳异常时，会触发 rebalance。

问题：

- 消费暂停。
- partition 重新分配。
- 可能重复消费。
- 频繁 rebalance 会影响吞吐。

优化：

- 合理设置 session timeout 和 poll interval。
- 避免单条消息处理过久。
- 控制消费者稳定性。
- 使用批量处理但不要阻塞 poll 太久。

## ISR、acks、Replication Factor

### Replication Factor

每个 partition 有多个副本。

### Leader / Follower

读写通常走 leader，follower 复制 leader 数据。

### ISR

In-Sync Replicas，同步进度跟得上的副本集合。

### acks

- `acks=0`：不等 broker 确认，性能高但可能丢。
- `acks=1`：leader 写入成功即返回。
- `acks=all`：ISR 副本满足要求后返回，可靠性更高。

关键配置：

```text
acks=all
min.insync.replicas >= 2
replication.factor >= 3
```

## Kafka 为什么高吞吐

原因：

- 顺序写磁盘。
- page cache。
- 批量发送。
- 零拷贝。
- 分区并行。
- 消费者顺序读取。

## Kafka 积压处理

看：

- consumer lag。
- 消费耗时。
- 下游耗时。
- partition 数。
- rebalance 频率。

处理：

- 扩容消费者，但不能超过 partition 数带来的并行度。
- 增加 partition。
- 批量处理。
- 优化下游写库。
- 毒性消息进死信。
- 非核心消费降级。

## 面试回答模板

Redis 我会重点关注 hot key、big key、持久化、主从和 Cluster。Redis 快主要因为内存操作、单线程命令执行避免锁竞争、I/O 多路复用和高效数据结构。缓存场景要处理 big key、hot key、内存淘汰和缓存一致性，核心交易数据不能只放 Redis。

Kafka 的核心是 topic、partition 和 consumer group。partition 提供并行度和单分区有序，consumer group 让多个消费者分摊 partition。offset 成功处理后再提交，重复消费通过幂等解决。可靠性上，生产端配置 `acks=all`，broker 用多副本和 ISR，消费端手动提交 offset。

跨境物流里，Redis 适合热点轨迹、限流、短期状态和分布式锁；Kafka 适合轨迹事件流、订单状态流、CDC 和搜索索引同步。遇到积压时，看 lag、消费耗时、下游瓶颈和 rebalance，按瓶颈扩容或降级。
