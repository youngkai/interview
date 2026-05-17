# slice、map 的底层实现和内存管理

## 30 秒回答

slice 是对底层数组的一层描述，包含指针、长度和容量。append 时如果容量足够，会复用原数组；容量不够会分配新数组并拷贝数据，所以多个 slice 共享同一个底层数组时要注意修改互相影响和内存滞留。

map 是哈希表，底层由 bucket 组织，每个 bucket 存多个 key/value，并通过哈希值定位。map 扩容不是一次性完成，而是渐进式迁移，避免一次扩容造成长时间停顿。map 不是并发安全的，并发读写需要加锁、用 `sync.Map` 或其他并发控制。

## slice 底层结构

slice 本身不是数组，它是一个描述符，通常可以理解为：

```go
type slice struct {
    array unsafe.Pointer
    len   int
    cap   int
}
```

含义：

- `array`：指向底层数组。
- `len`：当前元素个数。
- `cap`：从起始位置到底层数组末尾的容量。

示例：

```go
a := [5]int{1, 2, 3, 4, 5}
s := a[1:3]
```

此时：

- `s` 内容是 `[2, 3]`。
- `len(s) == 2`。
- `cap(s) == 4`，因为从 `a[1]` 到数组末尾还有 4 个位置。

## slice 共享底层数组

```go
s1 := []int{1, 2, 3, 4}
s2 := s1[1:3]
s2[0] = 20

fmt.Println(s1) // [1 20 3 4]
```

`s1` 和 `s2` 共享同一个底层数组，所以修改 `s2` 会影响 `s1`。

## append 的行为

### 容量足够时复用底层数组

```go
s1 := make([]int, 2, 4)
s1[0], s1[1] = 1, 2
s2 := append(s1, 3)
s2[0] = 100

fmt.Println(s1[0]) // 100
```

因为容量足够，`append` 没有分配新数组。

### 容量不足时分配新数组

```go
s1 := []int{1, 2}
s2 := append(s1, 3)
s2[0] = 100

fmt.Println(s1[0]) // 1
```

容量不足时会分配新数组，`s1` 和 `s2` 不再共享。

## slice 扩容规律

slice 扩容策略是 runtime 实现细节，不应该依赖精确倍数。通常可以概括为：

- 小容量时倾向于较快增长。
- 大容量时增长比例会放缓。
- 实际容量还会受元素大小和内存分配规格影响。

面试中不要死背“永远 2 倍扩容”，这是不准确的。

## slice 内存滞留

切小片可能导致大数组无法被 GC。

```go
func loadData() []byte {
    data := make([]byte, 10<<20) // 10 MB
    return data[:10]
}
```

虽然只返回 10 字节，但返回的 slice 仍引用整个 10 MB 底层数组，导致大数组无法释放。

解决方式：复制需要的数据。

```go
func loadData() []byte {
    data := make([]byte, 10<<20)
    result := make([]byte, 10)
    copy(result, data[:10])
    return result
}
```

或：

```go
return append([]byte(nil), data[:10]...)
```

## slice 预分配

如果能预估长度，应该预分配容量，减少扩容和拷贝。

```go
result := make([]Item, 0, len(input))
for _, v := range input {
    result = append(result, convert(v))
}
```

优点：

- 减少内存分配次数。
- 减少数据拷贝。
- 降低 GC 压力。

## map 底层结构

Go map 是哈希表。可以简单理解为：

```text
hash(key) -> bucket -> key/value
```

每个 bucket 存放多个 key/value。查找时：

1. 计算 key 的哈希值。
2. 根据哈希值定位 bucket。
3. 在 bucket 内比较 key。
4. 找到对应 value。

如果 bucket 放不下，会使用溢出 bucket。

## map 扩容

map 会在负载因子过高或溢出 bucket 太多时扩容。

扩容特点：

- 不是一次性搬完所有 key/value。
- 采用渐进式迁移。
- 每次读写时顺便迁移一部分旧 bucket。

这样可以避免一次性扩容导致明显停顿。

## map 为什么遍历无序

Go 语言故意不保证 map 遍历顺序，并且 runtime 还会引入随机性。

原因：

- 哈希表本身不适合保证顺序。
- 防止开发者依赖不稳定顺序。
- 有助于暴露错误假设。

如果需要稳定顺序，应该取出 key 后排序：

```go
keys := make([]string, 0, len(m))
for k := range m {
    keys = append(keys, k)
}
sort.Strings(keys)

for _, k := range keys {
    fmt.Println(k, m[k])
}
```

## map 并发安全

普通 map 不是并发安全的。

并发读写可能 panic：

```text
fatal error: concurrent map read and map write
```

解决方式：

### 1. Mutex/RWMutex

```go
type Cache struct {
    mu sync.RWMutex
    m  map[string]Value
}

func (c *Cache) Get(key string) (Value, bool) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    v, ok := c.m[key]
    return v, ok
}

func (c *Cache) Set(key string, v Value) {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.m[key] = v
}
```

### 2. sync.Map

适合读多写少、key 相对稳定、缓存类场景。

```go
var m sync.Map
m.Store("a", 1)
v, ok := m.Load("a")
```

### 3. 分片 map

把一个大 map 拆成多个 shard，每个 shard 一把锁，降低锁竞争。

## map 内存管理注意点

### 1. 删除 key 不一定立刻释放内存

```go
delete(m, key)
```

删除元素后，map 占用的 bucket 不一定立即归还给操作系统。大量插入后再删除，map 可能仍占较大内存。

常见处理：

- 重建 map。
- 分批迁移存活数据。
- 控制缓存上限。

### 2. value 很大时考虑存指针

map value 如果是大结构体，赋值和扩容搬迁成本可能较高。可以考虑存指针：

```go
map[string]*LargeValue
```

但指针会增加 GC 扫描压力，需要结合实际权衡。

### 3. key 类型要可比较

map key 必须是可比较类型。slice、map、function 不能作为 key。

## 面试回答模板

slice 底层是一个包含指针、长度和容量的结构，指针指向底层数组。切片之间可能共享同一个底层数组，所以修改一个 slice 可能影响另一个。append 时如果容量够，会复用底层数组；容量不够才会分配新数组并拷贝。实际开发中要注意预分配容量和小 slice 引用大数组导致的内存滞留。

map 底层是哈希表，通过 key 的哈希值定位 bucket，每个 bucket 存多个 key/value，冲突时可能使用溢出 bucket。map 扩容是渐进式的，不会一次性迁移所有数据。普通 map 不是并发安全的，并发读写要加锁、用分片 map 或 `sync.Map`。

## 常见追问

### 1. slice 作为函数参数传递会发生什么？

传递的是 slice 头的副本，底层数组仍然共享。所以函数内修改已有元素会影响外部；但如果 append 触发扩容，函数内的新 slice 可能指向新数组，不再影响外部 slice 头。

### 2. nil slice 和空 slice 区别是什么？

```go
var a []int        // nil slice
b := []int{}      // empty slice
c := make([]int, 0)
```

它们 `len` 都是 0，都可以 append。但 `a == nil` 为 true，`b == nil` 和 `c == nil` 为 false。JSON 编码时也可能表现不同，nil slice 可能编码为 `null`，空 slice 编码为 `[]`。

### 3. map 为什么不能取元素地址？

因为 map 扩容时元素位置可能变化，如果允许取地址，扩容后地址会失效。所以 Go 不允许直接对 map 元素取地址。

### 4. map 删除大量元素后内存不下降怎么办？

可以重建 map，把仍然有效的数据复制到新 map。也要检查是否有缓存无上限、key 泄漏、value 引用大对象等问题。

