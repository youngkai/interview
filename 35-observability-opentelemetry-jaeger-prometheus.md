# 系统监控与可观测性：OpenTelemetry、Jaeger、Prometheus

## 30 秒回答

跨境物流链路跨越订单、库存、仓储、运输、清关、支付、轨迹、通知等多个服务，还会调用海外承运商和海关接口。要做好可观测性，核心是把 Trace、Metrics、Logs 三类信号打通，并用统一的 trace_id 串起一次请求的完整路径。

Go 服务里可以用 OpenTelemetry 做统一埋点，通过 W3C Trace Context 在 HTTP/gRPC/MQ 中传播 trace_id 和 span_id。Trace 数据可以通过 OTLP 上报到 OpenTelemetry Collector，再导出到 Jaeger 查看调用链。Metrics 可以用 Prometheus client 或 OTel metrics 暴露请求量、延迟、错误率、队列积压、下游调用耗时等指标，再由 Prometheus 抓取和告警。

面试回答重点不是“用了某个工具”，而是能说明：请求慢时如何定位慢在哪个服务，消息积压时如何看到 lag，第三方接口异常时如何熔断告警，订单状态不一致时如何通过 trace_id、order_id、tracking_no 快速串起日志和链路。

## 可观测性的三大信号

### 1. Trace

Trace 用来描述一次请求跨多个服务的调用链。

例如：

```text
POST /orders
  -> order-service.CreateOrder
  -> inventory-service.ReserveStock
  -> pricing-service.CalculateFee
  -> order-db.Insert
  -> kafka.Publish(OrderCreated)
```

Trace 适合回答：

- 请求经过了哪些服务？
- 每个服务耗时多少？
- 慢在哪个 span？
- 哪个下游报错？
- 是否发生重试？

### 2. Metrics

Metrics 是聚合指标，适合看系统整体健康状况。

例如：

- QPS。
- 错误率。
- P95/P99 延迟。
- CPU、内存、GC。
- Goroutine 数量。
- DB 连接池使用率。
- Redis 命中率。
- MQ lag。
- 第三方接口成功率和耗时。

Metrics 适合告警和看趋势。

### 3. Logs

Logs 是离散事件记录，适合查具体细节。

日志里必须带关键字段：

- trace_id。
- span_id。
- order_id。
- tracking_no。
- tenant_id。
- carrier。
- event_id。
- error_code。

这样才能从 Jaeger 的 trace 跳到日志系统，或者从日志反查 trace。

## 跨境物流为什么特别需要全链路追踪

跨境物流链路有几个特点：

1. 服务多：订单、库存、仓储、运输、轨迹、支付、清关、通知。
2. 外部依赖多：DHL、UPS、FedEx、海关、海外仓、支付机构。
3. 网络跨区域：海内外网络延迟和丢包更不可控。
4. 状态异步：MQ、回调、补偿任务很多。
5. 问题定位难：一个订单异常可能横跨多个系统。

没有 trace 时，经常只能靠人工查多套日志。加了 trace 后，可以从一个 trace_id 看到请求从入口到下游的完整路径。

## 推荐架构

```text
Go Service
  -> OpenTelemetry SDK / instrumentation
  -> OTLP
  -> OpenTelemetry Collector
     -> Jaeger / Tempo / tracing backend
     -> Prometheus / metrics backend
     -> Log backend

Prometheus
  -> scrape /metrics
  -> Alertmanager
  -> Grafana Dashboard
```

常见组合：

- OpenTelemetry：统一采集和传播 trace、metrics、logs。
- OpenTelemetry Collector：接收、处理、导出遥测数据。
- Jaeger：查看分布式追踪。
- Prometheus：采集指标和告警。
- Grafana：展示 dashboard。
- Loki/ELK：日志检索。

## Trace 设计

### Trace、Span、Context

一次完整请求是一个 trace。

trace 里每个阶段是一个 span：

```text
Trace: create order
  Span: HTTP POST /orders
  Span: validate request
  Span: reserve stock
  Span: insert order
  Span: publish OrderCreated
```

每个 span 应该记录：

- service name。
- operation name。
- start/end time。
- status。
- attributes。
- error。

常见 attributes：

```text
order.id
tenant.id
tracking.no
carrier
mq.topic
db.statement
http.status_code
rpc.system
```

注意：不要把敏感信息、完整地址、身份证、支付卡号写进 span attribute。

## Go 服务接入 OpenTelemetry

### 初始化 TracerProvider

示例代码偏骨架，实际项目会把 endpoint、service name、采样率放到配置里。

```go
package observability

import (
    "context"
    "time"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
    "go.opentelemetry.io/otel/propagation"
    "go.opentelemetry.io/otel/sdk/resource"
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
    semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

func InitTracer(ctx context.Context, serviceName, endpoint string) (func(context.Context) error, error) {
    exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(endpoint), otlptracegrpc.WithInsecure())
    if err != nil {
        return nil, err
    }

    res, err := resource.New(ctx,
        resource.WithAttributes(
            semconv.ServiceName(serviceName),
        ),
    )
    if err != nil {
        return nil, err
    }

    tp := sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(2*time.Second)),
        sdktrace.WithResource(res),
        sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(0.1))),
    )

    otel.SetTracerProvider(tp)
    otel.SetTextMapPropagator(
        propagation.NewCompositeTextMapPropagator(
            propagation.TraceContext{},
            propagation.Baggage{},
        ),
    )

    return tp.Shutdown, nil
}
```

关键点：

- 使用 OTLP exporter 上报到 Collector。
- 设置 service.name，方便 Jaeger 按服务过滤。
- 使用 batch exporter，减少上报开销。
- 设置采样率，避免高 QPS 下 trace 数据爆炸。
- 设置 W3C Trace Context propagator。

## HTTP 链路传播

HTTP 服务端和客户端都要传播 context。

常见做法是用 OpenTelemetry 提供的 HTTP instrumentation。

入口收到请求后：

```text
解析 traceparent header
创建 server span
把 span 放进 request context
下游调用继续传播
```

下游 HTTP 调用时：

```text
从 context 取 trace
注入 traceparent header
创建 client span
```

如果某个服务没有透传 context，链路就会断。

## gRPC 链路传播

gRPC 通常通过 metadata 传播 trace context。

Go 里可以用 gRPC 的 OTel interceptor/stats handler。面试中重点表达：

- 服务端拦截器从 metadata 提取 trace context。
- 客户端拦截器把 trace context 注入 metadata。
- 每个 RPC 创建 client/server span。
- deadline、error code、method name 记录到 span。

## MQ 链路传播

跨境物流大量使用 Kafka/RocketMQ/RabbitMQ，trace 不能在 MQ 处断掉。

生产消息时：

```text
从当前 ctx 提取 trace context
写入消息 header
```

消费消息时：

```text
从消息 header 提取 trace context
创建 consumer span
处理业务
```

消息 header 示例：

```text
traceparent: 00-<trace-id>-<span-id>-01
```

MQ span 应该记录：

- topic。
- partition。
- offset。
- consumer group。
- event_id。
- order_id。

这样可以看到：

```text
HTTP 下单 -> publish OrderCreated -> consumer 处理 -> 调用仓储服务
```

## Jaeger 怎么用

Jaeger 主要用于查询和分析 trace。

常见排查方式：

### 1. 按 trace_id 查询

用户反馈某个订单慢，日志里有 trace_id，直接在 Jaeger 查整条链路。

### 2. 按 service 查询

查看 `order-service` 最近慢请求，按耗时排序。

### 3. 看 span 时间线

判断耗时集中在哪：

- DB 查询慢。
- Redis 慢。
- 第三方接口慢。
- MQ 等待久。
- 下游 gRPC 超时。

### 4. 看错误 span

快速定位哪个服务返回错误。

### 5. 看跨服务依赖

观察订单服务依赖哪些下游，下游错误是否影响上游。

## Prometheus 指标设计

### Go 服务常见指标

运行时指标：

- goroutines。
- heap allocation。
- GC duration。
- CPU。
- open file descriptors。

HTTP 指标：

- request total。
- request duration histogram。
- status code。
- inflight requests。

业务指标：

- orders_created_total。
- waybill_accepted_total。
- tracking_event_ingested_total。
- payment_callback_total。
- customs_push_failed_total。

下游指标：

- carrier_request_total。
- carrier_request_duration_seconds。
- carrier_error_total。
- customs_request_timeout_total。

MQ 指标：

- producer_send_failed_total。
- consumer_lag。
- consume_duration_seconds。
- dead_letter_total。

## Prometheus Go client 示例

```go
package metrics

import (
    "net/http"
    "time"

    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
    requestTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Namespace: "logistics",
            Subsystem: "api",
            Name:      "requests_total",
            Help:      "Total number of API requests.",
        },
        []string{"method", "path", "code"},
    )

    requestDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Namespace: "logistics",
            Subsystem: "api",
            Name:      "request_duration_seconds",
            Help:      "API request duration in seconds.",
            Buckets:   prometheus.DefBuckets,
        },
        []string{"method", "path"},
    )
)

func Register() {
    prometheus.MustRegister(requestTotal, requestDuration)
    http.Handle("/metrics", promhttp.Handler())
}

func Observe(method, path, code string, start time.Time) {
    requestTotal.WithLabelValues(method, path, code).Inc()
    requestDuration.WithLabelValues(method, path).Observe(time.Since(start).Seconds())
}
```

注意 label 不要使用高基数字段：

不要把这些作为 label：

- order_id。
- tracking_no。
- user_id。
- request_id。
- full URL。

这些会导致时序数量爆炸。

可以作为 label：

- service。
- method。
- route pattern。
- status code。
- carrier。
- tenant tier。
- region。

## Prometheus 告警设计

### 1. 错误率告警

```text
5 分钟内 5xx 错误率 > 1%
```

### 2. 延迟告警

```text
P99 延迟 > 1s 持续 10 分钟
```

### 3. MQ 积压告警

```text
consumer lag 持续增长
```

### 4. 第三方接口告警

```text
DHL 接口错误率 > 5%
海关接口 timeout > 阈值
```

### 5. 资源告警

```text
内存接近容器限制
Goroutine 数持续增长
DB 连接池耗尽
Redis 错误率升高
```

### 6. 业务告警

```text
订单创建失败率升高
轨迹事件入库失败
支付回调处理失败
清关任务超时数量增加
```

## RED 和 USE 指标方法

### RED

适合服务接口：

- Rate：请求速率。
- Errors：错误数量或错误率。
- Duration：耗时。

例如：

```text
order-service CreateOrder QPS
CreateOrder error rate
CreateOrder P95/P99 duration
```

### USE

适合资源：

- Utilization：利用率。
- Saturation：饱和度。
- Errors：错误。

例如：

```text
CPU utilization
DB connection saturation
disk/network errors
```

## 日志关联

日志要结构化，且带 trace_id。

示例字段：

```json
{
  "level": "error",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "span_id": "00f067aa0ba902b7",
  "order_id": "1001",
  "tracking_no": "YT123",
  "tenant_id": "2001",
  "carrier": "DHL",
  "msg": "customs push timeout"
}
```

这样可以：

- 从 trace 找相关日志。
- 从日志反查 trace。
- 从订单号聚合所有事件。

## 采样策略

高 QPS 系统不能把所有 trace 都上报。

常见策略：

### 1. 固定比例采样

例如采 1% 或 10%。

### 2. ParentBased 采样

如果上游已经决定采样，下游沿用上游决策，避免链路断裂。

### 3. 错误请求全采样

错误、超时、慢请求尽量保留。

### 4. 重要业务全采样

例如支付、扣费、清关异常、库存扣减失败可以提高采样率。

### 5. Collector 端 tail sampling

先接收一段 trace，再根据是否错误、是否慢请求决定保留。适合排查慢请求，但对 Collector 资源要求更高。

## 跨境物流排障案例

### 案例 1：用户反馈下单慢

排查路径：

1. 通过 order_id 查日志，拿到 trace_id。
2. 在 Jaeger 查 trace。
3. 发现 `inventory-service.ReserveStock` span 耗时 800ms。
4. 看 Prometheus 发现库存服务 DB P99 升高。
5. 查 DB 慢查询，发现库存流水表索引不合理。

### 案例 2：轨迹更新延迟

排查路径：

1. Prometheus 告警 `tracking_events_consumer_lag` 增长。
2. 查看消费者处理耗时 histogram。
3. Jaeger 中看到 MongoDB 写入 span 变慢。
4. 日志按 tracking_no 查到某承运商 payload 异常导致反复重试。
5. 将毒性消息转入死信队列，消费者恢复。

### 案例 3：海关接口超时

排查路径：

1. Prometheus 告警 `customs_request_timeout_total` 增长。
2. Jaeger 看到 `customs-api.PushOrder` span 大量超时。
3. 熔断器打开，订单接收服务继续返回 accepted。
4. 清关任务进入延迟重试队列。
5. 对账任务后续补偿。

## 面试回答模板

跨境物流链路长，服务多，还依赖海外承运商和海关接口，所以我会从 Trace、Metrics、Logs 三层做可观测性。Trace 用 OpenTelemetry 统一埋点和传播，在 HTTP、gRPC、MQ 中透传 trace context；数据通过 OTLP 上报到 OpenTelemetry Collector，再导出到 Jaeger 查看完整调用链。

Metrics 方面，Go 服务会暴露 Prometheus 指标，包括 QPS、错误率、P95/P99 延迟、Goroutine、GC、DB 连接池、Redis 命中率、MQ lag、第三方接口耗时和错误率。告警按 RED 和 USE 思路设计，比如接口错误率、P99 延迟、MQ 积压、海关接口 timeout、内存接近容器限制等。

日志必须结构化，并带 trace_id、span_id、order_id、tracking_no、tenant_id、carrier。这样一个订单异常时，可以从订单号查日志拿 trace_id，再到 Jaeger 看链路，最后结合 Prometheus 指标判断是哪个服务、数据库、MQ 还是第三方接口的问题。

## 常见追问

### 1. Trace 和日志有什么区别？

Trace 适合看一次请求跨服务的路径和耗时，日志适合看具体事件和错误细节。两者要通过 trace_id 关联。

### 2. 为什么不能把 order_id、tracking_no 放到 Prometheus label？

因为这些是高基数字段，会产生海量时间序列，导致 Prometheus 内存和查询压力爆炸。它们应该放日志和 trace attributes，而不是 metrics label。

### 3. MQ 异步链路如何保持 trace 不断？

生产消息时把 trace context 写入消息 header，消费者从 header 提取 context，再创建 consumer span。这样 HTTP 请求、MQ publish、MQ consume、后续 RPC 可以在同一个 trace 下。

### 4. Jaeger 和 OpenTelemetry 是什么关系？

OpenTelemetry 负责生成、采集和导出遥测数据，Jaeger 是常用的分布式追踪后端和查询 UI。现在更推荐应用使用 OpenTelemetry SDK/协议，把 trace 发送到 Collector 或 Jaeger，而不是使用旧的 Jaeger 客户端 SDK。

### 5. Prometheus 和 OpenTelemetry Metrics 怎么选？

如果团队已有 Prometheus 体系，Go 服务可以直接用 `prometheus/client_golang` 暴露 `/metrics`。如果希望统一 trace、metrics、logs 的采集管道，可以使用 OpenTelemetry metrics 通过 Collector 导出到 Prometheus 或其他后端。实际项目中两种方式都常见，关键是指标命名、label 控制、告警和 dashboard 要统一。

## 参考资料

- OpenTelemetry Go 官方文档：<https://opentelemetry.io/docs/languages/go/>
- OpenTelemetry Collector 官方文档：<https://opentelemetry.io/docs/collector/>
- OpenTelemetry Components：<https://opentelemetry.io/docs/concepts/components/>
- Jaeger APIs / OTLP 支持说明：<https://www.jaegertracing.io/docs/1.60/apis/>
- Prometheus Go 应用埋点指南：<https://prometheus.io/docs/guides/go-application/>
- Prometheus Go client：<https://github.com/prometheus/client_golang>

