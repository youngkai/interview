# 接口和类型断言的原理

## 30 秒回答

Go 的接口是一种动态类型机制。一个接口值内部包含动态类型和动态值。只要某个类型实现了接口要求的方法集，就隐式实现了这个接口，不需要显式声明。

类型断言用于从接口值中取出具体类型，语法是 `v, ok := x.(T)`。如果接口里的动态类型是 `T` 或实现了接口 `T`，断言成功。空接口 `interface{}` 或 `any` 可以承载任意类型，但使用时要通过类型断言、type switch 或反射恢复具体类型。

## 接口的本质

Go 接口定义的是行为，不是数据。

```go
type Reader interface {
    Read(p []byte) (n int, err error)
}
```

只要某个类型有这个方法，就实现了接口：

```go
type FileReader struct{}

func (FileReader) Read(p []byte) (int, error) {
    return 0, nil
}
```

不需要写：

```go
implements Reader
```

这叫隐式实现。

## 接口值的组成

接口值可以理解为两部分：

```text
(dynamic type, dynamic value)
```

例如：

```go
var r io.Reader
f := &os.File{}
r = f
```

此时 `r` 内部保存：

- 动态类型：`*os.File`
- 动态值：`f` 的指针值

## 空接口 any

```go
var x any
x = 1
x = "hello"
x = []int{1, 2, 3}
```

`any` 是 `interface{}` 的别名，表示没有方法要求，所以任意类型都实现它。

适合场景：

- 通用容器。
- JSON 解析。
- 日志字段。
- 泛型出现前的一些通用 API。

但过度使用 `any` 会损失类型安全。

## nil 接口陷阱

这是 Go 面试高频点。

```go
type MyError struct{}

func (*MyError) Error() string {
    return "my error"
}

func returnsError() error {
    var err *MyError = nil
    return err
}

func main() {
    err := returnsError()
    fmt.Println(err == nil) // false
}
```

原因是返回的接口值不是完全 nil，而是：

```text
(*MyError, nil)
```

接口只有动态类型和动态值都为 nil 时，才等于 nil。

```go
var err error = nil
```

这是：

```text
(nil, nil)
```

才是真正的 nil 接口。

## 方法集和值接收者、指针接收者

### 值接收者

```go
type T struct{}

func (T) M() {}
```

`T` 和 `*T` 都实现包含 `M` 的接口。

### 指针接收者

```go
type T struct{}

func (*T) M() {}
```

只有 `*T` 实现包含 `M` 的接口，`T` 不实现。

示例：

```go
type I interface {
    M()
}

type T struct{}

func (*T) M() {}

var _ I = &T{} // ok
var _ I = T{}  // compile error
```

原因是 `T` 的方法集不包含指针接收者方法。

## 类型断言

语法：

```go
v, ok := x.(T)
```

如果断言成功：

- `v` 是转换后的值。
- `ok` 是 true。

如果失败：

- `v` 是 T 的零值。
- `ok` 是 false。

示例：

```go
var x any = "hello"

s, ok := x.(string)
if ok {
    fmt.Println(s)
}
```

不带 ok 的写法失败会 panic：

```go
s := x.(string)
```

面试和生产代码里，除非非常确定，否则推荐 `v, ok` 写法。

## 类型断言的两种情况

### 1. 断言为具体类型

```go
var x any = 123
v, ok := x.(int)
```

要求接口内部的动态类型就是 `int`。

### 2. 断言为接口类型

```go
var x any = bytes.NewBuffer(nil)
v, ok := x.(io.Reader)
```

要求接口内部的动态类型实现 `io.Reader`。

## type switch

当要判断多个类型时，用 type switch。

```go
func handle(x any) {
    switch v := x.(type) {
    case string:
        fmt.Println("string:", v)
    case int:
        fmt.Println("int:", v)
    case io.Reader:
        fmt.Println("reader:", v)
    default:
        fmt.Println("unknown")
    }
}
```

## 接口的使用原则

### 1. 接口应该小

Go 推崇小接口，例如：

```go
type Reader interface {
    Read([]byte) (int, error)
}
```

小接口更容易实现、组合和测试。

### 2. 接口由使用方定义

通常谁使用抽象，谁定义接口。

例如 service 依赖 repository：

```go
type UserStore interface {
    GetUser(ctx context.Context, id int64) (*User, error)
}

type UserService struct {
    store UserStore
}
```

这样 service 只依赖自己需要的方法，而不是被具体实现牵着走。

### 3. 不要过早抽象

如果只有一个实现，而且没有测试隔离或替换需求，可以先不用接口。接口应该来自真实的解耦需求。

## 接口的性能注意点

接口调用通常涉及动态分派，可能影响内联和逃逸分析。大多数业务场景不用过度担心，但高性能热点路径需要通过 benchmark 判断。

可能的成本：

- 动态派发。
- 装箱。
- 逃逸到堆。
- 反射和类型断言开销。

优化原则：先写清楚，再用 pprof 和 benchmark 定位热点。

## 面试回答模板

Go 的接口是隐式实现的，只要类型实现了接口的方法集，就自动满足接口。接口值内部可以理解为动态类型和动态值两部分，这也是 nil 接口陷阱的原因：如果一个接口保存了 `(*T, nil)`，它本身并不等于 nil，因为动态类型不为空。

类型断言用于从接口中取出具体类型，常用 `v, ok := x.(T)`，失败不会 panic。如果要判断多种类型，可以用 type switch。断言目标既可以是具体类型，也可以是接口类型。

实际设计中，我会尽量定义小接口，并且倾向于在使用方定义接口。这样依赖更小，也方便测试替换。不会为了抽象而抽象。

## 常见追问

### 1. `interface{}` 和 `any` 的区别是什么？

没有本质区别。`any` 是 `interface{}` 的类型别名，可读性更好。

### 2. 为什么 `var err error = (*MyError)(nil)` 不等于 nil？

因为接口值中保存了动态类型 `*MyError`，虽然动态值是 nil，但动态类型不是 nil，所以接口整体不是 nil。

### 3. 指针接收者和值接收者对接口实现有什么影响？

值接收者方法属于 `T` 和 `*T` 的方法集；指针接收者方法只属于 `*T` 的方法集。因此如果接口方法由指针接收者实现，只有指针类型实现该接口。

### 4. 类型断言失败会怎样？

如果使用 `v, ok := x.(T)`，失败时 `ok=false`，不会 panic。如果使用 `v := x.(T)`，失败会 panic。

