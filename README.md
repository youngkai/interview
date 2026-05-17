# Go 面试专题目录

这组笔记按面试高频点拆成独立 Markdown 文件。每篇都包含核心结论、底层原理、使用场景、面试回答模板和常见追问。

## 目录

1. [Goroutine 与线程的区别](./01-goroutine-vs-thread.md)
2. [GMP 调度模型](./02-gmp-scheduler.md)
3. [Channel、select 的使用场景](./03-channel-select.md)
4. [context 包的使用：超时、取消、请求链路](./04-context.md)
5. [defer、panic、recover 的实际应用](./05-defer-panic-recover.md)
6. [slice、map 的底层实现和内存管理](./06-slice-map-internals.md)
7. [接口和类型断言的原理](./07-interface-type-assertion.md)
8. [GC 对高并发系统的影响](./08-gc-impact-high-concurrency.md)
9. [内存泄漏和内存优化](./09-memory-leak-optimization.md)
10. [高并发下的锁优化](./10-lock-optimization.md)
11. [Go 并发实战例题](./11-practical-concurrency-questions.md)
12. [微服务架构](./12-microservice-architecture.md)
13. [如何设计微服务接口](./13-microservice-api-design.md)
14. [RPC vs HTTP REST](./14-rpc-vs-http-rest.md)
15. [数据一致性问题：分布式事务、最终一致性](./15-distributed-consistency.md)
16. [高并发和高可用](./16-high-concurrency-high-availability.md)
17. [负载均衡](./17-load-balancing.md)
18. [缓存策略：Redis 和 Memcached](./18-cache-strategy.md)
19. [消息队列：Kafka 和 RabbitMQ](./19-message-queue.md)
20. [数据库分库分表策略](./20-database-sharding.md)
21. [跨境物流系统设计实战题](./21-logistics-system-design-practical.md)
22. [栈、队列、链表、哈希表实际应用场景](./22-data-structure-applications.md)
23. [并发安全的数据结构](./23-concurrent-safe-data-structures.md)
24. [排序、搜索、查找优化](./24-sorting-search-optimization.md)
25. [分布式系统常用算法](./25-distributed-common-algorithms.md)
26. [数据结构和分布式算法实战题](./26-data-structure-practical-questions.md)
27. [MySQL/PostgreSQL 调优](./27-mysql-postgresql-tuning.md)
28. [Redis 缓存策略和分布式锁](./28-redis-cache-distributed-lock.md)
29. [MongoDB 等 NoSQL 使用场景](./29-mongodb-nosql-scenarios.md)
30. [数据一致性和幂等性处理](./30-data-consistency-idempotency.md)
31. [数据库和缓存实战题](./31-database-practical-questions.md)
32. [高级 Golang 系统设计必考题](./32-advanced-golang-system-design.md)
33. [高级 Go Runtime、gRPC 与 Redis 分布式锁](./33-advanced-go-runtime-grpc-redis-lock.md)
34. [跨境物流高并发场景实战](./34-cross-border-high-concurrency-scenarios.md)
35. [系统监控与可观测性](./35-observability-opentelemetry-jaeger-prometheus.md)
36. [Go 核心高频强化版](./36-go-core-high-frequency-review.md)
37. [高级后端综合面试强化](./37-senior-backend-synthesis-review.md)
38. [慢查询索引优化与消息队列可靠性强化](./38-db-index-mq-reliability-review.md)
39. [Go HTTP Client/Server 与网络超时调优](./39-go-http-network-timeout.md)
40. [MySQL MVCC、锁与死锁排查](./40-mysql-mvcc-locks-deadlock.md)
41. [Redis 与 Kafka 深入机制](./41-redis-kafka-advanced-mechanisms.md)
42. [Go 服务优雅停机与 Kubernetes 部署治理](./42-graceful-shutdown-kubernetes.md)
43. [Go 高级后端面试复习路线](./43-review-roadmap.md)
44. [高级 Golang 高频 30 问速背版](./44-top-30-interview-qa.md)

## 复习建议

- 第一轮：只背每篇的“30 秒回答”。
- 第二轮：补充“底层原理”和“常见追问”。
- 第三轮：结合自己的项目，把每个知识点落到真实案例里。
