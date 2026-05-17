# 跨境物流系统设计实战题

## 题目 1：如果一个订单系统秒杀量很大，如何设计保证库存不超卖？

### 30 秒回答

防止库存超卖的核心是让库存扣减具备原子性，并控制入口流量。秒杀场景下不能让所有请求直接打数据库。通常会用 Redis 做前置库存扣减和限流，MQ 异步削峰，数据库最终落库，并通过幂等、订单状态机、库存流水和对账补偿保证最终一致。

更严格的方案是库存服务提供预占库存能力，扣减时用数据库条件更新或 Redis Lua 保证原子性，例如只有 `stock > 0` 时才能扣减。每个用户和订单请求要有幂等键，防止重复下单和重复扣减。

### 核心目标

要解决四个问题：

1. 不超卖。
2. 不重复下单。
3. 高并发下系统不被打爆。
4. 失败后库存能补偿回来。

## 典型架构

```text
客户端
  -> 网关限流
  -> 秒杀服务
  -> Redis 原子扣减
  -> MQ
  -> 订单服务创建订单
  -> 库存服务确认扣减
  -> 支付服务
```

## 关键设计点

### 1. 入口限流

在网关或服务入口限制流量：

- 按用户限流。
- 按 IP 限流。
- 按商品限流。
- 验证码或排队。
- 黑名单和风控。

目的不是让所有请求都进入系统，而是让系统只处理可承载的流量。

### 2. 防重复下单

同一用户对同一商品通常只能秒杀一次。

可以用 Redis：

```text
SETNX seckill:user:{user_id}:sku:{sku_id} request_id
```

设置成功才允许继续。失败说明用户已经提交过。

也要在数据库建立唯一约束：

```text
unique(user_id, activity_id, sku_id)
```

防止 Redis 异常或并发边界导致重复订单。

### 3. Redis 原子扣减库存

使用 Lua 脚本保证判断和扣减原子执行。

逻辑：

```text
if stock <= 0:
    return insufficient
stock = stock - 1
return success
```

伪代码：

```lua
local stock = tonumber(redis.call("GET", KEYS[1]))
if stock == nil or stock <= 0 then
    return 0
end
redis.call("DECR", KEYS[1])
return 1
```

这样不会出现两个请求同时看到库存为 1 然后都扣成功的问题。

### 4. MQ 异步创建订单

Redis 扣减成功后，不一定同步创建完整订单，可以把请求写入 MQ。

优点：

- 削峰。
- 快速响应。
- 保护数据库。
- 后续慢慢消费创建订单。

但要注意：MQ 消息可能重复，所以订单创建必须幂等。

### 5. 数据库最终扣减也要防超卖

即使 Redis 做了前置扣减，数据库落库也要有防线。

条件更新：

```sql
UPDATE sku_stock
SET stock = stock - 1
WHERE sku_id = ? AND stock > 0;
```

判断 affected rows：

- 1 表示扣减成功。
- 0 表示库存不足。

### 6. 库存流水

每次库存变化记录流水：

```text
request_id
order_id
sku_id
change_amount
operation_type
status
created_at
```

作用：

- 幂等。
- 对账。
- 问题追踪。
- 补偿。

### 7. 支付超时释放库存

如果订单创建后用户未支付，需要释放库存。

流程：

```text
创建订单 -> 状态 WAIT_PAY
延迟消息 -> 检查是否支付
未支付 -> 取消订单 -> 释放库存
```

释放库存也要幂等，防止重复释放。

### 8. 对账补偿

需要定时对账：

- Redis 扣减成功但订单没创建。
- 订单创建成功但数据库库存没扣。
- 支付失败但库存没释放。
- MQ 消息失败或积压。

发现异常后补偿或人工处理。

## 简化代码：Redis Lua 原子扣库存

Go 伪代码：

```go
const deductScript = `
local stock = tonumber(redis.call("GET", KEYS[1]))
if stock == nil or stock <= 0 then
    return 0
end
redis.call("DECR", KEYS[1])
return 1
`

func TryDeduct(ctx context.Context, rdb *redis.Client, skuID string) (bool, error) {
    key := "stock:" + skuID

    result, err := rdb.Eval(ctx, deductScript, []string{key}).Int()
    if err != nil {
        return false, err
    }
    return result == 1, nil
}
```

## 面试回答模板

秒杀库存防超卖，我会设计多层防线。入口先限流和防重复提交，避免无效请求进入核心链路。库存扣减必须是原子操作，可以用 Redis Lua 做前置扣减，也可以用数据库条件更新 `stock > 0` 保证不会扣成负数。

扣减成功后通过 MQ 异步创建订单，削峰保护数据库。订单创建和库存确认都要幂等，使用 `user_id + sku_id + activity_id` 唯一约束和 request_id 防重。支付超时后通过延迟消息取消订单并释放库存。

最后还要有库存流水和对账补偿，因为 Redis、MQ、数据库之间可能出现部分失败。真正可靠的设计不是只靠一个扣减操作，而是原子扣减、幂等、防重、异步削峰、超时释放和对账补偿一起保证。

## 常见追问

### 1. Redis 扣成功，但 MQ 发送失败怎么办？

可以用本地消息表或事务消息。扣减成功后记录待发送消息，由后台任务可靠投递。或者失败时回补 Redis 库存，但要防止并发和重复补偿。

### 2. Redis 库存和数据库库存不一致怎么办？

Redis 作为前置流量控制，数据库是最终事实来源。需要定时对账，活动开始前预热库存，活动中记录流水，活动后根据订单和库存流水校正。

### 3. 只用数据库能不能防超卖？

可以，用条件更新和事务能防超卖。但秒杀高并发下所有请求直接打数据库，数据库压力很大，所以通常会在前面加限流、缓存和 MQ 削峰。

## 题目 2：如何保证跨境物流的订单状态最终一致？

### 30 秒回答

跨境物流订单状态由多个系统共同推进，比如支付、仓储、运输、清关、末端派送、签收。因为外部承运商和内部服务都是异步的，不能要求强一致，通常采用事件驱动的最终一致性。

核心做法是订单服务维护订单状态机，各系统通过 MQ 发布状态事件，订单服务按状态机消费事件并幂等更新。为了解决消息重复、乱序、丢失，需要事件 ID、版本号、状态流转规则、重试、死信队列和定时对账。

## 业务状态示例

```text
CREATED
PAID
STOCK_RESERVED
WAREHOUSE_PROCESSING
SHIPPED
CUSTOMS_CLEARING
CUSTOMS_RELEASED
OUT_FOR_DELIVERY
DELIVERED
CANCELED
REFUNDED
```

状态不是随意覆盖，而是由状态机控制。

## 事件来源

| 来源系统 | 事件 |
| --- | --- |
| 支付服务 | PaymentSucceeded、PaymentFailed |
| 库存服务 | StockReserved、StockReleased |
| 仓储服务 | Picked、Packed、Outbound |
| 运输服务 | ShipmentCreated、FlightDeparted |
| 清关系统 | CustomsStarted、CustomsReleased、CustomsRejected |
| 末端配送 | OutForDelivery、Delivered |
| 售后服务 | RefundStarted、Refunded |

## 典型架构

```text
支付/库存/仓储/运输/清关/配送
  -> 状态事件
  -> MQ
  -> 订单状态消费者
  -> 订单状态机
  -> 订单库
  -> 状态变更事件
```

订单服务是订单主状态的 owner，其他系统不直接改订单主表。

## 关键设计点

### 1. 订单状态机

状态流转必须受控。

示例：

```text
PAID -> STOCK_RESERVED       允许
SHIPPED -> DELIVERED         允许
DELIVERED -> CUSTOMS_CLEARING 不允许
CANCELED -> SHIPPED          不允许
```

状态机可以避免乱序事件导致状态回退。

### 2. 事件幂等

每个事件有唯一 ID：

```text
event_id = carrier_a:tracking_no:delivered:timestamp
```

消费前先查是否处理过。

```sql
INSERT INTO processed_event(event_id, order_id, created_at)
VALUES (?, ?, ?);
```

如果唯一键冲突，说明重复消息，直接返回成功。

### 3. 处理乱序事件

跨境物流事件可能乱序。

例如：

```text
Delivered 先到
OutForDelivery 后到
```

处理方式：

- 每个事件带 event_time。
- 每个状态有优先级或版本。
- 状态机禁止回退。
- 轨迹明细可以按事件时间展示。
- 主订单状态只向前推进。

### 4. 可靠消息

状态事件不能只写内存。

生产端：

- 本地事务写业务数据和 outbox 表。
- 后台投递 MQ。
- 投递成功标记 sent。

消费端：

- 幂等处理。
- 成功后 ack。
- 失败重试。
- 多次失败进入死信队列。

### 5. 定时对账

定时任务检查异常订单：

- 已支付但长时间未预占库存。
- 已出库但未创建运单。
- 承运商显示签收但订单未签收。
- 清关拒绝但订单还在运输中。

对账可以主动查询承运商 API 或内部服务状态，发现不一致后重新发布事件或执行补偿。

### 6. 人工处理入口

跨境物流有大量外部异常，例如清关失败、地址错误、承运商丢件。系统不能只靠自动流转，必须有异常状态和人工处理入口。

## 面试回答模板

跨境物流订单状态涉及支付、库存、仓储、运输、清关、末端配送多个系统，不适合用一个强事务保证所有系统实时一致。我会采用事件驱动的最终一致性。

订单服务作为订单主状态的 owner，维护订单状态机。各系统完成自己的本地事务后，通过 MQ 发布事件，比如 PaymentSucceeded、ShipmentCreated、CustomsReleased、Delivered。订单服务消费事件时先做幂等校验，再根据状态机推进订单状态。

为了保证最终一致，要处理消息重复、乱序和丢失。重复靠 event_id 和消费记录去重；乱序靠状态机、事件时间和状态版本防止状态回退；丢失靠本地消息表、重试、死信队列和定时对账补偿。对于外部承运商异常，还要有人工处理和补偿流程。

## 常见追问

### 1. 如果 Delivered 事件先于 OutForDelivery 到达怎么办？

轨迹明细可以按事件时间保存完整记录，但订单主状态不能回退。Delivered 是更终态的状态，后来的 OutForDelivery 不能把主状态改回派送中。

### 2. 订单服务为什么要做状态 owner？

如果多个服务都能直接修改订单状态，就很容易出现覆盖、乱序和责任不清。订单服务统一维护状态机，其他服务通过事件表达事实。

### 3. 如何发现消息丢失？

通过本地消息表投递状态、MQ 消费监控、订单状态超时扫描、承运商 API 对账和死信队列告警发现。
