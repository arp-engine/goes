# goes

`goes` 是 **Go Embedded Script** 的缩写，是面向 Go 宿主程序的内嵌脚本运行时。它解析 Go 源码，对支持的语法子集做类型检查，再生成字节码交给虚拟机（VM）执行。

`goes` 的语法与语义锚点是原生 Go 1.23.x：已支持的能力应尽量遵循 Go 1.23.x 语义；未列入支持范围的能力应被明确拒绝，而不是用近似语义替代。

## 快速开始

在仓库根目录执行，构建需要 Go 1.23 或更新版本。

运行一个示例目录：

```powershell
go run ./cmd/goes ./testdata/programs/scalar
```

支持运行单个 `.go` 或 `.goes` 文件，也支持加载目录中的这两种文件：

```powershell
go run ./cmd/goes ./path/to/main.go
```

运行使用相对路径导入的多包示例：

```powershell
go run ./cmd/goes ./testdata/programs/import_test
```

`"./lib"`、`"../lib"` 相对于导入方目录解析；不以 `./` 或 `../` 开头的包路径则相对于 `-m` 指定的本地模块根目录解析。例如，`import "lib"` 会查找模块根目录下的 `lib` 子目录：

```powershell
go run ./cmd/goes -m ./path/to/module ./path/to/module
```

## 常见用法

宿主使用 `goes/eval` 和 `goes/runtime`。以下片段为突出调用流程，省略了部分错误处理。

### 1. 运行脚本文件

```go
result, _ := eval.RunPath("./scripts/my_script.goes")
fmt.Println(result.Int())
```

### 2. 编译并运行源码字符串

```go
program, _ := eval.CompileSource(`package main
func main() int {
    return 1 + 2
}
`)
result, _ := eval.RunFunctionSource(program, "main")
fmt.Println(result.Int()) // 3
```

### 3. 调用指定函数

```go
program, _ := eval.CompileSource(`package main
func Add(a int, b int) int {
    return a + b
}
func main() int { return 0 }
`)
result, _ := eval.RunFunctionSource(program, "Add", runtime.IntValue(2), runtime.IntValue(3))
fmt.Println(result.Int()) // 5
```

函数有多个返回值时，使用 `eval.RunFunctionValues` 或 `eval.RunFunctionWithHostValues` 获取完整的 `[]runtime.Value`。

### 4. Hook 风格调用

在编译前用 `HostRegistry` 注册 Go 结构体。注册时会自动提取类型名和字段信息，脚本可以直接使用该类型，不需要再声明一份。

```go
type State struct{ Counter int }

host := runtime.NewHostRegistry()
host.MustRegisterType(State{})

module := eval.MustCompileHookModuleWithHost(`package main
func OnEvent(state State, value int) State {
    state.Counter += value
    return state
}
func main() int { return 0 }
`, host)

state, _ := eval.InvokeHook(module, "OnEvent", State{Counter: 1}, 5, nil)
fmt.Println(state.(State).Counter) // 6
```

`InvokeHook` 把宿主的状态和可选上下文传入脚本，并将结果转换回宿主的状态类型；它不会自动注册参数的类型。注册的宿主类型必须是命名结构体，字段须为受支持的基础标量并已导出。找不到指定 hook 时返回原状态。

### 5. 宿主函数调用

```go
program, host, _ := eval.CompileSourceWithHostFuncs(`package main
func main() int {
    return addOne(10)
}
`, map[string]any{
    "addOne": func(ctx *runtime.HostContext, x int) int {
        return x + 1
    },
})

result, _ := eval.RunFunctionWithHost(program, "main", host, nil)
fmt.Println(result.Int()) // 11
```

自动包装的宿主函数以 `*runtime.HostContext` 为首参，支持基础标量、多返回值、可变参和可选的尾部 `error`。这个 `error` 由宿主运行时处理，不是脚本的额外返回值。也可通过 `runtime.HostRegistry` 显式声明、注册宿主函数、结构体及其方法和常量。

### 6. 异步挂起与恢复

```go
program, host, _ := eval.CompileSourceWithHostFuncs(`package main
func fetchData(id int) int {
    return asyncFetch(id)
}
func main() int { return 0 }
`, map[string]any{
    "asyncFetch": func(ctx *runtime.HostContext, id int) (int, error) {
        return 0, runtime.Yield(id)
    },
})

_, err := eval.RunFunctionWithHost(program, "fetchData", host, nil, runtime.IntValue(42))
var suspension *runtime.Suspension
if errors.As(err, &suspension) {
    results, _ := suspension.ResumeValues(runtime.IntValue(100))
    fmt.Println(results[0].Int()) // 100
}
```

挂起对象只能恢复一次。需要保存挂起状态时，可用 `suspension.Snapshot()` 获取快照，之后用 `eval.RestoreSnapshot` 在新 VM 中恢复挂起对象，再调用 `ResumeValues` 继续执行。恢复时须提供字节码哈希匹配的程序及所需的宿主注册表和上下文；宿主任意数据不会随 VM 自动恢复。

### 7. 断点与调用栈

用 `runtime.SetBreakpoint` 按脚本文件和行号设置断点，以 `runtime.SetTrapHandler` 接收源码位置、调用栈和变量，再通过 `eval.RunPathWithDebug` 或 `eval.InvokeHookWithDebug` 启用调试执行。直接编译源码时，可用 `eval.CompileSourceFile` 保留调试文件名。VM 运行时错误会附带 VM 调用栈，而不是 Go 的 goroutine 调用栈。

## 与 Go 1.23.x 的主要差异

`goes` 的语法和语义以原生 Go 1.23.x 为锚点，但当前定位是受限 Go 子集，不等同于完整 Go 1.23.x 实现。主要差异包括：

### 包与运行模型

- 文件入口使用 `package main`，多包脚本可导入本地非 `main` 包，仅能访问其导出符号。不按 `go.mod` 或 GOPATH 解析依赖，也不能直接导入标准库或第三方模块。
- 脚本需定义 `func main() int`。命令行运行该入口并打印整数结果；宿主可按名称调用其他函数，不执行 `main`。

### 类型系统

- 基础类型只覆盖 `bool`、`int`、`int64`、`float64`、`string`。
- 不支持完整预声明类型集合，例如 `uint` 系列、`uintptr`、`byte` / `rune` 别名、`complex64`、`complex128`。
- 不支持泛型、类型参数、`interface` 类型、`chan` 类型。
- `nil` 只做最小支持：可用于可 nil 类型的赋值与比较。

### 控制流

- 不支持 `go`、`select`、`defer`、`goto`、`fallthrough`。
- 不支持 label。
- `for range` 当前面向 map / slice / array，并包含 integer range 子集；不支持 string range 和 Go 1.23 的 range-over-function iterator。

### 函数、方法与调用

- 支持普通函数、递归、闭包、函数值、多返回值和可变参的当前子集。
- 多返回值只支持 Go 允许的多值上下文，不作为一等 tuple/object 暴露。
- 支持值接收者方法和值接收者方法值。
- 指针接收者方法调用只覆盖当前支持场景；不支持指针接收者方法值。
- 不支持 method expressions，例如 `T.M` / `(*T).M`。

### 复合类型与字面量

- 支持 struct、array、slice、map 和指针的当前子集。
- 不支持嵌入字段和提升方法。
- 支持 keyed struct literal；不支持 keyed array literal 和 keyed slice literal。
- array literal 当前比 Go 更严格：元素数量必须匹配数组长度，不做省略元素补零。
- nested positional composite literal 只覆盖当前支持子集，不等同完整 Go composite literal 语义。

### 内建函数与表达式

- 当前内建函数覆盖 `len`、`make`、`append`、`delete`、`clear`，其中 `make` 支持 map 与 slice，`append` 支持 slice 元素追加和 slice 展开追加，`clear` 支持 map 清空和 slice 元素置零。
- 不支持 `cap`、`copy`、`new`、`panic`、`recover`、`min`、`max`、`close`、`complex`、`real`、`imag` 等内建函数。
- 不支持 `iota`。
- slice expression 支持省略 low/high 边界、三索引形式，以及 slice / array 的共享底层存储语义；string slicing 支持双索引形式。
- 支持 map comma-ok 读取；仍不支持 type assertion、type switch 等完整 Go 语义。
- 常量和未定类型常量语义是简化版，不等同于 `go/types` 的完整规则。

### 宿主互操作

- VM 代码不能直接调用标准库或任意宿主 API，必须通过 host registry / host builtin 显式暴露。
- 宿主桥接支持基础标量和具有匹配模式、仅含受支持标量导出字段的命名结构体。标量可变参可由 VM 转换为 Go 切片供宿主调用，不表示支持任意容器的双向转换。
- 异步等待通过 host yield/resume 表达，不提供 goroutine / channel / select 语义。

## 与 Yaegi 的性能对比

2026-11-19 实测：Windows/amd64，Intel Core Ultra 5 245KF，Go 1.26.1，Yaegi v0.16.1。下表为每个负载 5 次采样的运行耗时中位数，单位为毫秒，不含脚本编译和 Yaegi 的包加载。三个实现的返回值在每次采样中均一致。

| 负载 | 原生 Go | goes | Yaegi | 耗时更低 |
| --- | ---: | ---: | ---: | --- |
| 斐波那契递归 | 21.38 | 4,369.01 | 6,487.44 | goes（1.48 倍） |
| 循环与分支 | 1.00 | 43.82 | 80.37 | goes（1.83 倍） |
| 结构体与数组 | 1.00 | 426.35 | 120.19 | Yaegi（3.55 倍） |
| map 读写 | 17.55 | 741.72 | 176.31 | Yaegi（4.21 倍） |
| 切片、数组和 map 的 `range` | 7.01 | 680.97 | 196.98 | Yaegi（3.46 倍） |

倍率为较长耗时除以较短耗时，仅代表本机上的这些负载。多数单次编译耗时低于本次计时分辨率，不据此比较编译速度。参见[完整报告](docs/benchmarks/2026-11-19-yaegi-benchmark.md)。

复跑（建议使用 64 位环境）：

```powershell
go run ./cmd/benchshot -samples 5
```

## 测试

运行完整测试：

```powershell
go test ./...
```

运行 VM 行为测试：

```powershell
go test ./tests/vm
```

运行原生 Go 与 goes 的 Go 基准测试：

```powershell
go test ./tests/bench -run '^$' -bench . -benchmem
```

VM 差分测试目前只比较整数返回值，并通过改写受控样例中的 `func main() int` 生成原生 Go 对照程序，不适用于任意源码。
