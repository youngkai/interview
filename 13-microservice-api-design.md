# 如何设计微服务接口

## 30 秒回答

微服务接口设计要关注清晰的业务语义、稳定的契约、幂等性、超时、错误码、版本兼容、安全认证和可观测性。跨境物流这种多系统协作场景中，接口不能只考虑正常流程，还要考虑重复请求、部分失败、网络超时、状态乱序、消息重放等问题。

好的接口应该让调用方知道：这个接口做什么、输入输出是什么、失败如何处理、能否重试、是否幂等、状态如何演进。

## 接口设计核心原则

### 1. 业务语义清晰

接口应该表达业务动作，而不是暴露内部表结构。

不推荐：

```text
POST /order/updateStatus
```

更清晰：

```text
POST /orders/{order_id}/cancel
POST /orders/{order_id}/pay
POST /orders/{order_id}/fulfill
```

对于库存：

```text
POST /inventory/reservations
POST /inventory/reservations/{reservation_id}/confirm
POST /inventory/reservations/{reservation_id}/release
```

这样可以表达库存预占、确认扣减、释放三个不同业务动作。

### 2. 明确数据归属

订单服务不要设计接口让外部随意改订单所有字段。

例如订单状态应该由订单服务内部状态机控制，外部系统只能触发业务动作或发送事件：

```text
运输服务 -> 发送 ShipmentCreated 事件
轨迹服务 -> 发送 PackageDelivered 事件
订单服务 -> 根据事件推进订单状态
```

### 3. 幂等性

分布式系统中调用可能超时，但服务端实际已经成功。调用方重试时，接口必须能处理重复请求。

常见做法：

- 使用 `Idempotency-Key`。
- 使用业务唯一键，例如 `order_id + operation_type`。
- 数据库唯一索引防重。
- 状态机判断重复操作。

示例：

```http
POST /inventory/reservations
Idempotency-Key: order-1001-reserve
```

服务端保存幂等记录：

```text
key: order-1001-reserve
result: reservation_id=xxx
```

重复请求直接返回第一次处理结果。

### 4. 超时和重试语义

接口文档要说明：

- 调用方超时时能否重试。
- 哪些错误可以重试。
- 哪些错误不能重试。
- 服务端是否保证幂等。

通常：

- 5xx、网络超时可以重试，但必须幂等。
- 4xx 通常表示请求错误，不应盲目重试。
- 409 表示状态冲突，需要业务判断。

### 5. 错误码稳定

错误响应不要只返回字符串。

推荐：

```json
{
  "code": "INSUFFICIENT_STOCK",
  "message": "insufficient stock",
  "request_id": "req-xxx"
}
```

调用方应该依赖稳定的 `code`，而不是解析 `message`。

### 6. 版本兼容

接口变更要尽量向后兼容。

建议：

- 新增字段，不删除字段。
- 字段语义不要随意改变。
- enum 增加新值时通知调用方。
- 必要时使用 `/v1/`、`/v2/` 或 protobuf 版本管理。

### 7. 分页和限制

列表接口必须分页，避免一次返回过多数据。

```http
GET /orders?cursor=xxx&limit=100
```

注意：

- `limit` 要有最大值。
- 大数据导出走异步任务。
- 高并发列表查询要避免深分页。

### 8. 安全和权限

接口要考虑：

- 服务间认证。
- 用户鉴权。
- 租户隔离。
- 数据权限。
- 防止越权访问。

跨境物流常见多租户场景，比如不同商家只能看自己的订单和库存。

### 9. 可观测性

请求头中传递：

```text
X-Request-ID
X-Trace-ID
X-User-ID
X-Tenant-ID
```

日志里记录：

- request id。
- trace id。
- order id。
- tenant id。
- error code。
- latency。

## 接口状态机设计

订单状态不能随意修改，要通过状态机控制。

示例：

```text
CREATED -> PAID -> RESERVED -> FULFILLING -> SHIPPED -> DELIVERED
CREATED -> CANCELED
PAID -> REFUNDING -> REFUNDED
```

非法状态变更要拒绝：

```text
DELIVERED -> CANCELED  不允许
```

面试中可以强调：状态机能防止并发和乱序事件导致状态回退。

## 面试回答模板

微服务接口设计首先要明确业务语义和数据归属。比如订单状态由订单服务维护，库存数量由库存服务维护，其他服务不能直接改表，只能通过接口或事件触发业务动作。

接口要支持幂等，因为网络超时和重试很常见。像库存预占、订单支付、创建运单这类接口，我会要求调用方传幂等键，服务端用唯一索引或幂等表保证重复请求返回同一个结果。

另外接口要有稳定错误码、超时和重试约定、版本兼容、分页限制、认证鉴权和 trace id。对于订单状态这种核心流程，我会用状态机约束状态流转，避免重复消息或乱序消息把状态改错。

## 常见追问

### 1. 幂等键放在哪里？

HTTP 可以放在 `Idempotency-Key` 请求头，也可以放在请求体里。RPC 可以放在 metadata 或 request 字段中。关键是服务端必须持久化或用唯一约束保证幂等。

### 2. 接口超时后调用方应该怎么做？

如果接口是幂等的，可以安全重试。如果不是幂等的，调用方应该先查询操作结果，不能盲目重试。

### 3. 如何处理接口版本升级？

优先做向后兼容变更，例如新增字段。破坏性变更需要新版本接口，并让老版本保留一段迁移期。

