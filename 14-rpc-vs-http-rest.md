# RPC vs HTTP REST

## 30 秒回答

RPC 更偏动作调用，像调用本地函数一样调用远程服务，常见实现有 gRPC、Thrift。HTTP REST 更偏资源建模，通过 HTTP 方法表达对资源的操作。

内部微服务之间，如果追求性能、强类型契约、代码生成和流式通信，通常会选 gRPC。对外开放 API、前后端接口、第三方集成，更常用 HTTP REST，因为通用性好、调试简单、生态成熟。

## HTTP REST 的特点

REST 常用 HTTP 方法表达资源操作：

```text
GET    /orders/{id}
POST   /orders
PATCH  /orders/{id}
DELETE /orders/{id}
```

优点：

- 通用性强。
- 浏览器、网关、代理支持好。
- 调试简单，curl/Postman 即可。
- 对外部开发者友好。
- 易于接入 API Gateway、WAF、CDN。

缺点：

- JSON 文本编码体积较大。
- 强类型约束较弱。
- 接口契约容易靠文档维护。
- 高性能内部调用不如二进制 RPC 高效。

## RPC 的特点

以 gRPC 为例：

```proto
service OrderService {
  rpc GetOrder(GetOrderRequest) returns (GetOrderResponse);
  rpc CreateOrder(CreateOrderRequest) returns (CreateOrderResponse);
}
```

优点：

- 强类型接口契约。
- protobuf 编码体积小。
- 性能通常较好。
- 支持代码生成。
- 支持双向流、服务端流、客户端流。
- 适合内部服务间调用。

缺点：

- 对浏览器和第三方集成不如 REST 直接。
- 调试门槛略高。
- 需要维护 proto 契约。
- 版本兼容要遵守 protobuf 规则。

## 跨境物流中的选择

### 适合 HTTP REST 的场景

- 商家后台调用订单 API。
- 前端查询订单和物流轨迹。
- 第三方平台接入。
- Webhook 回调。
- 开放平台 API。

示例：

```http
GET /v1/orders/{order_id}
GET /v1/shipments/{tracking_no}/events
POST /v1/webhooks/logistics-events
```

### 适合 RPC 的场景

- 订单服务调用库存服务。
- 订单服务调用计费服务。
- 运输服务调用轨迹服务。
- 内部服务高频调用。

示例：

```text
InventoryService.ReserveStock
PricingService.CalculateShippingFee
ShipmentService.CreateShipment
```

## 对比表

| 对比项 | HTTP REST | RPC/gRPC |
| --- | --- | --- |
| 建模方式 | 资源 | 方法/服务 |
| 协议 | HTTP + JSON 常见 | HTTP/2 + protobuf 常见 |
| 可读性 | 强 | 较弱 |
| 性能 | 一般足够 | 通常更高 |
| 契约 | OpenAPI/文档 | proto 强约束 |
| 调试 | 简单 | 需要工具 |
| 浏览器友好 | 是 | 原生不如 REST |
| 内部调用 | 可用 | 很适合 |
| 对外开放 | 很适合 | 需要额外适配 |

## 面试回答模板

RPC 和 REST 的区别主要在建模和使用场景。REST 面向资源，适合对外 API、前端接口和第三方集成，通用性和可调试性好。RPC 面向服务方法，适合内部微服务之间高频调用，强类型、性能好，也方便通过 proto 生成代码。

在跨境物流系统里，我倾向于外部接口用 HTTP REST，比如商家查询订单、物流轨迹、Webhook 回调；内部订单、库存、计费、运输之间的高频调用可以用 gRPC。无论选哪种，都要处理超时、重试、幂等、错误码和链路追踪。

## 常见追问

### 1. gRPC 一定比 REST 好吗？

不一定。gRPC 在内部高频调用上有优势，但对外开放、浏览器调用、简单调试方面 REST 更友好。

### 2. REST 能不能做内部微服务调用？

可以。很多系统内部也用 REST。是否选择 gRPC 要看性能要求、团队工具链、契约管理和跨语言需求。

### 3. RPC 调用失败怎么处理？

要设置超时、重试、熔断和幂等。不能无限重试，否则会放大故障。

